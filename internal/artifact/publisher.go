package artifact

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Publisher struct {
	Pool             *pgxpool.Pool
	Store            Store
	Key              ed25519.PrivateKey
	MinWorkerVersion string
	Log              *slog.Logger
}

// Publish exports the snapshot's SQLite artifact, uploads it with a signed
// manifest, and records artifact identity on the snapshot row. It does NOT
// move the current pointer — that happens in SetCurrent after the database
// publish succeeds, so a half-finished publication is never visible.
func (p *Publisher) Publish(ctx context.Context, snapshotID int64, version string) (*Manifest, error) {
	dir, err := os.MkdirTemp("", "vapn-artifact-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "routing.sqlite")

	targets, err := ExportSQLite(ctx, p.Pool, snapshotID, version, p.MinWorkerVersion, path)
	if err != nil {
		return nil, fmt.Errorf("export sqlite: %w", err)
	}
	sum, size, err := HashFile(path)
	if err != nil {
		return nil, err
	}

	var v4, v6 int
	if err := p.Pool.QueryRow(ctx, `select coalesce(prefix_count_v4,0), coalesce(prefix_count_v6,0)
		from routing.snapshot where id = $1`, snapshotID).Scan(&v4, &v6); err != nil {
		return nil, err
	}

	m := &Manifest{
		Version:          version,
		CreatedAt:        time.Now().UTC(),
		ObjectKey:        ObjectKeySQLite(version),
		SHA256:           sum,
		SizeBytes:        size,
		PrefixCountV4:    v4,
		PrefixCountV6:    v6,
		TargetCount:      targets,
		MinWorkerVersion: p.MinWorkerVersion,
	}
	if err := Sign(m, p.Key); err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := p.Store.Put(ctx, m.ObjectKey, f, size, "application/vnd.sqlite3"); err != nil {
		return nil, fmt.Errorf("upload artifact: %w", err)
	}
	manifestJSON, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if err := p.Store.Put(ctx, ObjectKeyManifest(version),
		bytes.NewReader(manifestJSON), int64(len(manifestJSON)), "application/json"); err != nil {
		return nil, fmt.Errorf("upload manifest: %w", err)
	}

	// Readback verification: the store is untrusted, so prove the upload
	// round-trips before the snapshot can ever be published.
	if err := p.verifyReadback(ctx, m); err != nil {
		return nil, fmt.Errorf("readback verification: %w", err)
	}

	if _, err := p.Pool.Exec(ctx, `update routing.snapshot
		set artifact_sha256 = $2, artifact_size_bytes = $3, artifact_signature = $4
		where id = $1`, snapshotID, m.SHA256, m.SizeBytes, m.Signature); err != nil {
		return nil, err
	}
	p.Log.Info("artifact published", "version", version,
		"sha256", sum[:12], "size_bytes", size, "targets", targets)
	return m, nil
}

func (p *Publisher) verifyReadback(ctx context.Context, m *Manifest) error {
	rc, err := p.Store.Get(ctx, ObjectKeyManifest(m.Version))
	if err != nil {
		return err
	}
	defer rc.Close()
	var got Manifest
	if err := json.NewDecoder(rc).Decode(&got); err != nil {
		return err
	}
	if err := Verify(got, p.Key.Public().(ed25519.PublicKey)); err != nil {
		return err
	}
	if got.SHA256 != m.SHA256 {
		return fmt.Errorf("stored manifest hash mismatch")
	}
	obj, err := p.Store.Get(ctx, m.ObjectKey)
	if err != nil {
		return err
	}
	defer obj.Close()
	tmp, err := os.CreateTemp("", "vapn-readback-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, obj); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return VerifyFile(tmp.Name(), got)
}

// SetCurrent atomically points workers at version.
func (p *Publisher) SetCurrent(ctx context.Context, version string) error {
	ptr := Pointer{Version: version, ManifestKey: ObjectKeyManifest(version), UpdatedAt: time.Now().UTC()}
	raw, err := json.Marshal(ptr)
	if err != nil {
		return err
	}
	return p.Store.Put(ctx, PointerKey, bytes.NewReader(raw), int64(len(raw)), "application/json")
}

// RollbackTo re-publishes a previous snapshot version: flips database
// statuses and re-points the current pointer. Fails if the version's routing
// data has been pruned.
func (p *Publisher) RollbackTo(ctx context.Context, version string) error {
	var id int64
	var status string
	err := p.Pool.QueryRow(ctx,
		`select id, status from routing.snapshot where version = $1`, version).Scan(&id, &status)
	if err != nil {
		return fmt.Errorf("unknown snapshot version %q: %w", version, err)
	}
	var prefixes int
	if err := p.Pool.QueryRow(ctx,
		`select count(*) from routing.prefix where snapshot_id = $1`, id).Scan(&prefixes); err != nil {
		return err
	}
	if prefixes == 0 {
		return fmt.Errorf("snapshot %s has been pruned; cannot roll back to it", version)
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update routing.snapshot set status = 'superseded'
		where status = 'published' and id <> $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update routing.snapshot
		set status = 'published', published_at = now() where id = $1`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := p.SetCurrent(ctx, version); err != nil {
		return err
	}
	p.Log.Warn("rolled back to snapshot", "version", version)
	return nil
}

// Prune drops routing data and store objects of superseded snapshots beyond
// the newest `retain`, keeping their summary rows for history. The published
// snapshot is never pruned.
func (p *Publisher) Prune(ctx context.Context, retain int) error {
	rows, err := p.Pool.Query(ctx, `
		select id, version from routing.snapshot
		where status = 'superseded'
		order by id desc offset $1`, retain)
	if err != nil {
		return err
	}
	type victim struct {
		id      int64
		version string
	}
	var victims []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.version); err != nil {
			rows.Close()
			return err
		}
		victims = append(victims, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, v := range victims {
		// Closed assignments (and their released leases) still reference the
		// snapshot's targets. They are scheduling history for work that can
		// never be issued again, and leaving them behind makes the target
		// delete below fail on a foreign key — which used to stall every build
		// once RETAIN_SNAPSHOTS was exceeded on a system that had scheduled
		// any work at all.
		if _, err := p.Pool.Exec(ctx, `
			delete from scheduling.lease l using scheduling.assignment a,
			                                    routing.probe_target t
			where l.assignment_id = a.id and a.target_id = t.id
			  and t.snapshot_id = $1`, v.id); err != nil {
			return err
		}
		if _, err := p.Pool.Exec(ctx, `
			delete from scheduling.assignment a using routing.probe_target t
			where a.target_id = t.id and t.snapshot_id = $1`, v.id); err != nil {
			return err
		}
		if _, err := p.Pool.Exec(ctx,
			`delete from routing.probe_target where snapshot_id = $1`, v.id); err != nil {
			return err
		}
		if _, err := p.Pool.Exec(ctx,
			`delete from routing.provider_geo where snapshot_id = $1`, v.id); err != nil {
			return err
		}
		if _, err := p.Pool.Exec(ctx,
			`delete from routing.prefix where snapshot_id = $1`, v.id); err != nil {
			return err
		}
		for _, key := range []string{ObjectKeySQLite(v.version), ObjectKeyManifest(v.version)} {
			if err := p.Store.Delete(ctx, key); err != nil {
				p.Log.Warn("could not delete artifact object", "key", key, "error", err)
			}
		}
		p.Log.Info("pruned snapshot", "version", v.version)
	}
	return nil
}

// PublicKeyBase64 renders the verification key workers pin.
func PublicKeyBase64(key ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/vpsadvisor/ip-discovery/internal/artifact"
)

func readPointer(t *testing.T, root string) artifact.Pointer {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.PointerKey)))
	if err != nil {
		t.Fatal(err)
	}
	var ptr artifact.Pointer
	if err := json.Unmarshal(raw, &ptr); err != nil {
		t.Fatal(err)
	}
	return ptr
}

func readManifest(t *testing.T, root, version string) artifact.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.ObjectKeyManifest(version))))
	if err != nil {
		t.Fatal(err)
	}
	var m artifact.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestArtifactDistribution(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	env := newBuilder(t, pool, writeBview(t))
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}

	ptr := readPointer(t, env.storeRoot)
	m := readManifest(t, env.storeRoot, ptr.Version)
	if err := artifact.Verify(m, env.pubKey); err != nil {
		t.Fatalf("published manifest does not verify: %v", err)
	}
	sqlitePath := filepath.Join(env.storeRoot, filepath.FromSlash(m.ObjectKey))
	if err := artifact.VerifyFile(sqlitePath, m); err != nil {
		t.Fatalf("published artifact does not verify: %v", err)
	}

	// Worker-facing contents: 6 prefixes; 4 targets (the /25 is excluded and
	// the overlapping /16+/17 share one deduplicated address).
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var prefixes, targets int
	if err := db.QueryRow("select count(*) from prefixes").Scan(&prefixes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("select count(*) from targets").Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if prefixes != 6 || targets != 4 {
		t.Fatalf("artifact contents: prefixes=%d targets=%d, want 6/4", prefixes, targets)
	}
	var metaVersion string
	if err := db.QueryRow("select value from meta where key = 'version'").Scan(&metaVersion); err != nil {
		t.Fatal(err)
	}
	if metaVersion != ptr.Version {
		t.Fatalf("artifact meta version %s != pointer version %s", metaVersion, ptr.Version)
	}

	// Tamper with the stored artifact: verification must fail closed.
	if err := os.WriteFile(sqlitePath, append([]byte("x"), byte(0)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := artifact.VerifyFile(sqlitePath, m); err == nil {
		t.Fatal("tampered stored artifact still verifies")
	}
}

func TestRollbackAndPrune(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	bview := writeBview(t)
	env := newBuilder(t, pool, bview)

	// Four runs with RetainSnapshots=2: the oldest superseded snapshot gets
	// pruned by the fourth run.
	versions := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		if err := env.b.Run(ctx); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, readPointer(t, env.storeRoot).Version)
	}

	var prunedPrefixes int
	if err := pool.QueryRow(ctx, `select count(*) from routing.prefix p
		join routing.snapshot s on s.id = p.snapshot_id
		where s.version = $1`, versions[0]).Scan(&prunedPrefixes); err != nil {
		t.Fatal(err)
	}
	if prunedPrefixes != 0 {
		t.Fatalf("oldest snapshot still has %d prefix rows after prune", prunedPrefixes)
	}
	if _, err := os.Stat(filepath.Join(env.storeRoot,
		filepath.FromSlash(artifact.ObjectKeySQLite(versions[0])))); !os.IsNotExist(err) {
		t.Fatal("pruned snapshot's artifact object still in store")
	}
	var summaryStatus string
	if err := pool.QueryRow(ctx, `select status from routing.snapshot where version = $1`,
		versions[0]).Scan(&summaryStatus); err != nil {
		t.Fatal(err) // summary row must survive pruning
	}

	// Rollback to the third run: statuses flip and the pointer moves back.
	if err := env.pub.RollbackTo(ctx, versions[2]); err != nil {
		t.Fatal(err)
	}
	if got := readPointer(t, env.storeRoot).Version; got != versions[2] {
		t.Fatalf("pointer after rollback = %s, want %s", got, versions[2])
	}
	var published string
	if err := pool.QueryRow(ctx,
		`select version from routing.snapshot where status = 'published'`).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if published != versions[2] {
		t.Fatalf("published after rollback = %s, want %s", published, versions[2])
	}

	// Rolling back to the pruned snapshot must be refused.
	if err := env.pub.RollbackTo(ctx, versions[0]); err == nil {
		t.Fatal("rollback to a pruned snapshot was allowed")
	}
}

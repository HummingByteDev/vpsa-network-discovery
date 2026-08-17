package artifact

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite" // pure-Go driver: keeps every binary CGO-free
)

// ExportSQLite writes the worker-facing subset of a snapshot to a SQLite file
// (docs/architecture/06-lifecycles.md): prefixes and targets for local
// validation and enrichment — workers never choose targets from it.
func ExportSQLite(ctx context.Context, pool *pgxpool.Pool, snapshotID int64, version, minWorkerVersion, path string) (targets int, err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := db.Close(); err == nil {
			err = cerr
		}
	}()

	// Columns are only ever added here, never removed or reordered: workers
	// select the columns they know by name, so an older worker reads a newer
	// artifact unchanged.
	if _, err := db.ExecContext(ctx, `
		create table meta (key text primary key, value text not null);
		create table prefixes (
		  prefix text not null, origin_asn integer not null,
		  provider_id text not null, geo_country text, geo_city text,
		  primary key (prefix, origin_asn)
		);
		create table targets (
		  address text primary key, provider_id text not null, prefix text not null,
		  geo_country text, geo_city text
		);`); err != nil {
		return 0, fmt.Errorf("create artifact schema: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for k, v := range map[string]string{
		"version": version, "min_worker_version": minWorkerVersion,
	} {
		if _, err := tx.ExecContext(ctx, "insert into meta (key, value) values (?, ?)", k, v); err != nil {
			return 0, err
		}
	}

	prefixRows, err := pool.Query(ctx, `
		select p.prefix::text, p.origin_asn, a.provider_id::text, p.geo_country, p.geo_city
		from routing.prefix p join routing.asn a on a.asn = p.origin_asn
		where p.snapshot_id = $1`, snapshotID)
	if err != nil {
		return 0, err
	}
	defer prefixRows.Close()
	insPrefix, err := tx.PrepareContext(ctx,
		"insert into prefixes (prefix, origin_asn, provider_id, geo_country, geo_city) values (?, ?, ?, ?, ?)")
	if err != nil {
		return 0, err
	}
	for prefixRows.Next() {
		var prefix, providerID string
		var originASN int64
		var country, city *string
		if err := prefixRows.Scan(&prefix, &originASN, &providerID, &country, &city); err != nil {
			return 0, err
		}
		if _, err := insPrefix.ExecContext(ctx, prefix, originASN, providerID, country, city); err != nil {
			return 0, err
		}
	}
	if err := prefixRows.Err(); err != nil {
		return 0, err
	}

	targetRows, err := pool.Query(ctx, `
		select host(t.address), t.provider_id::text, p.prefix::text,
		       t.geo_country, t.geo_city
		from routing.probe_target t join routing.prefix p on p.id = t.prefix_id
		where t.snapshot_id = $1 and t.active`, snapshotID)
	if err != nil {
		return 0, err
	}
	defer targetRows.Close()
	insTarget, err := tx.PrepareContext(ctx,
		"insert into targets (address, provider_id, prefix, geo_country, geo_city) values (?, ?, ?, ?, ?)")
	if err != nil {
		return 0, err
	}
	for targetRows.Next() {
		var address, providerID, prefix string
		var country, city *string
		if err := targetRows.Scan(&address, &providerID, &prefix, &country, &city); err != nil {
			return 0, err
		}
		if _, err := insTarget.ExecContext(ctx, address, providerID, prefix, country, city); err != nil {
			return 0, err
		}
		targets++
	}
	if err := targetRows.Err(); err != nil {
		return 0, err
	}
	return targets, tx.Commit()
}

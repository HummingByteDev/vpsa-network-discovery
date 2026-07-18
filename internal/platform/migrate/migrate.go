// Package migrate applies SQL migrations in lexical order, tracking applied
// versions in public.schema_migrations and serializing runs with an advisory
// lock so concurrent deploys are safe.
package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryLockID = 0x5641504e // "VAPN"

// Apply runs all pending migrations from dir. It is idempotent.
func Apply(ctx context.Context, pool *pgxpool.Pool, dir string, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", advisoryLockID); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "select pg_advisory_unlock($1)", advisoryLockID)
	}()

	if _, err := conn.Exec(ctx, `create table if not exists public.schema_migrations (
		version text primary key, applied_at timestamptz not null default now())`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no migrations found in %q", dir)
	}
	sort.Strings(files)

	applied := 0
	for _, f := range files {
		version := filepath.Base(f)
		var exists bool
		if err := conn.QueryRow(ctx,
			"select exists(select 1 from public.schema_migrations where version = $1)",
			version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx,
			"insert into public.schema_migrations (version) values ($1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Info("applied migration", "migration", version)
		applied++
	}
	log.Info("migrations up to date", "applied_now", applied, "total", len(files))
	return nil
}

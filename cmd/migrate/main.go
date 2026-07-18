// Command migrate applies the repository's SQL migrations to the configured
// database (see internal/platform/migrate).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/db"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/logging"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/migrate"
)

func main() {
	log := logging.New("migrate")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	dsn := cfg.Require("DB_DSN")
	dir := cfg.String("MIGRATIONS_DIR", "migrations")
	wait := cfg.Duration("DB_WAIT", 60*time.Second)
	if err := cfg.Err(); err != nil {
		log.Error("bad configuration", "error", err)
		os.Exit(1)
	}
	cfg.Dump(log)

	pool, err := db.WaitAndConnect(ctx, dsn, wait)
	if err != nil {
		log.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := migrate.Apply(ctx, pool, dir, log); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

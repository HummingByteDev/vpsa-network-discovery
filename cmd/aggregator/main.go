// Command aggregator computes trust-weighted consensus and publishes results.
// Phase 1 ships the service shell; the aggregation pipeline arrives in Phase 8.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vpsadvisor/ip-discovery/internal/platform/config"
	"github.com/vpsadvisor/ip-discovery/internal/platform/db"
	"github.com/vpsadvisor/ip-discovery/internal/platform/httpserver"
	"github.com/vpsadvisor/ip-discovery/internal/platform/logging"
)

func main() {
	log := logging.New("aggregator")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	addr := cfg.String("HTTP_ADDR", ":8082")
	dsn := cfg.Require("DB_DSN")
	dbWait := cfg.Duration("DB_WAIT", 60*time.Second)
	if err := cfg.Err(); err != nil {
		log.Error("bad configuration", "error", err)
		os.Exit(1)
	}
	cfg.Dump(log)

	pool, err := db.WaitAndConnect(ctx, dsn, dbWait)
	if err != nil {
		log.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	srv := httpserver.New(addr, log)
	srv.AddReadyCheck("postgres", pool.Ping)
	if err := srv.Run(ctx); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

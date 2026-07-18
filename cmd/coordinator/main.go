// Command coordinator is the worker-facing data-plane API and scheduler.
// Phase 1 ships the service shell (config, DB connectivity, health/readiness,
// metrics); the worker API arrives in Phase 4, the scheduler in Phase 7.
package main

import (
	"context"
	"net/http"
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
	log := logging.New("coordinator")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	addr := cfg.String("HTTP_ADDR", ":8080")
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
	srv.Handle("/api/v1/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "coordinator API lands in Phase 4", http.StatusNotImplemented)
	}))
	if err := srv.Run(ctx); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

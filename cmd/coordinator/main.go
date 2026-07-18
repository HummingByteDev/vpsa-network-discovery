// Command coordinator is the worker-facing data-plane API: registration,
// heartbeats, artifact advertisement/download, and the platform admin
// surface. Scheduler and observation intake land in Phases 5–7.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/coordinator"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/audit"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/db"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/httpserver"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/logging"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/registry"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/geo"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/scheduler"
)

func main() {
	log := logging.New("coordinator")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	addr := cfg.String("HTTP_ADDR", ":8080")
	dsn := cfg.Require("DB_DSN")
	dbWait := cfg.Duration("DB_WAIT", 60*time.Second)
	adminToken := cfg.Require("ADMIN_TOKEN")
	devToken := cfg.String("DEV_ENROLLMENT_TOKEN", "")
	schedInterval := cfg.Duration("SCHEDULER_INTERVAL", 30*time.Second)
	redundancy := cfg.Int("REDUNDANCY", 3)
	maxPerWorker := cfg.Int("MAX_ASSIGNMENTS_PER_WORKER", 64)
	asnMMDB := cfg.String("GEOIP_ASN_MMDB", "")
	store, storeErr := artifact.StoreFromConfig(cfg)
	if err := cfg.Err(); err != nil {
		log.Error("bad configuration", "error", err)
		os.Exit(1)
	}
	cfg.Dump(log)
	if devToken != "" {
		log.Warn("dev auto-enrollment is enabled; never use this in production")
	}

	pool, err := db.WaitAndConnect(ctx, dsn, dbWait)
	if err != nil {
		log.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if storeErr != nil {
		log.Error("artifact store misconfigured", "error", storeErr)
		os.Exit(1)
	}
	if store == nil {
		log.Error("artifact store required: set CNIP_ARTIFACT_S3_ENDPOINT or CNIP_ARTIFACT_DIR")
		os.Exit(1)
	}

	ccfg := coordinator.Config{
		AdminToken:              adminToken,
		DevEnrollmentToken:      devToken,
		MaxAssignmentsPerWorker: maxPerWorker,
		Audit:                   &audit.Logger{Pool: pool, Log: log},
	}
	if asnMMDB != "" {
		resolver, err := geo.OpenASN(asnMMDB)
		if err != nil {
			log.Error("GeoLite2-ASN database unusable", "error", err)
			os.Exit(1)
		}
		defer resolver.Close()
		ccfg.ResolveASN = resolver.Lookup
	}
	api := coordinator.New(ccfg, &registry.Store{Pool: pool}, store, log)
	api.StartMaintenance(ctx)

	sched := &scheduler.Scheduler{Pool: pool,
		Cfg: scheduler.Config{Redundancy: redundancy}, Log: log}
	go sched.Run(ctx, schedInterval)

	srv := httpserver.New(addr, log)
	srv.AddReadyCheck("postgres", pool.Ping)
	srv.Handle("/api/v1/", api.Handler())
	srv.Handle("/admin/v1/", api.Handler())
	if err := srv.Run(ctx); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

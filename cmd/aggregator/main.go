// Command aggregator computes trust-weighted consensus and publishes results.
// Phase 1 ships the service shell; the aggregation pipeline arrives in Phase 8.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/db"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/aggregate"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/httpserver"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/logging"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/publisher"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/trust"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(httpserver.SelfCheck(os.Getenv("VAPN_HTTP_ADDR"), ":8082"))
	}
	log := logging.New("aggregator")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	addr := cfg.String("HTTP_ADDR", ":8082")
	dsn := cfg.Require("DB_DSN")
	dbWait := cfg.Duration("DB_WAIT", 60*time.Second)
	cfg2 := cfg.Duration("TRUST_INTERVAL", time.Minute)
	windowSeconds := cfg.Int("WINDOW_SECONDS", 300)
	minWorkers := cfg.Int("MIN_WORKERS", 3)
	advisorURL := cfg.String("ADVISOR_URL", "")
	advisorToken := cfg.String("ADVISOR_TOKEN", "")
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

	scorer := &trust.Scorer{Pool: pool, Log: log}
	go scorer.Run(ctx, cfg2)

	engine := &aggregate.Engine{Pool: pool, Cfg: aggregate.Config{
		WindowSeconds: windowSeconds,
		MinWorkers:    minWorkers,
	}, Log: log}
	go engine.Run(ctx)

	if advisorURL != "" {
		pub := &publisher.Publisher{Pool: pool,
			Client: advisor.New(advisorURL, advisorToken), Log: log}
		go pub.Run(ctx, 15*time.Second, 5*time.Minute)
	} else {
		log.Warn("no VPS Advisor endpoint configured; publication outbox will accumulate")
	}

	srv := httpserver.New(addr, log)
	srv.AddReadyCheck("postgres", pool.Ping)
	if err := srv.Run(ctx); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

// Command builder runs one snapshot build: provider sync from VPS Advisor,
// RIS bview extraction, validation, GeoIP enrichment, PostgreSQL load, probe
// target derivation, sanity gate, publish. Designed to run as a scheduled
// one-shot job. Exit codes: 0 published, 2 held by sanity gate, 1 error.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/builder"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/db"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/logging"
)

func main() {
	log := logging.New("builder")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	dsn := cfg.Require("DB_DSN")
	advisorURL := cfg.String("ADVISOR_URL", "http://localhost:8081")
	advisorToken := cfg.Require("ADVISOR_TOKEN")
	dbWait := cfg.Duration("DB_WAIT", 60*time.Second)
	bcfg := builder.Config{
		BviewPath:             cfg.String("RIS_BVIEW_PATH", "data/ripe/latest-bview.gz"),
		CityMMDB:              cfg.String("GEOIP_CITY_MMDB", ""),
		MaxTargetsPerProvider: cfg.Int("MAX_TARGETS_PER_PROVIDER", 100),
		SanityMaxDelta:        float64(cfg.Int("SANITY_MAX_DELTA_PCT", 50)) / 100,
		SanityForce:           cfg.Bool("SANITY_FORCE", false),
		RetainSnapshots:       cfg.Int("RETAIN_SNAPSHOTS", 5),
	}
	// Artifact store: any S3-compatible provider (B2 in production, minio in
	// dev) or a local directory; unset means build-only, no distribution.
	store, storeErr := artifact.StoreFromConfig(cfg)
	signingKeyB64 := cfg.String("SNAPSHOT_SIGNING_KEY", "")
	minWorkerVersion := cfg.String("MIN_WORKER_VERSION", "0.1.0")
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

	if storeErr != nil {
		log.Error("artifact store misconfigured", "error", storeErr)
		os.Exit(1)
	}
	var pub *artifact.Publisher
	if store != nil {
		key, err := artifact.ParseSigningKey(signingKeyB64)
		if err != nil {
			log.Error("artifact store configured but VAPN_SNAPSHOT_SIGNING_KEY unusable", "error", err)
			os.Exit(1)
		}
		pub = &artifact.Publisher{Pool: pool, Store: store, Key: key,
			MinWorkerVersion: minWorkerVersion, Log: log}
		log.Info("artifact publication enabled", "public_key", artifact.PublicKeyBase64(key))
	}

	b := builder.New(bcfg, pool, advisor.New(advisorURL, advisorToken), pub, log)
	if err := b.Run(ctx); err != nil {
		if errors.Is(err, builder.ErrSanityGate) {
			log.Error("snapshot held for review", "error", err)
			os.Exit(2)
		}
		log.Error("build failed", "error", err)
		os.Exit(1)
	}
}

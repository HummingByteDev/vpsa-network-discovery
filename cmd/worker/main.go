// Command worker is the community-run measurement agent. First boot needs
// only CNIP_ENROLLMENT_TOKEN and CNIP_COORDINATOR_URL (plus the pinned
// CNIP_SNAPSHOT_PUBLIC_KEY baked into distributed images); everything else is
// automatic. `worker doctor` prints a self-diagnosis. Probing lands in
// Phase 5.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/config"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/logging"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

func main() {
	log := logging.New("worker")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	acfg := worker.AgentConfig{
		CoordinatorURL:    cfg.Require("COORDINATOR_URL"),
		EnrollmentToken:   cfg.String("ENROLLMENT_TOKEN", ""),
		Name:              cfg.String("WORKER_NAME", ""),
		StateDir:          cfg.String("STATE_DIR", "/state"),
		HeartbeatInterval: cfg.Duration("HEARTBEAT_INTERVAL", 30*time.Second),
	}
	pubB64 := cfg.Require("SNAPSHOT_PUBLIC_KEY")
	if err := cfg.Err(); err != nil {
		log.Error("bad configuration", "error", err)
		os.Exit(1)
	}
	cfg.Dump(log)

	pub, err := artifact.ParsePublicKey(pubB64)
	if err != nil {
		log.Error("bad snapshot public key", "error", err)
		os.Exit(1)
	}
	acfg.SnapshotPubKey = pub

	agent, err := worker.NewAgent(acfg, log)
	if err != nil {
		log.Error("agent init failed", "error", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		if err := agent.Doctor(ctx); err != nil {
			os.Exit(1)
		}
		return
	}

	if err := agent.Run(ctx); err != nil {
		log.Error("agent failed", "error", err)
		os.Exit(1)
	}
}

// Command worker is the community-run measurement agent.
// Phase 1 ships the entrypoint and configuration surface only; enrollment and
// the agent loop arrive in Phase 4, the probe engine in Phase 5.
package main

import (
	"os"

	"github.com/vpsadvisor/ip-discovery/internal/platform/config"
	"github.com/vpsadvisor/ip-discovery/internal/platform/logging"
	"github.com/vpsadvisor/ip-discovery/internal/platform/version"
)

func main() {
	log := logging.New("worker")
	cfg := config.New()
	_ = cfg.String("ENROLLMENT_TOKEN", "")
	_ = cfg.String("COORDINATOR_URL", "")
	_ = cfg.String("STATE_DIR", "/state")
	_ = cfg.String("MAXMIND_LICENSE_KEY", "") // optional; operator's own key
	cfg.Dump(log)
	log.Info("worker agent arrives in Phase 4", "version", version.Version)
	os.Exit(0)
}

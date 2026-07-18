// Command mockadvisor serves the fixture-driven VPS Advisor stub used for
// local development and contract tests (see internal/mockadvisor).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/vpsadvisor/ip-discovery/internal/mockadvisor"
	"github.com/vpsadvisor/ip-discovery/internal/platform/config"
	"github.com/vpsadvisor/ip-discovery/internal/platform/httpserver"
	"github.com/vpsadvisor/ip-discovery/internal/platform/logging"
)

func main() {
	log := logging.New("mockadvisor")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.New()
	addr := cfg.String("HTTP_ADDR", ":8081")
	token := cfg.String("ADVISOR_TOKEN", "dev-advisor-token")
	fixturesPath := cfg.String("MOCK_FIXTURES", "")
	if err := cfg.Err(); err != nil {
		log.Error("bad configuration", "error", err)
		os.Exit(1)
	}
	cfg.Dump(log)

	var raw []byte
	if fixturesPath != "" {
		b, err := os.ReadFile(fixturesPath)
		if err != nil {
			log.Error("read fixtures", "error", err)
			os.Exit(1)
		}
		raw = b
	}
	fixtures, err := mockadvisor.LoadFixtures(raw)
	if err != nil {
		log.Error("load fixtures", "error", err)
		os.Exit(1)
	}
	log.Info("fixtures loaded", "providers", len(fixtures.Providers))

	srv := httpserver.New(addr, log)
	srv.Handle("/api/", mockadvisor.NewServer(fixtures, token, log))
	if err := srv.Run(ctx); err != nil {
		log.Error("server error", "error", err)
		os.Exit(1)
	}
}

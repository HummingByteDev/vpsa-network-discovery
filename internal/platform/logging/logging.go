// Package logging provides the platform-wide structured logger.
//
// All services log JSON to stdout. Level comes from CNIP_LOG_LEVEL
// (debug|info|warn|error, default info).
package logging

import (
	"log/slog"
	"os"
	"strings"

	"github.com/vpsadvisor/ip-discovery/internal/platform/version"
)

func New(service string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("CNIP_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(h).With(
		slog.String("service", service),
		slog.String("version", version.Version),
	)
}

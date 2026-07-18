// Package version exposes build-time identity, injected via -ldflags.
package version

var (
	Version = "dev"
	Commit  = "unknown"
)

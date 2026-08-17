// Package prod holds no code — only the production deployment files. This test
// guards the one property of them that is easy to break by hand and expensive
// to discover in production: the edge ports must stay configurable.
//
// A hard-coded "80:80" makes the stack impossible to install on a VM that
// already serves something on port 80 — `docker compose up -d` fails with
// "failed to bind host port 0.0.0.0:80/tcp: address already in use" and the
// operator has no way out short of editing the shipped compose file.
package prod

import (
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEdgePortsAreConfigurableWithStandardDefaults(t *testing.T) {
	compose := read(t, "docker-compose.yml")
	for _, want := range []string{
		`"${VAPN_CADDY_HTTP_PORT:-80}:${VAPN_CADDY_HTTP_PORT:-80}"`,
		`"${VAPN_CADDY_HTTPS_PORT:-443}:${VAPN_CADDY_HTTPS_PORT:-443}"`,
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml does not publish %s", want)
		}
	}
	for _, forbidden := range []string{`"80:80"`, `"443:443"`} {
		if strings.Contains(compose, forbidden) {
			t.Errorf("docker-compose.yml hard-codes %s; the port must stay overridable", forbidden)
		}
	}

	// Caddy is told the real ports rather than being port-mapped behind its
	// back, so its redirects and ACME challenge selection match reality.
	caddyfile := read(t, "Caddyfile")
	for _, want := range []string{
		"http_port {$VAPN_CADDY_HTTP_PORT:80}",
		"https_port {$VAPN_CADDY_HTTPS_PORT:443}",
	} {
		if !strings.Contains(caddyfile, want) {
			t.Errorf("Caddyfile is missing %q", want)
		}
	}
	if !strings.Contains(compose, "VAPN_CADDY_HTTP_PORT: ${VAPN_CADDY_HTTP_PORT:-80}") {
		t.Error("the caddy service does not receive VAPN_CADDY_HTTP_PORT, so the Caddyfile default would silently win")
	}

	// An operator has to be able to find the setting.
	env := read(t, ".env.example")
	for _, want := range []string{"VAPN_CADDY_HTTP_PORT=80", "VAPN_CADDY_HTTPS_PORT=443"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env.example does not document %s", want)
		}
	}
}

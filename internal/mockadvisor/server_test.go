package mockadvisor

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "test-token"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	f, err := LoadFixtures(nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(f, testToken, discardLogger())
}

func get(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRequired(t *testing.T) {
	h := newTestServer(t)
	if rec := get(t, h, "/api/v1/monitoring/providers", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", rec.Code)
	}
	if rec := get(t, h, "/api/v1/monitoring/providers", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", rec.Code)
	}
}

func TestListProvidersEnabledFilter(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/api/v1/monitoring/providers?enabled=true", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var body struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Providers) != 4 {
		t.Fatalf("got %d enabled providers, want 4", len(body.Providers))
	}
	for _, p := range body.Providers {
		if !p.MonitoringEnabled {
			t.Fatalf("provider %s is disabled but was returned with enabled=true", p.Name)
		}
	}
}

func TestUpdatedSinceFilter(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/api/v1/monitoring/providers?updated_since=2026-07-04T00:00:00Z", testToken)
	var body struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Providers) != 3 {
		t.Fatalf("got %d providers updated since 2026-07-04, want 3", len(body.Providers))
	}
}

func TestGetProvider(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/api/v1/monitoring/providers/0d4b1f3a-9c1e-4f7a-8b2d-111111111111", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var p Provider
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "Hetzner Online" || p.ASNs[0] != 24940 {
		t.Fatalf("unexpected provider: %+v", p)
	}
	if rec := get(t, h, "/api/v1/monitoring/providers/nope", testToken); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404", rec.Code)
	}
}

func TestListASNs(t *testing.T) {
	h := newTestServer(t)
	rec := get(t, h, "/api/v1/monitoring/asns", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "24940") {
		t.Fatalf("ASN mapping missing 24940: %s", rec.Body.String())
	}
}

func TestResultsIngestion(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/monitoring/results/providers/0d4b1f3a-9c1e-4f7a-8b2d-111111111111",
		strings.NewReader(`{"as_of":"2026-07-18T08:05:00Z","global":{"verdict":"healthy"}}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}
}

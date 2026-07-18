// Package mockadvisor is a fixture-driven stand-in for the VPS Advisor
// website's monitoring endpoints (contract A1–A4 in docs/architecture/04).
// It exists so every platform component can be developed and contract-tested
// without the real website (risk R4). It implements the contract faithfully
// but holds no state beyond its fixtures.
package mockadvisor

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

//go:embed fixtures/providers.json
var defaultFixtures []byte

type Provider struct {
	ProviderID        string    `json:"provider_id"`
	Name              string    `json:"name"`
	ASNs              []int64   `json:"asns"`
	MonitoringEnabled bool      `json:"monitoring_enabled"`
	Priority          int       `json:"priority"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Fixtures struct {
	Providers []Provider `json:"providers"`
}

func LoadFixtures(raw []byte) (*Fixtures, error) {
	if raw == nil {
		raw = defaultFixtures
	}
	var f Fixtures
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse fixtures: %w", err)
	}
	return &f, nil
}

type Server struct {
	fixtures *Fixtures
	token    string
	log      *slog.Logger
}

func NewServer(f *Fixtures, token string, log *slog.Logger) http.Handler {
	s := &Server{fixtures: f, token: token, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/monitoring/providers", s.listProviders)
	mux.HandleFunc("GET /api/v1/monitoring/providers/{id}", s.getProvider)
	mux.HandleFunc("GET /api/v1/monitoring/asns", s.listASNs)
	mux.HandleFunc("GET /api/v1/monitoring/enrollments/pending", s.emptyList("enrollments"))
	mux.HandleFunc("POST /api/v1/monitoring/enrollments/{id}/registered", s.accept(http.StatusNoContent))
	mux.HandleFunc("GET /api/v1/monitoring/admin/decisions", s.emptyList("decisions"))
	mux.HandleFunc("PUT /api/v1/monitoring/results/providers/{id}", s.acceptResults)
	mux.HandleFunc("POST /api/v1/monitoring/results/anomalies", s.accept(http.StatusAccepted))
	mux.HandleFunc("POST /api/v1/monitoring/results/history", s.accept(http.StatusAccepted))
	mux.HandleFunc("POST /api/v1/monitoring/telemetry/fleet", s.accept(http.StatusAccepted))
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	want := "Bearer " + s.token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			problem(w, http.StatusUnauthorized, "invalid or missing service credential")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": status, "title": http.StatusText(status), "detail": detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	onlyEnabled := q.Get("enabled") == "true"
	var since time.Time
	if v := q.Get("updated_since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			problem(w, http.StatusBadRequest, "updated_since must be RFC 3339")
			return
		}
		since = t
	}
	out := make([]Provider, 0, len(s.fixtures.Providers))
	for _, p := range s.fixtures.Providers {
		if onlyEnabled && !p.MonitoringEnabled {
			continue
		}
		if !since.IsZero() && !p.UpdatedAt.After(since) {
			continue
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "next_cursor": nil})
}

func (s *Server) getProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, p := range s.fixtures.Providers {
		if p.ProviderID == id {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	problem(w, http.StatusNotFound, "unknown provider")
}

func (s *Server) listASNs(w http.ResponseWriter, r *http.Request) {
	type row struct {
		ASN        int64     `json:"asn"`
		ProviderID string    `json:"provider_id"`
		Enabled    bool      `json:"monitoring_enabled"`
		UpdatedAt  time.Time `json:"updated_at"`
	}
	out := []row{}
	for _, p := range s.fixtures.Providers {
		for _, asn := range p.ASNs {
			out = append(out, row{ASN: asn, ProviderID: p.ProviderID,
				Enabled: p.MonitoringEnabled, UpdatedAt: p.UpdatedAt})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"asns": out, "next_cursor": nil})
}

func (s *Server) emptyList(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{key: []any{}, "next_cursor": nil})
	}
}

func (s *Server) accept(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.log.Info("accepted", "method", r.Method, "path", r.URL.Path)
		w.WriteHeader(status)
	}
}

func (s *Server) acceptResults(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	s.log.Info("received provider status", "provider_id", r.PathValue("id"))
	w.WriteHeader(http.StatusAccepted)
}

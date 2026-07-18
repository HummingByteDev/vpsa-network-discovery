// Package coordinator implements the worker-facing data-plane API (contract B
// in docs/architecture/04-api-contracts.md) and the platform admin surface
// (contract C). Phase 4 scope: registration, heartbeat, artifact
// advertisement/download, admin worker management. Assignments and
// observations land in Phases 5–7; replay tracking in Phase 6.
package coordinator

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/audit"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/registry"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/wireauth"
)

type Config struct {
	AdminToken string
	// Audit, when set, receives security- and admin-relevant events.
	Audit *audit.Logger
	// DevEnrollmentToken, when set, lets a worker register with this shared
	// token: a worker record is auto-created and auto-approved. Development
	// convenience only — never set in production.
	DevEnrollmentToken string
	SnapshotPollTTL    time.Duration // how long clients may cache the manifest
}

type Server struct {
	cfg      Config
	reg      *registry.Store
	store    artifact.Store
	audit    *audit.Logger
	log      *slog.Logger
	handler  http.Handler
	manifest struct {
		sync.Mutex
		cached  *artifact.Manifest
		fetched time.Time
	}
}

func New(cfg Config, reg *registry.Store, store artifact.Store, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, reg: reg, store: store, audit: cfg.Audit, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/workers/register", s.register)
	mux.Handle("POST /api/v1/workers/heartbeat", s.signed(s.heartbeat))
	mux.Handle("GET /api/v1/workers/me", s.signed(s.me))
	mux.Handle("GET /api/v1/artifacts/routing/current", s.signed(s.currentManifest))
	mux.Handle("GET /api/v1/artifacts/routing/current/download", s.signed(s.downloadArtifact))
	mux.Handle("POST /api/v1/assignments/lease", s.signed(s.leaseAssignments))
	mux.Handle("POST /api/v1/assignments/release", s.signed(s.releaseAssignments))
	mux.Handle("POST /api/v1/observations", s.signed(s.uploadObservations))
	mux.Handle("POST /api/v1/workers/keys/rotate", s.signed(s.rotateKey))

	mux.Handle("POST /admin/v1/workers", s.admin(s.adminCreateWorker))
	mux.Handle("GET /admin/v1/workers", s.admin(s.adminListWorkers))
	mux.Handle("POST /admin/v1/workers/{id}/state", s.admin(s.adminSetState))
	mux.Handle("POST /admin/v1/workers/{id}/rotate-key", s.admin(s.adminRequestRotation))
	s.handler = mux
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

// StartMaintenance runs background upkeep until ctx is canceled: pruning the
// replay-nonce window (2× the wireauth skew, so a nonce outlives any
// timestamp still accepted).
func (s *Server) StartMaintenance(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := s.reg.PruneNonces(ctx, 2*wireauth.MaxSkew); err == nil && n > 0 {
					s.log.Debug("pruned replay nonces", "count", n)
				}
			}
		}
	}()
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

// --- auth middleware ---

type workerCtxKey struct{}

type workerIdentity struct {
	ID    string
	State string
}

// signed verifies the wireauth headers against the worker's registered key
// and enforces lifecycle state: suspended/retired workers get 403.
func (s *Server) signed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerID := r.Header.Get(wireauth.HeaderWorkerID)
		if workerID == "" {
			problem(w, http.StatusUnauthorized, "missing worker identity")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			problem(w, http.StatusBadRequest, "unreadable body")
			return
		}
		keys, state, err := s.reg.ActiveKeys(r.Context(), workerID)
		if err != nil {
			problem(w, http.StatusUnauthorized, "unknown worker or no active key")
			return
		}
		var nonce string
		verifyErr := fmt.Errorf("no keys")
		for _, pub := range keys {
			if nonce, verifyErr = wireauth.Verify(r.Method, r.URL.Path, r.Header, body, pub, time.Now()); verifyErr == nil {
				break
			}
		}
		if verifyErr != nil {
			s.log.Warn("signature rejected", "worker", workerID, "error", verifyErr)
			s.reg.RecordTrustEvent(r.Context(), workerID, "bad_signature", "system")
			problem(w, http.StatusUnauthorized, "signature verification failed")
			return
		}
		if replayed, err := s.reg.SeenNonce(r.Context(), workerID, nonce); err != nil {
			problem(w, http.StatusInternalServerError, "auth check failed")
			return
		} else if replayed {
			s.log.Warn("replayed nonce rejected", "worker", workerID)
			s.reg.RecordTrustEvent(r.Context(), workerID, "replay", "system")
			problem(w, http.StatusConflict, "nonce replay detected")
			return
		}
		switch state {
		case "suspended", "retired":
			problem(w, http.StatusForbidden, "worker is "+state)
			return
		}
		ctx := context.WithValue(r.Context(), workerCtxKey{}, workerIdentity{ID: workerID, State: state})
		r2 := r.Clone(ctx)
		r2.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r2)
	})
}

func identity(r *http.Request) workerIdentity {
	id, _ := r.Context().Value(workerCtxKey{}).(workerIdentity)
	return id
}

func (s *Server) admin(next http.HandlerFunc) http.Handler {
	want := "Bearer " + s.cfg.AdminToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminToken == "" ||
			subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
			problem(w, http.StatusUnauthorized, "invalid admin credential")
			return
		}
		next(w, r)
	})
}

// --- worker endpoints ---

type registerRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	PublicKey       string `json:"public_key"` // base64, 32 bytes
	Name            string `json:"name"`
	SoftwareVersion string `json:"software_version"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pubRaw, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(pubRaw) != ed25519.PublicKeySize {
		problem(w, http.StatusUnprocessableEntity, "public_key must be base64 of 32 bytes")
		return
	}
	pub := ed25519.PublicKey(pubRaw)

	if s.cfg.DevEnrollmentToken != "" && req.EnrollmentToken == s.cfg.DevEnrollmentToken {
		s.devRegister(w, r, req, pub)
		return
	}

	workerID, state, err := s.reg.Register(r.Context(), req.EnrollmentToken, pub, req.SoftwareVersion)
	if errors.Is(err, registry.ErrBadToken) {
		problem(w, http.StatusUnauthorized, "enrollment token invalid, used, or expired")
		return
	}
	if err != nil {
		s.log.Error("register failed", "error", err)
		problem(w, http.StatusInternalServerError, "registration failed")
		return
	}
	s.log.Info("worker registered", "worker", workerID)
	if s.audit != nil {
		s.audit.Event(r.Context(), "auth", "worker:"+workerID, "registered", workerID, nil)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"worker_id": workerID, "state": state})
}

// devRegister auto-creates and auto-approves a worker (shared dev token flow).
func (s *Server) devRegister(w http.ResponseWriter, r *http.Request, req registerRequest, pub ed25519.PublicKey) {
	name := req.Name
	if name == "" {
		name = "dev-worker"
	}
	workerID, token, err := s.reg.CreateWorker(r.Context(), registry.DevOperatorID, name, time.Minute)
	if err == nil {
		_, _, err = s.reg.Register(r.Context(), token, pub, req.SoftwareVersion)
	}
	if err == nil {
		err = s.reg.SetState(r.Context(), workerID, "active", "dev auto-enrollment", "system:dev")
	}
	if err != nil {
		s.log.Error("dev registration failed", "error", err)
		problem(w, http.StatusInternalServerError, "registration failed")
		return
	}
	s.log.Info("dev worker auto-enrolled", "worker", workerID, "name", name)
	writeJSON(w, http.StatusCreated, map[string]string{"worker_id": workerID, "state": "active"})
}

type heartbeatRequest struct {
	SoftwareVersion string `json:"software_version"`
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	wk, err := s.reg.Heartbeat(r.Context(), identity(r).ID, req.SoftwareVersion)
	if err != nil {
		problem(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}
	actions := []string{}
	var cfg map[string]any
	if err := json.Unmarshal(wk.Config, &cfg); err == nil {
		if v, ok := cfg["rotate_requested"].(bool); ok && v {
			actions = append(actions, "rotate_key")
		}
	}
	resp := map[string]any{
		"state":   wk.State,
		"config":  json.RawMessage(wk.Config),
		"actions": actions,
	}
	if m := s.currentManifestCached(r.Context()); m != nil && wk.State == "active" {
		resp["snapshot"] = map[string]any{"version": m.Version}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	writeJSON(w, http.StatusOK, map[string]string{"worker_id": id.ID, "state": id.State})
}

// currentManifestCached fetches the store's current manifest with a small TTL
// cache so heartbeats don't hammer the object store.
func (s *Server) currentManifestCached(ctx context.Context) *artifact.Manifest {
	ttl := s.cfg.SnapshotPollTTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	s.manifest.Lock()
	defer s.manifest.Unlock()
	if s.manifest.cached != nil && time.Since(s.manifest.fetched) < ttl {
		return s.manifest.cached
	}
	rc, err := s.store.Get(ctx, artifact.PointerKey)
	if err != nil {
		return s.manifest.cached
	}
	defer rc.Close()
	var ptr artifact.Pointer
	if err := json.NewDecoder(rc).Decode(&ptr); err != nil {
		return s.manifest.cached
	}
	mrc, err := s.store.Get(ctx, ptr.ManifestKey)
	if err != nil {
		return s.manifest.cached
	}
	defer mrc.Close()
	var m artifact.Manifest
	if err := json.NewDecoder(mrc).Decode(&m); err != nil {
		return s.manifest.cached
	}
	s.manifest.cached = &m
	s.manifest.fetched = time.Now()
	return &m
}

func (s *Server) currentManifest(w http.ResponseWriter, r *http.Request) {
	m := s.currentManifestCached(r.Context())
	if m == nil {
		problem(w, http.StatusNotFound, "no snapshot published yet")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manifest":      m,
		"download_path": "/api/v1/artifacts/routing/current/download",
	})
}

func (s *Server) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	m := s.currentManifestCached(r.Context())
	if m == nil {
		problem(w, http.StatusNotFound, "no snapshot published yet")
		return
	}
	rc, err := s.store.Get(r.Context(), m.ObjectKey)
	if err != nil {
		problem(w, http.StatusBadGateway, "artifact store unavailable")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Length", fmt.Sprint(m.SizeBytes))
	_, _ = io.Copy(w, rc)
}

// --- admin endpoints ---

func (s *Server) adminCreateWorker(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		OperatorID string `json:"operator_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Name == "" {
		problem(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.OperatorID == "" {
		req.OperatorID = registry.DevOperatorID
	}
	workerID, token, err := s.reg.CreateWorker(r.Context(), req.OperatorID, req.Name, 24*time.Hour)
	if err != nil {
		s.log.Error("create worker failed", "error", err)
		problem(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"worker_id": workerID, "enrollment_token": token,
	})
}

func (s *Server) adminListWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.reg.List(r.Context())
	if err != nil {
		problem(w, http.StatusInternalServerError, "list failed")
		return
	}
	type row struct {
		ID              string     `json:"worker_id"`
		Name            string     `json:"name"`
		State           string     `json:"state"`
		StateReason     string     `json:"state_reason,omitempty"`
		SoftwareVersion string     `json:"software_version,omitempty"`
		LastHeartbeat   *time.Time `json:"last_heartbeat_at,omitempty"`
	}
	out := make([]row, 0, len(workers))
	for _, wk := range workers {
		out = append(out, row{wk.ID, wk.Name, wk.State, wk.StateReason,
			wk.SoftwareVersion, wk.LastHeartbeat})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": out})
}

func (s *Server) adminSetState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	err := s.reg.SetState(r.Context(), r.PathValue("id"), req.State, req.Reason, "admin")
	if err == nil && s.audit != nil {
		s.audit.Event(r.Context(), "admin", "admin", "worker_state:"+req.State,
			r.PathValue("id"), map[string]string{"reason": req.Reason})
	}
	switch {
	case errors.Is(err, registry.ErrUnknownWorker):
		problem(w, http.StatusNotFound, "unknown worker")
	case errors.Is(err, registry.ErrBadTransition):
		problem(w, http.StatusConflict, err.Error())
	case err != nil:
		problem(w, http.StatusInternalServerError, "transition failed")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

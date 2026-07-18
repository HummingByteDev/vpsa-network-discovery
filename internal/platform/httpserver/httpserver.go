// Package httpserver is the shared HTTP scaffolding: /healthz, /readyz with
// pluggable checks, /metrics (Prometheus), request logging, graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/metrics"
)

// SelfCheck probes this process's own /healthz — the container healthcheck
// entrypoint for distroless images, which have no shell or curl. Returns a
// process exit code (0 healthy, 1 not).
func SelfCheck(addr, fallback string) int {
	if addr == "" {
		addr = fallback
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1" + addr + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

type ReadyCheck func(ctx context.Context) error

type Server struct {
	addr   string
	log    *slog.Logger
	mux    *http.ServeMux
	mu     sync.RWMutex
	checks map[string]ReadyCheck
}

func New(addr string, log *slog.Logger) *Server {
	s := &Server{
		addr:   addr,
		log:    log,
		mux:    http.NewServeMux(),
		checks: map[string]ReadyCheck{},
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("GET /readyz", s.readyz)
	s.mux.Handle("GET /metrics", promhttp.Handler())
	return s
}

// Handle mounts a handler using net/http ServeMux patterns ("GET /path/{id}").
func (s *Server) Handle(pattern string, h http.Handler) {
	s.mux.Handle(pattern, s.logged(h))
}

func (s *Server) AddReadyCheck(name string, c ReadyCheck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[name] = c
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, check := range s.checks {
		if err := check(ctx); err != nil {
			s.log.Warn("readiness check failed", "check", name, "error", err)
			http.Error(w, fmt.Sprintf("%s: %v", name, err), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) logged(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		// r.Pattern is set by the innermost ServeMux that matched, keeping
		// metric cardinality bounded to registered routes.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		metrics.HTTPRequests.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
		metrics.HTTPDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		s.log.Debug("request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"remote", r.RemoteAddr, "duration_ms", time.Since(start).Milliseconds())
	})
}

// Run serves until ctx is canceled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", s.addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.log.Info("http server shutting down")
		return srv.Shutdown(shutCtx)
	}
}

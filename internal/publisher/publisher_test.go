package publisher

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/migrate"
)

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("VAPN_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("VAPN_TEST_DB_DSN not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir, _ := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err := migrate.Apply(context.Background(), pool, dir, log); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`truncate aggregation.publication_outbox`); err != nil {
		t.Fatal(err)
	}
	return pool
}

// TestDrainAckAndBackoff: a failing advisor backs the row off; a healthy one
// acks it. At-least-once, never lost.
func TestDrainAckAndBackoff(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()

	var healthy atomic.Bool
	var received atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		received.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	if _, err := pool.Exec(ctx, `insert into aggregation.publication_outbox (kind, payload)
		values ('provider_status', '{"provider_id":"7f9c0000-0000-0000-0000-000000000001","global":{"verdict":"healthy"}}')`); err != nil {
		t.Fatal(err)
	}
	p := &Publisher{Pool: pool, Client: advisor.New(srv.URL, "t"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// Advisor down: row survives with backoff.
	if _, err := p.DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var acked bool
	if err := pool.QueryRow(ctx, `select attempts, acked_at is not null
		from aggregation.publication_outbox`).Scan(&attempts, &acked); err != nil {
		t.Fatal(err)
	}
	if acked || attempts != 1 {
		t.Fatalf("after failure: acked=%v attempts=%d, want false/1", acked, attempts)
	}

	// Not due yet: drain pushes nothing.
	if n, err := p.DrainOnce(ctx); err != nil || n != 0 {
		t.Fatalf("backed-off row was retried early (n=%d, err=%v)", n, err)
	}

	// Advisor back: force due, drain acks.
	healthy.Store(true)
	if _, err := pool.Exec(ctx,
		`update aggregation.publication_outbox set next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	if n, err := p.DrainOnce(ctx); err != nil || n != 1 {
		t.Fatalf("drain after recovery: n=%d err=%v, want 1/nil", n, err)
	}
	if received.Load() != 1 {
		t.Fatalf("advisor received %d documents, want 1", received.Load())
	}
	if err := pool.QueryRow(ctx, `select acked_at is not null
		from aggregation.publication_outbox`).Scan(&acked); err != nil {
		t.Fatal(err)
	}
	if !acked {
		t.Fatal("row not acked after successful push")
	}
}

func TestFleetTelemetryEnqueue(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	p := &Publisher{Pool: pool, Client: advisor.New("http://unused", "t"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := p.EnqueueFleetTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	var kind string
	var payload []byte
	if err := pool.QueryRow(ctx, `select kind, payload from aggregation.publication_outbox
		order by id desc limit 1`).Scan(&kind, &payload); err != nil {
		t.Fatal(err)
	}
	if kind != "telemetry" || len(payload) == 0 {
		t.Fatalf("telemetry row: kind=%s len=%d", kind, len(payload))
	}
}

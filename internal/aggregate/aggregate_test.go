package aggregate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/migrate"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/trust"
)

// Replay-based tests: recorded observation sets with injected liars and
// outages must produce the right verdicts and demote the liars' trust.
// Gated on CNIP_TEST_DB_DSN; truncates measurement/aggregation/registry data.

const provider = "aaaaaaaa-0000-0000-0000-000000000001"

var windowStart = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CNIP_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("CNIP_TEST_DB_DSN not set; skipping DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(context.Background(), pool, dir, discard()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, stmt := range []string{
		"truncate measurements.observation, measurements.upload_batch",
		"truncate aggregation.consensus_window, aggregation.provider_status, aggregation.anomaly, aggregation.publication_outbox, aggregation.worker_agreement",
		"truncate registry.worker cascade",
		"truncate routing.provider cascade",
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into routing.provider
		(provider_id, name, monitoring_enabled, priority, synced_at)
		values ($1, 'AggTest', true, 10, now())`, provider); err != nil {
		t.Fatal(err)
	}
	return pool
}

// seedWorkers creates n active workers with fresh heartbeats and long tenure.
func seedWorkers(t *testing.T, pool *pgxpool.Pool, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = uuid.NewString()
		if _, err := pool.Exec(ctx, `insert into registry.worker
			(id, operator_id, name, state, approved_at, last_heartbeat_at)
			values ($1, $2, $3, 'active', now() - interval '60 days', now())`,
			ids[i], uuid.NewString(), fmt.Sprintf("agg-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// observe inserts count observations for a worker/target inside the window.
func observe(t *testing.T, pool *pgxpool.Pool, workerID, target string, ok bool, rtt float64, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		var rttArg any
		if ok {
			rttArg = rtt
		}
		if _, err := pool.Exec(ctx, `insert into measurements.observation
			(worker_id, assignment_id, provider_id, target, probe_type, measured_at,
			 ok, rtt_ms, packets_sent, packets_lost, signature)
			values ($1, 1, $2, $3, 'icmp', $4, $5, $6, 4, $7, 'sig')`,
			workerID, provider, target,
			windowStart.Add(time.Duration(i*10)*time.Second), ok, rttArg,
			map[bool]int{true: 0, false: 4}[ok]); err != nil {
			t.Fatal(err)
		}
	}
}

func engine(pool *pgxpool.Pool, minWorkers int) *Engine {
	return &Engine{Pool: pool, Cfg: Config{WindowSeconds: 300, MinWorkers: minWorkers}, Log: discard()}
}

// TestConsensusOutvotesLiar: three honest workers see the target up, one liar
// reports it down. Verdict stays healthy; the liar's agreement collapses and
// its trust drops below the honest workers'.
func TestConsensusOutvotesLiar(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	workers := seedWorkers(t, pool, 4)
	honest, liar := workers[:3], workers[3]

	for _, w := range honest {
		observe(t, pool, w, "203.0.113.10", true, 20, 6)
	}
	observe(t, pool, liar, "203.0.113.10", false, 0, 6)

	e := engine(pool, 3)
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}

	var verdict string
	var confidence float64
	if err := pool.QueryRow(ctx, `select verdict, confidence from aggregation.provider_status
		where provider_id = $1`, provider).Scan(&verdict, &confidence); err != nil {
		t.Fatal(err)
	}
	if verdict != "healthy" {
		t.Fatalf("verdict = %s, want healthy despite the liar", verdict)
	}
	if confidence <= 0 {
		t.Fatalf("confidence = %v, want > 0", confidence)
	}

	var liarAgreement, honestAgreement float64
	if err := pool.QueryRow(ctx, `select agreement from aggregation.worker_agreement
		where worker_id = $1`, liar).Scan(&liarAgreement); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select agreement from aggregation.worker_agreement
		where worker_id = $1`, honest[0]).Scan(&honestAgreement); err != nil {
		t.Fatal(err)
	}
	if liarAgreement >= 0.5 || honestAgreement <= 0.9 {
		t.Fatalf("agreement liar=%v honest=%v, want <0.5 and >0.9", liarAgreement, honestAgreement)
	}

	// Trust demotion flows through the scorer. The agreement window is
	// "last 24h"; our test window is in the past relative to wall clock, so
	// shift it forward for the scorer read.
	if _, err := pool.Exec(ctx, `update aggregation.worker_agreement
		set window_start = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	scorer := &trust.Scorer{Pool: pool, Log: discard()}
	if err := scorer.ComputeAll(ctx); err != nil {
		t.Fatal(err)
	}
	var liarScore, honestScore float64
	if err := pool.QueryRow(ctx, `select score from registry.trust_score
		where worker_id = $1`, liar).Scan(&liarScore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select score from registry.trust_score
		where worker_id = $1`, honest[0]).Scan(&honestScore); err != nil {
		t.Fatal(err)
	}
	if liarScore >= honestScore {
		t.Fatalf("liar trust %v not below honest %v", liarScore, honestScore)
	}
}

// TestOutageVerdictAndAnomaly: a previously-responsive target goes dark for
// every worker → outage verdict, anomaly opened; recovery resolves it.
func TestOutageVerdictAndAnomaly(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	workers := seedWorkers(t, pool, 3)

	// Baseline responsiveness in the trailing 24h (before the window).
	for _, w := range workers {
		if _, err := pool.Exec(ctx, `insert into measurements.observation
			(worker_id, assignment_id, provider_id, target, probe_type, measured_at,
			 ok, rtt_ms, packets_sent, packets_lost, signature)
			values ($1, 1, $2, '203.0.113.20', 'icmp', $3, true, 25, 4, 0, 'sig')`,
			w, provider, windowStart.Add(-2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// In the window: everyone sees it down.
	for _, w := range workers {
		observe(t, pool, w, "203.0.113.20", false, 0, 6)
	}

	e := engine(pool, 3)
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}
	var verdict string
	if err := pool.QueryRow(ctx, `select verdict from aggregation.provider_status
		where provider_id = $1`, provider).Scan(&verdict); err != nil {
		t.Fatal(err)
	}
	if verdict != "outage" {
		t.Fatalf("verdict = %s, want outage", verdict)
	}
	var openAnomalies int
	if err := pool.QueryRow(ctx, `select count(*) from aggregation.anomaly
		where provider_id = $1 and kind = 'reachability_loss' and resolved_at is null`,
		provider).Scan(&openAnomalies); err != nil {
		t.Fatal(err)
	}
	if openAnomalies != 1 {
		t.Fatalf("open reachability anomalies = %d, want 1", openAnomalies)
	}

	// Recovery in the next window resolves the anomaly.
	next := windowStart.Add(5 * time.Minute)
	for _, w := range workers {
		for i := 0; i < 6; i++ {
			if _, err := pool.Exec(ctx, `insert into measurements.observation
				(worker_id, assignment_id, provider_id, target, probe_type, measured_at,
				 ok, rtt_ms, packets_sent, packets_lost, signature)
				values ($1, 1, $2, '203.0.113.20', 'icmp', $3, true, 25, 4, 0, 'sig')`,
				w, provider, next.Add(time.Duration(i*10)*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.ComputeWindow(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select verdict from aggregation.provider_status
		where provider_id = $1`, provider).Scan(&verdict); err != nil {
		t.Fatal(err)
	}
	if verdict != "healthy" {
		t.Fatalf("verdict after recovery = %s, want healthy", verdict)
	}
	if err := pool.QueryRow(ctx, `select count(*) from aggregation.anomaly
		where provider_id = $1 and resolved_at is null`, provider).Scan(&openAnomalies); err != nil {
		t.Fatal(err)
	}
	if openAnomalies != 0 {
		t.Fatalf("unresolved anomalies after recovery = %d, want 0", openAnomalies)
	}
	// Publication outbox got status documents.
	var outbox int
	if err := pool.QueryRow(ctx, `select count(*) from aggregation.publication_outbox
		where kind = 'provider_status'`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if outbox == 0 {
		t.Fatal("no provider_status documents queued for publication")
	}
}

// TestInsufficientData: below the worker-diversity minimum the platform
// refuses to guess.
func TestInsufficientData(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	workers := seedWorkers(t, pool, 1)
	observe(t, pool, workers[0], "203.0.113.30", true, 20, 6)

	e := engine(pool, 3)
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}
	var verdict string
	var confidence float64
	if err := pool.QueryRow(ctx, `select verdict, confidence from aggregation.provider_status
		where provider_id = $1`, provider).Scan(&verdict, &confidence); err != nil {
		t.Fatal(err)
	}
	if verdict != "insufficient_data" || confidence != 0 {
		t.Fatalf("verdict=%s confidence=%v, want insufficient_data/0", verdict, confidence)
	}
}

// TestWindowIdempotent: recomputing the same window changes nothing.
func TestWindowIdempotent(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	workers := seedWorkers(t, pool, 3)
	for _, w := range workers {
		observe(t, pool, w, "203.0.113.40", true, 20, 3)
	}
	e := engine(pool, 3)
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	var windows int
	if err := pool.QueryRow(ctx,
		`select count(*) from aggregation.consensus_window`).Scan(&windows); err != nil {
		t.Fatal(err)
	}
	if windows != 1 {
		t.Fatalf("windows = %d, want 1 after recompute", windows)
	}
}

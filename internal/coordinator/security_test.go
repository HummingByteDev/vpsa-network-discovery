package coordinator

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"testing"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/trust"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/wireauth"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

// TestReplayRejected replays a byte-identical signed request: the first
// succeeds, the second dies on the nonce with 409 and a trust event.
func TestReplayRejected(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")

	var workerID string
	if err := e.pool.QueryRow(ctx, `select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"software_version":"replay-test"}`)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/workers/heartbeat",
		bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := wireauth.Sign(req, workerID, agentKey(t, st), body); err != nil {
		t.Fatal(err)
	}

	// First send: fresh nonce, accepted.
	first, err := http.DefaultClient.Do(cloneWithBody(req, body))
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request got %d, want 200", first.StatusCode)
	}
	// Byte-identical replay: rejected with 409.
	second, err := http.DefaultClient.Do(cloneWithBody(req, body))
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("replay got %d, want 409", second.StatusCode)
	}
	var events int
	if err := e.pool.QueryRow(ctx, `select count(*) from registry.trust_event
		where worker_id = $1 and event_type = 'replay'`, workerID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Fatal("replay left no trust event")
	}
}

func cloneWithBody(req *http.Request, body []byte) *http.Request {
	r := req.Clone(context.Background())
	r.Body = http.NoBody
	if len(body) > 0 {
		r.Body = readCloser(body)
	}
	return r
}

type rc struct{ *bytes.Reader }

func (rc) Close() error { return nil }

func readCloser(b []byte) rc { return rc{bytes.NewReader(b)} }

// TestKeyRotationOverlap: voluntary rotation keeps the old key valid through
// the overlap, then it ages out; the new key works immediately.
func TestKeyRotationOverlap(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")
	var workerID string
	if err := e.pool.QueryRow(ctx, `select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}

	oldKey := agentKey(t, st)
	_, newKey, _ := ed25519.GenerateKey(rand.Reader)
	oldClient := worker.NewClient(e.srv.URL, oldKey).WithID(workerID)
	if err := oldClient.RotateKey(ctx, newKey.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}

	// Both keys valid during overlap.
	if _, err := worker.NewClient(e.srv.URL, newKey).WithID(workerID).Heartbeat(ctx, "new"); err != nil {
		t.Fatalf("new key rejected during overlap: %v", err)
	}
	if _, err := worker.NewClient(e.srv.URL, oldKey).WithID(workerID).Heartbeat(ctx, "old"); err != nil {
		t.Fatalf("old key rejected during overlap: %v", err)
	}

	// Force the overlap to expire; the old key must die.
	if _, err := e.pool.Exec(ctx, `update registry.worker_key
		set valid_until = now() - interval '1 second'
		where worker_id = $1 and valid_until is not null`, workerID); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.NewClient(e.srv.URL, oldKey).WithID(workerID).Heartbeat(ctx, "old"); err == nil {
		t.Fatal("old key accepted after overlap expiry")
	}
	if _, err := worker.NewClient(e.srv.URL, newKey).WithID(workerID).Heartbeat(ctx, "new"); err != nil {
		t.Fatalf("new key rejected after overlap expiry: %v", err)
	}
}

// TestDemandedRotation: admin requests rotation; the agent performs it on its
// next heartbeat and ends up with two key rows and a cleared demand.
func TestDemandedRotation(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")
	var workerID string
	if err := e.pool.QueryRow(ctx, `select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}

	if err := e.reg.RequestRotation(ctx, workerID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		var n int
		_ = e.pool.QueryRow(ctx, `select count(*) from registry.worker_key
			where worker_id = $1`, workerID).Scan(&n)
		return n == 2
	}, "agent-performed rotation")

	var pending bool
	if err := e.pool.QueryRow(ctx, `select config ? 'rotate_requested'
		from registry.worker where id = $1`, workerID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("rotation demand not cleared after rotation")
	}
	// The agent keeps functioning on its new key.
	waitFor(t, 5*time.Second, func() bool {
		var hb *time.Time
		_ = e.pool.QueryRow(ctx, `select last_heartbeat_at from registry.worker
			where id = $1 and last_heartbeat_at > now() - interval '2 seconds'`,
			workerID).Scan(&hb)
		return hb != nil
	}, "heartbeats continuing on the new key")
}

// TestTrustSkeleton seeds heartbeat recency, tenure, and penalties and
// checks the computed components.
func TestTrustSkeleton(t *testing.T) {
	e := setup(t, "dev-token")
	ctx := context.Background()

	agent, st := newAgent(t, e, "dev-token")
	runCtx, cancel := context.WithCancel(ctx)
	go func() { _ = agent.Run(runCtx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")
	cancel()

	var workerID string
	if err := e.pool.QueryRow(ctx, `select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}
	// Two weeks of tenure → tenure ≈ 0.5; fresh heartbeat → availability 1.
	if _, err := e.pool.Exec(ctx, `update registry.worker
		set approved_at = now() - interval '14 days', last_heartbeat_at = now()
		where id = $1`, workerID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e.reg.RecordTrustEvent(ctx, workerID, "replay", "system")
	}

	scorer := &trust.Scorer{Pool: e.pool, Log: discard()}
	if err := scorer.ComputeAll(ctx); err != nil {
		t.Fatal(err)
	}
	var score, avail, tenure, penalty float64
	if err := e.pool.QueryRow(ctx, `select score,
		(components->>'availability')::float, (components->>'tenure')::float,
		(components->>'penalty')::float
		from registry.trust_score where worker_id = $1`,
		workerID).Scan(&score, &avail, &tenure, &penalty); err != nil {
		t.Fatal(err)
	}
	if avail != 1.0 {
		t.Fatalf("availability = %v, want 1.0", avail)
	}
	if tenure < 0.45 || tenure > 0.55 {
		t.Fatalf("tenure = %v, want ≈0.5 at 14 days", tenure)
	}
	if penalty != 0.3 {
		t.Fatalf("penalty = %v, want 0.3 for 3 events", penalty)
	}
	want := 1.0*(0.3+0.7*tenure) - 0.3
	if diff := score - want; diff > 0.001 || diff < -0.001 {
		t.Fatalf("score = %v, want %v", score, want)
	}
}

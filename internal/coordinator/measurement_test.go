package coordinator

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/observation"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/probe"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

// fakeProber answers instantly with a fixed RTT — the executor and the whole
// upload path are real; only the network I/O is simulated.
func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeProber struct{}

func (fakeProber) Type() string { return "icmp" }
func (fakeProber) Probe(_ context.Context, _ netip.Addr, _ probe.Params) (probe.Result, error) {
	return probe.Result{OK: true, RTTMillis: 12.5, PacketsSent: 4, PacketsLost: 0,
		Metrics: map[string]any{"rtt_min_ms": 11.0}}, nil
}

// seedAssignment inserts the routing + scheduling fixtures an assignment
// needs (provider → asn → snapshot → prefix → target → assignment) and
// patches the worker's local snapshot artifact so the target passes the
// worker-side snapshot check.
func seedAssignment(t *testing.T, e *env, st worker.State, target string) int64 {
	t.Helper()
	ctx := context.Background()
	const provider = "33333333-3333-3333-3333-333333333333"
	var snapshotID, prefixID, targetID, assignmentID int64
	if _, err := e.pool.Exec(ctx, `truncate scheduling.assignment cascade`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `truncate measurements.observation, measurements.upload_batch`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `insert into routing.provider
		(provider_id, name, monitoring_enabled, priority, synced_at)
		values ($1, 'Seeded', true, 10, now()) on conflict do nothing`, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `insert into routing.asn (asn, provider_id, synced_at)
		values (64510, $1, now()) on conflict do nothing`, provider); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `insert into routing.snapshot
		(version, source_uri, source_timestamp, status)
		values ('seed-'||clock_timestamp()::text, 'seed', now(), 'published')
		returning id`).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `insert into routing.prefix
		(snapshot_id, prefix, origin_asn) values ($1, '127.0.0.0/8', 64510)
		returning id`, snapshotID).Scan(&prefixID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `insert into routing.probe_target
		(snapshot_id, provider_id, prefix_id, address, rationale)
		values ($1, $2, $3, $4, 'test') returning id`,
		snapshotID, provider, prefixID, target).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `insert into scheduling.assignment
		(target_id, provider_id, probe_type, interval_seconds, redundancy_group)
		values ($1, $2, 'icmp', 1, gen_random_uuid()) returning id`,
		targetID, provider).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}

	// The worker's installed artifact is a fake blob; replace it with a real
	// SQLite file listing the target so the executor's snapshot check passes.
	db, err := sql.Open("sqlite", st.SnapshotPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table if not exists targets
		(address text primary key, provider_id text, prefix text);
		insert or ignore into targets values (?, ?, '127.0.0.0/8')`, target, provider); err != nil {
		t.Fatal(err)
	}
	return assignmentID
}

// TestProbeToDatabase is the Phase 5 gate: assignment → executor probes →
// signed batch upload → rows in measurements.observation with valid
// provenance.
func TestProbeToDatabase(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	exec := worker.NewExecutor(agent.Client(), probe.NewRegistry(fakeProber{}),
		agentKey(t, st), st, discard())
	exec.LeaseInterval = 100 * time.Millisecond
	exec.FlushInterval = 100 * time.Millisecond
	agent.SetExecutor(exec)

	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")

	assignmentID := seedAssignment(t, e, st, "127.0.0.1")

	var count int
	waitFor(t, 10*time.Second, func() bool {
		_ = e.pool.QueryRow(ctx,
			`select count(*) from measurements.observation where assignment_id = $1`,
			assignmentID).Scan(&count)
		return count > 0
	}, "observations landing in database")

	var okFlag bool
	var rtt float64
	var sig []byte
	var providerID string
	if err := e.pool.QueryRow(ctx, `select ok, rtt_ms, signature, provider_id::text
		from measurements.observation where assignment_id = $1
		order by measured_at limit 1`, assignmentID).Scan(&okFlag, &rtt, &sig, &providerID); err != nil {
		t.Fatal(err)
	}
	if !okFlag || rtt != 12.5 {
		t.Fatalf("observation ok=%v rtt=%v, want true/12.5", okFlag, rtt)
	}
	if providerID != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("provider_id = %s, want seeded provider", providerID)
	}
	if len(sig) == 0 {
		t.Fatal("stored observation has no signature")
	}
}

// TestUploadRejectsTamperAndForeignAssignments exercises the intake defenses.
func TestUploadRejectsTamperAndForeignAssignments(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")
	assignmentID := seedAssignment(t, e, st, "127.0.0.1")

	var workerID string
	if err := e.pool.QueryRow(ctx, `select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}
	client := worker.NewClient(e.srv.URL, agentKey(t, st)).WithID(workerID)
	if _, err := client.LeaseAssignments(ctx, 8); err != nil {
		t.Fatal(err)
	}

	rtt := 5.0
	good := observation.Observation{AssignmentID: assignmentID, Target: "127.0.0.1",
		ProbeType: "icmp", MeasuredAt: time.Now().UTC(), OK: true, RTTMillis: &rtt,
		PacketsSent: 4}
	if err := observation.Sign(&good, agentKey(t, st)); err != nil {
		t.Fatal(err)
	}
	tampered := good
	tampered.PacketsLost = 4 // altered after signing
	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	foreignSigned := good
	foreignSigned.Signature = ""
	if err := observation.Sign(&foreignSigned, wrongKey); err != nil {
		t.Fatal(err)
	}
	notMine := good
	notMine.AssignmentID = assignmentID + 999
	notMine.Signature = ""
	if err := observation.Sign(&notMine, agentKey(t, st)); err != nil {
		t.Fatal(err)
	}

	batch := []observation.Observation{good, tampered, foreignSigned, notMine}
	if err := client.UploadObservations(ctx, "11111111-2222-3333-4444-555555555555", batch); err != nil {
		t.Fatal(err)
	}
	var accepted int
	if err := e.pool.QueryRow(ctx,
		`select count(*) from measurements.observation`).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 {
		t.Fatalf("accepted %d observations, want exactly the 1 valid one", accepted)
	}

	// Idempotency: same batch again inserts nothing.
	if err := client.UploadObservations(ctx, "11111111-2222-3333-4444-555555555555", batch); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx,
		`select count(*) from measurements.observation`).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 {
		t.Fatalf("duplicate batch changed row count to %d", accepted)
	}
}

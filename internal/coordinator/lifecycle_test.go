package coordinator

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

// The worker approval lifecycle, end to end and from both sides.
//
// These exist because of a production failure whose whole signature was an
// absence: an administrator approved a worker on VPS Advisor, the website
// recorded it, and the worker went on logging "awaiting approval" forever
// because the decision never reached the coordinator. Nothing crashed and
// nothing disagreed loudly — the two systems simply held different states.
//
// So every test here asserts the *agreement*: what the website decided, what
// the coordinator stores, and what the worker itself believes must be the same
// value. A test that only checked the database would have passed throughout the
// original bug.

// advisorStub is a mutable stand-in for the VPS Advisor monitoring API. Unlike
// the fixture-driven mockadvisor it can be changed mid-test, which is what
// "an admin approves it while the worker is running" requires.
type advisorStub struct {
	mu          sync.Mutex
	enrollments []advisor.PendingEnrollment
	decisions   []advisor.Decision
	acked       map[string]int
	sinceSeen   []string
	pageSize    int // 0 = unpaginated
}

func newAdvisorStub(t *testing.T) (*advisorStub, *advisor.Client) {
	t.Helper()
	s := &advisorStub{acked: map[string]int{}}
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return s, advisor.New(srv.URL, "svc-token")
}

func (s *advisorStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/monitoring/providers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"providers": []any{}, "next_cursor": nil})
	})
	mux.HandleFunc("GET /api/v1/monitoring/enrollments/pending", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []advisor.PendingEnrollment{}
		for _, e := range s.enrollments {
			if s.acked[e.EnrollmentID] == 0 {
				out = append(out, e)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"enrollments": out, "next_cursor": nil})
	})
	mux.HandleFunc("POST /api/v1/monitoring/enrollments/{id}/registered", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.acked[r.PathValue("id")]++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/monitoring/admin/decisions", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		raw := r.URL.Query().Get("since")
		s.sinceSeen = append(s.sinceSeen, raw)
		var since time.Time
		if raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "bad since"})
				return
			}
			since = t
		}
		// Oldest first, strictly after `since` — the documented contract.
		rows := []advisor.Decision{}
		for _, d := range s.decisions {
			if d.DecidedAt.After(since) {
				rows = append(rows, d)
			}
		}
		// Keyset pagination on the decision id, mirroring the website.
		if c := r.URL.Query().Get("cursor"); c != "" {
			trimmed := rows[:0]
			seen := false
			for _, d := range rows {
				if seen {
					trimmed = append(trimmed, d)
				}
				if d.DecisionID == c {
					seen = true
				}
			}
			rows = trimmed
		}
		var next any
		if s.pageSize > 0 && len(rows) > s.pageSize {
			rows = rows[:s.pageSize]
			next = rows[len(rows)-1].DecisionID
		}
		writeJSON(w, http.StatusOK, map[string]any{"decisions": rows, "next_cursor": next})
	})
	return s.auth(mux)
}

func (s *advisorStub) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer svc-token" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "bad token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// enrol registers a worker on the "website" and returns its one-time token.
func (s *advisorStub) enrol(workerID, name string) string {
	token := "token-" + workerID
	sum := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enrollments = append(s.enrollments, advisor.PendingEnrollment{
		EnrollmentID: "en-" + workerID,
		WorkerID:     workerID,
		WorkerName:   name,
		OperatorID:   "44444444-4444-4444-4444-444444444444",
		TokenHash:    hex.EncodeToString(sum[:]),
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	return token
}

// decide records an admin decision, as the dashboard's approve/reject/suspend
// buttons do. decidedAt is the *website's* clock.
func (s *advisorStub) decide(workerID, state, reason string, decidedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, advisor.Decision{
		DecisionID: fmt.Sprintf("d-%d", len(s.decisions)+1),
		WorkerID:   workerID, State: state, Reason: reason, DecidedAt: decidedAt,
	})
}

func (s *advisorStub) ackCount(enrollmentID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acked[enrollmentID]
}

// dbState is the coordinator's stored state for a worker.
func dbState(t *testing.T, e *env, workerID string) string {
	t.Helper()
	var state string
	if err := e.pool.QueryRow(context.Background(),
		`select state from registry.worker where id = $1`, workerID).Scan(&state); err != nil {
		t.Fatalf("reading worker state: %v", err)
	}
	return state
}

// workerBelief is the state the worker itself reports, read from the
// status.json it writes for the host `vapn` CLI — the worker's own answer,
// not a second read of the database.
func workerBelief(st worker.State) string {
	s, err := worker.ReadStatus(st.Dir)
	if err != nil {
		return ""
	}
	return s.State
}

// requireAgreement is the assertion the original bug would have failed: the
// website, the coordinator and the worker all naming the same state.
func requireAgreement(t *testing.T, e *env, st worker.State, workerID, want string) {
	t.Helper()
	if got := dbState(t, e, workerID); got != want {
		t.Fatalf("coordinator state = %q, want %q", got, want)
	}
	waitFor(t, 5*time.Second, func() bool { return workerBelief(st) == want },
		fmt.Sprintf("worker to report state %q (it reports %q)", want, workerBelief(st)))
}

const advisorWorkerID = "d584fade-f0a3-47e2-ba50-780d587b8702"

// newSyncedEnv wires the coordinator's advisor client to a stub and provisions
// one enrolled-but-unapproved worker, mirroring the production shape: the
// website mints the worker id and the platform adopts it.
func newSyncedEnv(t *testing.T) (*env, *advisorStub, string) {
	t.Helper()
	e := setup(t, "")
	stub, client := newAdvisorStub(t)
	e.api.cfg.AdvisorClient = client
	token := stub.enrol(advisorWorkerID, "west-t.local")
	e.api.SyncAdvisor(context.Background(), time.Time{})
	if got := dbState(t, e, advisorWorkerID); got != "pending" {
		t.Fatalf("provisioned worker state = %q, want pending", got)
	}
	return e, stub, token
}

// TestRegisterLeavesWorkerPending: registration is not approval. The worker
// gets an identity and heartbeats, and everyone agrees it is pending.
func TestRegisterLeavesWorkerPending(t *testing.T) {
	e, _, token := newSyncedEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, token)
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	requireAgreement(t, e, st, advisorWorkerID, "pending")
	if v := st.SnapshotVersion(); v != "" {
		t.Fatalf("pending worker installed snapshot %s", v)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestApprovalReachesRunningWorker is the regression test for the reported
// failure: an admin approves on VPS Advisor while the worker is running, and
// the worker must transition to active on its own — no restart, no reinstall.
func TestApprovalReachesRunningWorker(t *testing.T) {
	e, stub, token := newSyncedEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, token)
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	requireAgreement(t, e, st, advisorWorkerID, "pending")

	// The admin approves. Nothing restarts.
	stub.decide(advisorWorkerID, "active", "verified operator", time.Now().UTC())
	e.api.SyncAdvisor(ctx, time.Time{})

	requireAgreement(t, e, st, advisorWorkerID, "active")
	// And an approved worker actually goes to work.
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() == e.version },
		"snapshot convergence after approval")

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestApprovalSurvivesWorkerRestart: a restarted worker reuses its persisted
// identity rather than enrolling again, and picks the approved state back up.
func TestApprovalSurvivesWorkerRestart(t *testing.T) {
	e, stub, token := newSyncedEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, token)
	first := make(chan error, 1)
	go func() { first <- agent.Run(ctx) }()
	requireAgreement(t, e, st, advisorWorkerID, "pending")

	stub.decide(advisorWorkerID, "active", "verified operator", time.Now().UTC())
	e.api.SyncAdvisor(ctx, time.Time{})
	requireAgreement(t, e, st, advisorWorkerID, "active")
	cancel()
	if err := <-first; err != nil {
		t.Fatal(err)
	}

	// Restart on the same state directory, with the enrollment token gone —
	// as the CLI leaves it once the token is spent.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	restarted, err := worker.NewAgent(worker.AgentConfig{
		CoordinatorURL:    e.srv.URL,
		Name:              "west-t.local",
		StateDir:          st.Dir,
		HeartbeatInterval: 50 * time.Millisecond,
		SnapshotPubKey:    e.signKey.Public().(ed25519.PublicKey),
	}, discard())
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() { second <- restarted.Run(ctx2) }()
	requireAgreement(t, e, st, advisorWorkerID, "active")

	// Still one worker: a restart must not enrol a second identity.
	var workers int
	if err := e.pool.QueryRow(ctx2, `select count(*) from registry.worker`).Scan(&workers); err != nil {
		t.Fatal(err)
	}
	if workers != 1 {
		t.Fatalf("restart created %d worker records, want 1", workers)
	}
	cancel2()
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

// TestRejectRetiresWorker: rejecting a registration is terminal. There is no
// `rejected` state — the lifecycle uses `retired`.
func TestRejectRetiresWorker(t *testing.T) {
	e, stub, token := newSyncedEnv(t)
	ctx := context.Background()

	agent, _ := newAgent(t, e, token)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- agent.Run(runCtx) }()
	waitFor(t, 5*time.Second, func() bool { return dbState(t, e, advisorWorkerID) == "pending" }, "pending")

	stub.decide(advisorWorkerID, "retired", "not a real operator", time.Now().UTC())
	e.api.SyncAdvisor(ctx, time.Time{})

	if got := dbState(t, e, advisorWorkerID); got != "retired" {
		t.Fatalf("state = %q, want retired", got)
	}
	// A retired worker's keys are revoked, so its next call is refused rather
	// than being told it is retired.
	waitFor(t, 5*time.Second, func() bool {
		_, err := agent.Client().Heartbeat(ctx, "test")
		return err != nil
	}, "retired worker to be locked out")
	cancel()
	<-done
}

// TestSuspendLocksWorkerOut: suspension stops work but keeps the identity, so
// the worker can be reinstated without reinstalling.
func TestSuspendLocksWorkerOut(t *testing.T) {
	e, stub, token := newSyncedEnv(t)
	ctx := context.Background()

	agent, st := newAgent(t, e, token)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- agent.Run(runCtx) }()

	stub.decide(advisorWorkerID, "active", "approved", time.Now().UTC())
	e.api.SyncAdvisor(ctx, time.Time{})
	requireAgreement(t, e, st, advisorWorkerID, "active")

	stub.decide(advisorWorkerID, "suspended", "abuse report", time.Now().UTC())
	e.api.SyncAdvisor(ctx, time.Time{})
	if got := dbState(t, e, advisorWorkerID); got != "suspended" {
		t.Fatalf("state = %q, want suspended", got)
	}
	waitFor(t, 5*time.Second, func() bool {
		_, err := agent.Client().Heartbeat(ctx, "test")
		return err != nil
	}, "suspended worker to be refused")

	// Reinstatement is possible because the identity survived.
	stub.decide(advisorWorkerID, "active", "cleared", time.Now().UTC())
	e.api.SyncAdvisor(ctx, time.Time{})
	requireAgreement(t, e, st, advisorWorkerID, "active")
	cancel()
	<-done
}

// TestDecisionCursorIgnoresLocalClock is the regression test for the class of
// bug that stranded the reported worker: the sync cursor used to be this
// host's clock, so any coordinator running ahead of the website silently
// dropped every decision recorded inside the difference. The cursor must come
// from the website's own `decided_at`.
func TestDecisionCursorIgnoresLocalClock(t *testing.T) {
	e, stub, _ := newSyncedEnv(t)
	ctx := context.Background()

	// The website's clock reads ten minutes behind this host's.
	const skew = 10 * time.Minute
	websiteNow := time.Now().UTC().Add(-skew)

	// A pass with nothing to do must not park the cursor in the website's
	// future, which is what "advance to local now" did.
	cursor := e.api.SyncAdvisor(ctx, time.Time{})
	if cursor.After(websiteNow) {
		t.Fatalf("cursor %s is ahead of the website's clock %s: a decision recorded "+
			"now would never be fetched", cursor, websiteNow)
	}

	stub.decide(advisorWorkerID, "active", "approved on a slower clock", websiteNow)
	cursor = e.api.SyncAdvisor(ctx, cursor)
	if got := dbState(t, e, advisorWorkerID); got != "active" {
		t.Fatalf("state = %q, want active — the approval was lost to clock skew", got)
	}
	if !cursor.Equal(websiteNow) {
		t.Fatalf("cursor = %s, want the applied decision's decided_at %s", cursor, websiteNow)
	}

	// And the advanced cursor must not re-deliver what it already applied.
	stub.mu.Lock()
	stub.sinceSeen = nil
	stub.mu.Unlock()
	e.api.SyncAdvisor(ctx, cursor)
	stub.mu.Lock()
	seen := append([]string(nil), stub.sinceSeen...)
	stub.mu.Unlock()
	if len(seen) != 1 || seen[0] == "" {
		t.Fatalf("expected one since-bounded poll, got %v", seen)
	}
}

// TestDecisionFeedPaginationIsFollowed: the website pages the feed, and a
// client that reads only the first page loses every approval after it.
func TestDecisionFeedPaginationIsFollowed(t *testing.T) {
	e, stub, _ := newSyncedEnv(t)
	ctx := context.Background()
	stub.pageSize = 1

	// Three decisions, oldest first; only the last is the state we expect to
	// land, and it is reachable only by following two cursors.
	base := time.Now().UTC().Add(-time.Hour)
	stub.decide(advisorWorkerID, "active", "approved", base)
	stub.decide(advisorWorkerID, "suspended", "paused", base.Add(time.Minute))
	stub.decide(advisorWorkerID, "active", "reinstated", base.Add(2*time.Minute))

	e.api.SyncAdvisor(ctx, time.Time{})
	if got := dbState(t, e, advisorWorkerID); got != "active" {
		t.Fatalf("state = %q, want active: pagination was not followed to the end", got)
	}
}

// TestEnrollmentAckedWhenWorkerAlreadyExists: the ack is what drops an
// enrollment out of the pending feed. Gating it on "we just created the worker
// row" left a re-issued token being re-ingested on every pass, forever.
func TestEnrollmentAckedWhenWorkerAlreadyExists(t *testing.T) {
	e, stub, _ := newSyncedEnv(t) // first pass already ingested and acked
	ctx := context.Background()
	if n := stub.ackCount("en-" + advisorWorkerID); n != 1 {
		t.Fatalf("first pass acked %d times, want 1", n)
	}

	// The operator loses the token and regenerates it: a second enrollment for
	// a worker the platform already holds.
	sum := sha256.Sum256([]byte("second-token"))
	stub.mu.Lock()
	stub.enrollments = append(stub.enrollments, advisor.PendingEnrollment{
		EnrollmentID: "en-second", WorkerID: advisorWorkerID, WorkerName: "west-t.local",
		OperatorID: "44444444-4444-4444-4444-444444444444",
		TokenHash:  hex.EncodeToString(sum[:]), ExpiresAt: time.Now().Add(time.Hour),
	})
	stub.mu.Unlock()

	e.api.SyncAdvisor(ctx, time.Time{})
	if n := stub.ackCount("en-second"); n != 1 {
		t.Fatalf("re-issued enrollment acked %d times, want 1 — it would be "+
			"re-ingested on every pass", n)
	}
	// The new token is redeemable: the ack must not have skipped the ingest.
	var tokens int
	if err := e.pool.QueryRow(ctx, `select count(*) from registry.enrollment_token
		where worker_id = $1`, advisorWorkerID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 2 {
		t.Fatalf("enrollment tokens = %d, want 2", tokens)
	}
}

// TestAdvisorSyncHealthIsReported: a failing pull must be visible on the admin
// surface. The original incident was invisible precisely because it was not.
func TestAdvisorSyncHealthIsReported(t *testing.T) {
	e := setup(t, "")
	ctx := context.Background()

	// A site that answers nothing the contract defines — the shape of a wrong
	// VAPN_ADVISOR_URL.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer dead.Close()
	e.api.cfg.AdvisorClient = advisor.New(dead.URL, "svc")
	e.api.SyncAdvisor(ctx, time.Time{})

	health := e.api.AdvisorHealth()
	d, ok := health["decisions"]
	if !ok {
		t.Fatal("decisions feed health not reported")
	}
	if d.FailureStreak == 0 || d.LastError == "" {
		t.Fatalf("failing decision feed reported as healthy: %+v", d)
	}
	if d.LastSuccessAt != nil {
		t.Fatalf("never-successful feed reports a success time: %+v", d)
	}

	// And it reaches the admin overview an operator actually reads.
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/admin/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		AdvisorSync map[string]struct {
			LastError     string `json:"last_error"`
			FailureStreak int    `json:"consecutive_failures"`
		} `json:"advisor_sync"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AdvisorSync["decisions"].FailureStreak == 0 {
		t.Fatalf("admin overview hides the failing feed: %+v", body.AdvisorSync)
	}
}

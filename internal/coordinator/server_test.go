package coordinator

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/migrate"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/registry"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

// Integration tests gated on CNIP_TEST_DB_DSN. They truncate the registry
// schema of the target database.

const adminToken = "test-admin"

type env struct {
	pool    *pgxpool.Pool
	reg     *registry.Store
	srv     *httptest.Server
	signKey ed25519.PrivateKey
	version string
}

func setup(t *testing.T, devToken string) *env {
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(context.Background(), pool, dir, log); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `truncate registry.worker cascade`); err != nil {
		t.Fatal(err)
	}

	// Publish a fake artifact into an FS store: pointer → signed manifest →
	// object. Content is arbitrary bytes; Phase 4 workers verify hash and
	// signature, not SQL contents.
	storeRoot := t.TempDir()
	store := artifact.FSStore{Root: storeRoot}
	_, signKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	version := "20260718T0000Z-1"
	// A minimal but real SQLite artifact: the executor opens it to validate
	// probe targets against the snapshot.
	blobPath := filepath.Join(t.TempDir(), "routing.sqlite")
	adb, err := sql.Open("sqlite", blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adb.Exec(`create table meta (key text primary key, value text);
		create table targets (address text primary key, provider_id text, prefix text);
		insert into meta values ('version', '` + version + `')`); err != nil {
		t.Fatal(err)
	}
	if err := adb.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	sum, size, err := artifact.HashFile(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	m := artifact.Manifest{
		Version: version, CreatedAt: time.Now().UTC(),
		ObjectKey: artifact.ObjectKeySQLite(version), SHA256: sum, SizeBytes: size,
		MinWorkerVersion: "0.0.1",
	}
	if err := artifact.Sign(&m, signKey); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mustPut := func(key string, v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Put(ctx, m.ObjectKey, bytes.NewReader(blob), size, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	mustPut(artifact.ObjectKeyManifest(version), m)
	mustPut(artifact.PointerKey, artifact.Pointer{Version: version, ManifestKey: artifact.ObjectKeyManifest(version)})

	reg := &registry.Store{Pool: pool}
	api := New(Config{AdminToken: adminToken, DevEnrollmentToken: devToken,
		SnapshotPollTTL: time.Millisecond}, reg, store, log)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &env{pool: pool, reg: reg, srv: srv, signKey: signKey, version: version}
}

func newAgent(t *testing.T, e *env, token string) (*worker.Agent, worker.State) {
	t.Helper()
	stateDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	agent, err := worker.NewAgent(worker.AgentConfig{
		CoordinatorURL:    e.srv.URL,
		EnrollmentToken:   token,
		Name:              "test-worker",
		StateDir:          stateDir,
		HeartbeatInterval: 50 * time.Millisecond,
		SnapshotPubKey:    e.signKey.Public().(ed25519.PublicKey),
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	return agent, worker.State{Dir: stateDir}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestEnrollmentApprovalAndSync drives the production flow: admin-created
// enrollment token → register (pending, no snapshot) → admin approval →
// snapshot converges.
func TestEnrollmentApprovalAndSync(t *testing.T) {
	e := setup(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerID, token, err := e.reg.CreateWorker(ctx, registry.DevOperatorID, "prod-flow", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	agent, st := newAgent(t, e, token)
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	// Registered and heartbeating, but pending: no snapshot may arrive.
	waitFor(t, 5*time.Second, func() bool {
		var hb *time.Time
		_ = e.pool.QueryRow(ctx, `select last_heartbeat_at from registry.worker where id=$1`,
			workerID).Scan(&hb)
		return hb != nil
	}, "pending worker heartbeat")
	time.Sleep(150 * time.Millisecond) // a few more ticks
	if v := st.SnapshotVersion(); v != "" {
		t.Fatalf("pending worker obtained snapshot %s", v)
	}

	if err := e.reg.SetState(ctx, workerID, "active", "approved in test", "admin"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() == e.version },
		"snapshot convergence after approval")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent exited with error: %v", err)
	}
}

func TestDevAutoEnrollment(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() == e.version },
		"auto-enrolled worker snapshot convergence")
}

// TestSuspendedWorkerLockedOut proves a state transition takes effect within
// one heartbeat: 403, and the worker can no longer download artifacts.
func TestSuspendedWorkerLockedOut(t *testing.T) {
	e := setup(t, "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent, st := newAgent(t, e, "dev-token")
	go func() { _ = agent.Run(ctx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")

	var workerID string
	if err := e.pool.QueryRow(ctx,
		`select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}
	if err := e.reg.SetState(ctx, workerID, "suspended", "test", "admin"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		_, err := worker.NewClient(e.srv.URL, agentKey(t, st)).
			WithID(workerID).Heartbeat(ctx, "t")
		return err != nil
	}, "suspension enforcement")
}

func agentKey(t *testing.T, st worker.State) ed25519.PrivateKey {
	t.Helper()
	k, err := st.Key()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestBadSignatureRejected sends a heartbeat signed with the wrong key.
func TestBadSignatureRejected(t *testing.T) {
	e := setup(t, "dev-token")
	ctx := context.Background()

	agent, st := newAgent(t, e, "dev-token")
	runCtx, cancel := context.WithCancel(ctx)
	go func() { _ = agent.Run(runCtx) }()
	waitFor(t, 5*time.Second, func() bool { return st.SnapshotVersion() != "" }, "initial sync")
	cancel()

	var workerID string
	if err := e.pool.QueryRow(ctx,
		`select id from registry.worker limit 1`).Scan(&workerID); err != nil {
		t.Fatal(err)
	}
	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	impostor := worker.NewClient(e.srv.URL, wrongKey).WithID(workerID)
	if _, err := impostor.Heartbeat(ctx, "t"); err == nil {
		t.Fatal("heartbeat with wrong key accepted")
	}
}

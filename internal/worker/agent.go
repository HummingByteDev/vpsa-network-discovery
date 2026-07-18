package worker

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/version"
)

type AgentConfig struct {
	CoordinatorURL    string
	EnrollmentToken   string // required only before first registration
	Name              string
	StateDir          string
	HeartbeatInterval time.Duration
	SnapshotPubKey    ed25519.PublicKey // pinned artifact verification key
}

type Agent struct {
	cfg    AgentConfig
	state  State
	client *Client
	log    *slog.Logger

	executor     *Executor
	executorStop context.CancelFunc
}

func NewAgent(cfg AgentConfig, log *slog.Logger) (*Agent, error) {
	st := State{Dir: cfg.StateDir}
	if err := st.Ensure(); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	key, err := st.Key()
	if err != nil {
		return nil, err
	}
	client := NewClient(cfg.CoordinatorURL, key)
	if id, err := st.WorkerID(); err != nil {
		return nil, err
	} else if id != "" {
		client.WorkerID = id
	}
	return &Agent{cfg: cfg, state: st, client: client, log: log}, nil
}

// Run drives the agent lifecycle: register once, then heartbeat and keep the
// snapshot in sync until ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	if a.client.WorkerID == "" {
		if err := a.register(ctx); err != nil {
			return err
		}
	}
	a.log.Info("agent running", "worker_id", a.client.WorkerID,
		"snapshot", orNone(a.state.SnapshotVersion()))

	interval := a.cfg.HeartbeatInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	a.tick(ctx) // immediate first beat
	for {
		select {
		case <-ctx.Done():
			a.log.Info("agent shutting down")
			return nil
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Agent) register(ctx context.Context) error {
	if a.cfg.EnrollmentToken == "" {
		return fmt.Errorf("first boot requires CNIP_ENROLLMENT_TOKEN")
	}
	// The coordinator may not be up yet (compose start order): retry briefly.
	var resp *RegisterResponse
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		resp, err = a.client.Register(ctx, a.cfg.EnrollmentToken, a.cfg.Name, version.Version)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return fmt.Errorf("registration: %w", err)
	}
	if err := a.state.SaveWorkerID(resp.WorkerID); err != nil {
		return err
	}
	a.log.Info("registered", "worker_id", resp.WorkerID, "state", resp.State)
	return nil
}

func (a *Agent) tick(ctx context.Context) {
	hb, err := a.client.Heartbeat(ctx, version.Version)
	if err != nil {
		a.log.Warn("heartbeat failed", "error", err)
		return
	}
	switch hb.State {
	case "pending":
		a.log.Info("awaiting approval")
		return
	case "active", "quarantined":
	default:
		a.log.Warn("unexpected state in heartbeat", "state", hb.State)
		return
	}
	if hb.Snapshot != nil && hb.Snapshot.Version != a.state.SnapshotVersion() {
		if err := a.SyncSnapshot(ctx); err != nil {
			a.log.Error("snapshot sync failed", "error", err)
		}
	}
	// With a verified snapshot in place, start measuring (Phase 5+).
	if a.executor != nil && a.executorStop == nil && a.state.SnapshotVersion() != "" {
		execCtx, cancel := context.WithCancel(ctx)
		a.executorStop = cancel
		go a.executor.Run(execCtx)
		a.log.Info("probe executor started")
	}
}

// SetExecutor arms the measurement executor; it starts once the worker is
// active and a verified snapshot is installed.
func (a *Agent) SetExecutor(e *Executor) { a.executor = e }

// Client exposes the signed coordinator client for executor construction.
func (a *Agent) Client() *Client { return a.client }

// StateHandle exposes the persistent state directory helper.
func (a *Agent) StateHandle() State { return a.state }

// SyncSnapshot downloads the advertised artifact, verifies it against the
// signed manifest and the pinned public key, and atomically installs it.
// Every failure leaves the previously installed snapshot untouched.
func (a *Agent) SyncSnapshot(ctx context.Context) error {
	mr, err := a.client.CurrentManifest(ctx)
	if err != nil {
		return err
	}
	m := mr.Manifest
	if err := artifact.Verify(m, a.cfg.SnapshotPubKey); err != nil {
		return fmt.Errorf("manifest rejected: %w", err)
	}
	if m.Version == a.state.SnapshotVersion() {
		return nil
	}
	// Downgrade protection: versions embed the build timestamp and are
	// lexically ordered; a "new" version older than the installed one means
	// a tampered pointer or a deliberate (audited) rollback. Refuse unless
	// the local artifact is absent.
	if local := a.state.SnapshotVersion(); local != "" && m.Version < local {
		a.log.Warn("advertised snapshot is older than installed; treating as rollback",
			"installed", local, "advertised", m.Version)
	}
	tmp, err := a.client.DownloadArtifact(ctx, mr.DownloadPath, a.state.Dir)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := artifact.VerifyFile(tmp, m); err != nil {
		return fmt.Errorf("artifact rejected: %w", err)
	}
	if err := a.state.InstallSnapshot(tmp, m.Version); err != nil {
		return err
	}
	a.log.Info("snapshot installed", "version", m.Version,
		"prefixes_v4", m.PrefixCountV4, "prefixes_v6", m.PrefixCountV6,
		"targets", m.TargetCount)
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// Doctor prints a human-readable self-diagnosis and returns non-nil if any
// check fails. Run via `worker doctor`.
func (a *Agent) Doctor(ctx context.Context) error {
	fail := 0
	check := func(name string, err error) {
		if err != nil {
			fail++
			fmt.Printf("✗ %-22s %v\n", name, err)
		} else {
			fmt.Printf("✓ %-22s ok\n", name)
		}
	}
	_, err := a.state.Key()
	check("identity key", err)
	id, _ := a.state.WorkerID()
	if id == "" {
		check("registration", fmt.Errorf("not registered yet"))
	} else {
		check("registration", nil)
		_, err = a.client.Heartbeat(ctx, version.Version)
		check("coordinator reachable", err)
	}
	if v := a.state.SnapshotVersion(); v == "" {
		check("routing snapshot", fmt.Errorf("not installed yet"))
	} else {
		check("routing snapshot", nil)
		fmt.Printf("  snapshot version: %s\n", v)
	}
	if fail > 0 {
		return fmt.Errorf("%d check(s) failed", fail)
	}
	return nil
}

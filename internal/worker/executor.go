package worker

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/observation"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/probe"
)

// Executor runs leased assignments: one goroutine per assignment probing on
// its interval (with jitter), a bounded queue, and a batching uploader.
// Assignments always come from the coordinator; before probing anything the
// executor checks the target against the verified local snapshot — a worker
// never probes an address the signed snapshot doesn't list.
type Executor struct {
	client   *Client
	registry probe.Registry
	key      ed25519.PrivateKey
	state    State
	log      *slog.Logger

	LeaseInterval time.Duration // default 60s
	FlushInterval time.Duration // default 30s
	Capacity      int           // max concurrent assignments (default 64)

	queue   chan observation.Observation
	mu      sync.Mutex
	running map[int64]context.CancelFunc

	// Self-reporting for status.json (read by the vapn CLI).
	submitted    atomic.Uint64
	lastUploadAt atomic.Int64 // unix millis
	lastUploadMS atomic.Int64
}

// Stats snapshots the executor's self-reported counters.
func (e *Executor) Stats() (assignments int, submitted uint64, lastUpload time.Time, lastMS int64, queueDepth int) {
	e.mu.Lock()
	assignments = len(e.running)
	e.mu.Unlock()
	if ms := e.lastUploadAt.Load(); ms > 0 {
		lastUpload = time.UnixMilli(ms).UTC()
	}
	return assignments, e.submitted.Load(), lastUpload, e.lastUploadMS.Load(), len(e.queue)
}

func NewExecutor(client *Client, registry probe.Registry, key ed25519.PrivateKey, state State, log *slog.Logger) *Executor {
	return &Executor{
		client: client, registry: registry, key: key, state: state, log: log,
		LeaseInterval: 60 * time.Second,
		FlushInterval: 30 * time.Second,
		Capacity:      64,
		queue:         make(chan observation.Observation, 4096),
		running:       map[int64]context.CancelFunc{},
	}
}

func (e *Executor) Run(ctx context.Context) {
	go e.uploader(ctx)
	ticker := time.NewTicker(e.LeaseInterval)
	defer ticker.Stop()
	e.lease(ctx)
	for {
		select {
		case <-ctx.Done():
			e.stopAll()
			return
		case <-ticker.C:
			e.lease(ctx)
		}
	}
}

func (e *Executor) lease(ctx context.Context) {
	assignments, err := e.client.LeaseAssignments(ctx, e.Capacity)
	if err != nil {
		e.log.Warn("assignment lease failed", "error", err)
		return
	}
	current := map[int64]Assignment{}
	for _, a := range assignments {
		current[a.ID] = a
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Stop assignments we no longer hold.
	for id, cancel := range e.running {
		if _, ok := current[id]; !ok {
			cancel()
			delete(e.running, id)
		}
	}
	// Start new ones.
	for id, a := range current {
		if _, ok := e.running[id]; ok {
			continue
		}
		if !e.targetInSnapshot(a.Target) {
			e.log.Warn("assignment target not in verified snapshot; refusing",
				"assignment", id, "target", a.Target)
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		e.running[id] = cancel
		go e.probeLoop(runCtx, a)
	}
	e.log.Debug("assignments reconciled", "held", len(e.running))
}

func (e *Executor) stopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, cancel := range e.running {
		cancel()
		delete(e.running, id)
	}
}

// targetInSnapshot consults the verified SQLite artifact.
func (e *Executor) targetInSnapshot(target string) bool {
	db, err := sql.Open("sqlite", "file:"+e.state.SnapshotPath()+"?mode=ro")
	if err != nil {
		return false
	}
	defer db.Close()
	var one int
	err = db.QueryRow("select 1 from targets where address = ?", target).Scan(&one)
	return err == nil
}

func (e *Executor) probeLoop(ctx context.Context, a Assignment) {
	prober, err := e.registry.Get(a.ProbeType)
	if err != nil {
		e.log.Warn("unsupported probe type", "assignment", a.ID, "type", a.ProbeType)
		return
	}
	target, err := netip.ParseAddr(a.Target)
	if err != nil {
		e.log.Warn("bad target address", "assignment", a.ID, "target", a.Target)
		return
	}
	interval := time.Duration(a.IntervalSeconds) * time.Second
	// Jitter spreads fleet load and avoids synchronized bursts at the target.
	select {
	case <-ctx.Done():
		return
	case <-time.After(rand.N(interval)):
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		e.execute(ctx, prober, a, target)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Executor) execute(ctx context.Context, prober probe.Prober, a Assignment, target netip.Addr) {
	res, err := prober.Probe(ctx, target, probe.Params{})
	measured := time.Now().UTC()
	if err != nil {
		e.log.Warn("probe execution failed", "assignment", a.ID, "error", err)
		return
	}
	obs := observation.Observation{
		AssignmentID: a.ID,
		Target:       a.Target,
		ProbeType:    a.ProbeType,
		MeasuredAt:   measured,
		OK:           res.OK,
		PacketsSent:  res.PacketsSent,
		PacketsLost:  res.PacketsLost,
		Metrics:      res.Metrics,
	}
	if res.OK {
		rtt := res.RTTMillis
		obs.RTTMillis = &rtt
	}
	if err := observation.Sign(&obs, e.key); err != nil {
		e.log.Error("observation signing failed", "error", err)
		return
	}
	select {
	case e.queue <- obs:
	default:
		e.log.Warn("observation queue full; dropping oldest")
		select {
		case <-e.queue:
		default:
		}
		select {
		case e.queue <- obs:
		default:
		}
	}
}

func (e *Executor) uploader(ctx context.Context) {
	ticker := time.NewTicker(e.FlushInterval)
	defer ticker.Stop()
	var pending []observation.Observation
	flush := func(flushCtx context.Context) {
		for len(pending) > 0 {
			n := len(pending)
			if n > 256 {
				n = 256
			}
			batch := pending[:n]
			uploadStart := time.Now()
			if err := e.client.UploadObservations(flushCtx, uuid.NewString(), batch); err != nil {
				e.log.Warn("observation upload failed; will retry", "count", len(pending), "error", err)
				return
			}
			e.submitted.Add(uint64(n))
			e.lastUploadAt.Store(time.Now().UnixMilli())
			e.lastUploadMS.Store(time.Since(uploadStart).Milliseconds())
			e.log.Info("observations uploaded", "count", n)
			pending = pending[n:]
		}
	}
	for {
		select {
		case <-ctx.Done():
			// Final flush with a short grace period (graceful shutdown).
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			for {
				select {
				case obs := <-e.queue:
					pending = append(pending, obs)
					continue
				default:
				}
				break
			}
			flush(drainCtx)
			cancel()
			return
		case obs := <-e.queue:
			pending = append(pending, obs)
			if len(pending) >= 256 {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		}
	}
}

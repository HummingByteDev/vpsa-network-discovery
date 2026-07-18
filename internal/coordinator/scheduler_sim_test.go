package coordinator

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"testing"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/registry"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/scheduler"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

// seedFleet inserts nProviders × targetsEach published targets. Provider i
// owns ASN 64600+i; priorities alternate to exercise the interval policy.
func seedFleet(t *testing.T, e *env, nProviders, targetsEach int) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"scheduling.assignment cascade",
		"routing.probe_target cascade", "routing.prefix cascade",
		"routing.snapshot cascade", "routing.asn cascade", "routing.provider cascade"} {
		if _, err := e.pool.Exec(ctx, "truncate "+table); err != nil {
			t.Fatal(err)
		}
	}
	var snapshotID int64
	if err := e.pool.QueryRow(ctx, `insert into routing.snapshot
		(version, source_uri, source_timestamp, status)
		values ('sim', 'sim', now(), 'published') returning id`).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nProviders; i++ {
		provider := fmt.Sprintf("%08d-0000-0000-0000-000000000000", i+1)
		priority := 5
		if i%2 == 1 {
			priority = 100
		}
		if _, err := e.pool.Exec(ctx, `insert into routing.provider
			(provider_id, name, monitoring_enabled, priority, synced_at)
			values ($1, $2, true, $3, now())`, provider, fmt.Sprintf("P%d", i), priority); err != nil {
			t.Fatal(err)
		}
		if _, err := e.pool.Exec(ctx, `insert into routing.asn (asn, provider_id, synced_at)
			values ($1, $2, now())`, 64600+i, provider); err != nil {
			t.Fatal(err)
		}
		var prefixID int64
		if err := e.pool.QueryRow(ctx, `insert into routing.prefix
			(snapshot_id, prefix, origin_asn) values ($1, $2, $3) returning id`,
			snapshotID, fmt.Sprintf("10.%d.0.0/16", 100+i), 64600+i).Scan(&prefixID); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < targetsEach; j++ {
			if _, err := e.pool.Exec(ctx, `insert into routing.probe_target
				(snapshot_id, provider_id, prefix_id, address, rationale)
				values ($1, $2, $3, $4, 'sim')`,
				snapshotID, provider, prefixID,
				fmt.Sprintf("10.%d.0.%d", 100+i, j+1)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func makeSimWorkers(t *testing.T, e *env, n int) []*worker.Client {
	t.Helper()
	ctx := context.Background()
	clients := make([]*worker.Client, 0, n)
	for i := 0; i < n; i++ {
		workerID, token, err := e.reg.CreateWorker(ctx, registry.DevOperatorID,
			fmt.Sprintf("sim-%d", i), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		st := worker.State{Dir: t.TempDir()}
		if err := st.Ensure(); err != nil {
			t.Fatal(err)
		}
		key, err := st.Key()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.reg.Register(ctx, token, key.Public().(ed25519.PublicKey), "sim"); err != nil {
			t.Fatal(err)
		}
		if err := e.reg.SetState(ctx, workerID, "active", "sim", "test"); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, worker.NewClient(e.srv.URL, key).WithID(workerID))
	}
	return clients
}

func TestSchedulerSimulation(t *testing.T) {
	e := setup(t, "")
	ctx := context.Background()
	const (
		nProviders  = 10
		targetsEach = 5
		redundancy  = 3
		nWorkers    = 20
		capacity    = 12
	)
	seedFleet(t, e, nProviders, targetsEach)

	sched := &scheduler.Scheduler{Pool: e.pool,
		Cfg: scheduler.Config{Redundancy: redundancy}, Log: discard()}
	if err := sched.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := e.pool.QueryRow(ctx, `select count(*) from scheduling.assignment
		where status in ('open','leased')`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != nProviders*targetsEach*redundancy {
		t.Fatalf("assignments = %d, want %d", total, nProviders*targetsEach*redundancy)
	}
	// Idempotent: second pass creates nothing.
	if err := sched.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var again int
	if err := e.pool.QueryRow(ctx, `select count(*) from scheduling.assignment
		where status in ('open','leased')`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != total {
		t.Fatalf("reconcile not idempotent: %d → %d", total, again)
	}
	// Interval policy: high-priority providers probe faster.
	var fast, slow int
	if err := e.pool.QueryRow(ctx, `select min(interval_seconds), max(interval_seconds)
		from scheduling.assignment`).Scan(&fast, &slow); err != nil {
		t.Fatal(err)
	}
	if fast != 30 || slow != 120 {
		t.Fatalf("interval policy: fast=%d slow=%d, want 30/120", fast, slow)
	}

	clients := makeSimWorkers(t, e, nWorkers)
	// Workers 0 and 1 probe from provider 0's network (ASN 64600).
	for _, c := range clients[:2] {
		if _, err := e.pool.Exec(ctx, `update registry.worker set source_asn = 64600
			where id = $1`, c.WorkerID); err != nil {
			t.Fatal(err)
		}
	}

	leaseAll := func(cs []*worker.Client) {
		for _, c := range cs {
			if _, err := c.LeaseAssignments(ctx, capacity); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Round 1: everyone leases. Round 2: five workers die; survivors keep
	// renewing until the dead workers' leases expire and get reclaimed.
	leaseAll(clients)
	alive := clients[5:]
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(800 * time.Millisecond) // < the 2.5s TTL: alive renew in time, dead expire
		leaseAll(alive)
		var uncovered int
		if err := e.pool.QueryRow(ctx, `select count(*) from scheduling.assignment a
			where a.status in ('open','leased') and not exists (
			  select 1 from scheduling.lease l
			  where l.assignment_id = a.id and l.released_at is null
			    and l.worker_id = any($1))`,
			workerIDs(alive)).Scan(&uncovered); err != nil {
			t.Fatal(err)
		}
		if uncovered == 0 {
			break
		}
	}

	// Every redundancy group fully covered by distinct live workers.
	var badGroups int
	if err := e.pool.QueryRow(ctx, `select count(*) from (
		select a.redundancy_group,
		       count(distinct l.worker_id) filter (where l.released_at is null) as live
		from scheduling.assignment a
		left join scheduling.lease l on l.assignment_id = a.id
		where a.status in ('open','leased')
		group by 1 having count(distinct l.worker_id) filter (where l.released_at is null) < $1
	) g`, redundancy).Scan(&badGroups); err != nil {
		t.Fatal(err)
	}
	if badGroups != 0 {
		t.Fatalf("%d redundancy groups below %d distinct live workers", badGroups, redundancy)
	}
	// No worker holds two replicas of one group.
	var dupes int
	if err := e.pool.QueryRow(ctx, `select count(*) from (
		select a.redundancy_group, l.worker_id
		from scheduling.lease l join scheduling.assignment a on a.id = l.assignment_id
		where l.released_at is null
		group by 1, 2 having count(*) > 1) d`).Scan(&dupes); err != nil {
		t.Fatal(err)
	}
	if dupes != 0 {
		t.Fatalf("%d (group, worker) pairs hold multiple replicas", dupes)
	}
	// Self-ASN exclusion: workers on 64600 never hold provider 0 assignments.
	var violations int
	if err := e.pool.QueryRow(ctx, `select count(*)
		from scheduling.lease l
		join scheduling.assignment a on a.id = l.assignment_id
		join registry.worker w on w.id = l.worker_id
		join routing.asn ra on ra.provider_id = a.provider_id
		where l.released_at is null and w.source_asn = ra.asn`).Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("%d self-ASN violations", violations)
	}
	// ProbePolicy cap honored.
	var overCap int
	if err := e.pool.QueryRow(ctx, `select count(*) from (
		select worker_id from scheduling.lease where released_at is null
		group by 1 having count(*) > $1) o`, capacity).Scan(&overCap); err != nil {
		t.Fatal(err)
	}
	if overCap != 0 {
		t.Fatalf("%d workers over the %d-assignment cap", overCap, capacity)
	}
}

func workerIDs(cs []*worker.Client) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.WorkerID
	}
	return out
}

// TestDrainOnSupersede: superseding the snapshot drains everything; a new
// published snapshot's targets get fresh assignments.
func TestDrainOnSupersede(t *testing.T) {
	e := setup(t, "")
	ctx := context.Background()
	seedFleet(t, e, 2, 3)
	sched := &scheduler.Scheduler{Pool: e.pool,
		Cfg: scheduler.Config{Redundancy: 2}, Log: discard()}
	if err := sched.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.pool.Exec(ctx,
		`update routing.snapshot set status = 'superseded' where version = 'sim'`); err != nil {
		t.Fatal(err)
	}
	if err := sched.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var open, closed int
	if err := e.pool.QueryRow(ctx, `select
		count(*) filter (where status in ('open','leased')),
		count(*) filter (where status = 'closed')
		from scheduling.assignment`).Scan(&open, &closed); err != nil {
		t.Fatal(err)
	}
	if open != 0 || closed != 12 {
		t.Fatalf("after supersede: open=%d closed=%d, want 0/12", open, closed)
	}
}

// Package scheduler turns published probe targets into redundant assignments
// and keeps the assignment pool consistent with the routing snapshot and
// provider configuration (docs/architecture/01 §4.2). It generates work; the
// coordinator's lease endpoint distributes it (with diversity and
// self-network exclusion at claim time).
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Redundancy int // workers per target (default 3)
	// Interval policy by provider priority; floors are ProbePolicy safety.
	FastInterval int // seconds, priority <= 10 (default 30)
	MidInterval  int // seconds, priority <= 50 (default 60)
	SlowInterval int // seconds, otherwise (default 120)
	MinInterval  int // absolute floor (default 15)
}

func (c Config) withDefaults() Config {
	if c.Redundancy <= 0 {
		c.Redundancy = 3
	}
	if c.FastInterval <= 0 {
		c.FastInterval = 30
	}
	if c.MidInterval <= 0 {
		c.MidInterval = 60
	}
	if c.SlowInterval <= 0 {
		c.SlowInterval = 120
	}
	if c.MinInterval <= 0 {
		c.MinInterval = 15
	}
	return c
}

type Scheduler struct {
	Pool *pgxpool.Pool
	Cfg  Config
	Log  *slog.Logger
}

// Reconcile is one scheduling pass: drain assignments that no longer belong
// (superseded snapshot targets, delisted/disabled providers), then create
// missing replicas for every eligible target. Deterministic redundancy
// groups (uuid derived from target id) make the pass idempotent.
func (s *Scheduler) Reconcile(ctx context.Context) error {
	cfg := s.Cfg.withDefaults()

	// 1. Drain: mark assignments whose target is not in the published
	// snapshot, or whose provider is no longer eligible.
	drained, err := s.Pool.Exec(ctx, `
		update scheduling.assignment a set status = 'draining'
		where a.status in ('open','leased')
		  and not exists (
		    select 1 from routing.probe_target t
		    join routing.snapshot sn on sn.id = t.snapshot_id and sn.status = 'published'
		    join routing.provider p on p.provider_id = t.provider_id
		    where t.id = a.target_id and t.active
		      and p.monitoring_enabled and p.delisted_at is null)`)
	if err != nil {
		return err
	}
	// Release live leases on draining assignments, then close them.
	if _, err := s.Pool.Exec(ctx, `
		update scheduling.lease l set released_at = now(), release_reason = 'drained'
		from scheduling.assignment a
		where a.id = l.assignment_id and a.status = 'draining' and l.released_at is null`); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `
		update scheduling.assignment set status = 'closed', closed_at = now()
		where status = 'draining'`); err != nil {
		return err
	}

	// 2. Generate: one replica per missing slot, redundancy_group derived
	// deterministically from the target id.
	created, err := s.Pool.Exec(ctx, `
		with eligible as (
		  select t.id as target_id, t.provider_id,
		         greatest($5::int,
		           case when p.priority <= 10 then $2::int
		                when p.priority <= 50 then $3::int
		                else $4::int end) as interval_seconds
		  from routing.probe_target t
		  join routing.snapshot sn on sn.id = t.snapshot_id and sn.status = 'published'
		  join routing.provider p on p.provider_id = t.provider_id
		  where t.active and p.monitoring_enabled and p.delisted_at is null
		), existing as (
		  select target_id, count(*) as n
		  from scheduling.assignment
		  where status in ('open','leased')
		  group by target_id
		)
		insert into scheduling.assignment
		  (target_id, provider_id, probe_type, interval_seconds, redundancy_group)
		select e.target_id, e.provider_id, 'icmp', e.interval_seconds,
		       md5('vapn-group-' || e.target_id)::uuid
		from eligible e
		cross join generate_series(1, $1) as slot
		left join existing x on x.target_id = e.target_id
		where slot > coalesce(x.n, 0)`,
		cfg.Redundancy, cfg.FastInterval, cfg.MidInterval, cfg.SlowInterval, cfg.MinInterval)
	if err != nil {
		return err
	}
	if drained.RowsAffected() > 0 || created.RowsAffected() > 0 {
		s.Log.Info("scheduler reconciled",
			"drained", drained.RowsAffected(), "created", created.RowsAffected())
	}
	return nil
}

// Run reconciles on an interval until ctx is canceled.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.Reconcile(ctx); err != nil {
			s.Log.Error("scheduler reconcile failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Package trust computes worker trust scores (docs/architecture/05 §4).
// Phase 6 skeleton: availability, tenure, and security-event penalties. The
// dominant consensus-agreement component joins in Phase 8 when the
// aggregation pipeline can supply settled windows to score against.
package trust

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Scorer struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

// ComputeAll recomputes trust for every non-retired worker in one statement:
//   - availability: recency of heartbeats (full within 5m, half within 1h)
//   - tenure: slow ramp d/(d+14) over days since approval — new workers start
//     near the floor, capping Sybil value (worth ~0.5 after two weeks)
//   - penalty: 0.1 per security event (bad signature, replay) in the last
//     7 days, capped at 0.5
//
// score = clamp(availability × (0.3 + 0.7 × tenure) − penalty, 0, 1)
func (s *Scorer) ComputeAll(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `
		insert into registry.trust_score (worker_id, score, components, computed_at)
		select id,
		       greatest(0.0, least(1.0, avail * (0.3 + 0.7 * tenure) - penalty)),
		       jsonb_build_object('availability', round(avail::numeric, 3),
		                          'tenure', round(tenure::numeric, 3),
		                          'penalty', round(penalty::numeric, 3)),
		       now()
		from (
		  select w.id,
		    case when w.last_heartbeat_at is null then 0.0
		         when w.last_heartbeat_at > now() - interval '5 minutes' then 1.0
		         when w.last_heartbeat_at > now() - interval '1 hour' then 0.5
		         else 0.0 end as avail,
		    case when w.approved_at is null then 0.0
		         else (extract(epoch from now() - w.approved_at) / 86400.0) /
		              ((extract(epoch from now() - w.approved_at) / 86400.0) + 14.0)
		    end as tenure,
		    least(0.5, 0.1 * (
		      select count(*) from registry.trust_event e
		      where e.worker_id = w.id
		        and e.created_at > now() - interval '7 days'
		        and e.event_type in ('bad_signature', 'replay')
		    )) as penalty
		  from registry.worker w
		  where w.state <> 'retired'
		) scored
		on conflict (worker_id) do update
		  set score = excluded.score,
		      components = excluded.components,
		      computed_at = excluded.computed_at`)
	return err
}

// Run recomputes on an interval until ctx is canceled.
func (s *Scorer) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.ComputeAll(ctx); err != nil {
			s.Log.Error("trust computation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

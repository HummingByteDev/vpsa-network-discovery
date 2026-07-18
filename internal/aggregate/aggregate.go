// Package aggregate computes trust-weighted consensus from raw observations
// and maintains public provider status (docs/architecture/01 §4.3). Public
// truth is always a function of consensus windows — never of raw
// observations — and the posture is conservative: without enough distinct
// workers or responsive targets the verdict is insufficient_data, never a
// guess.
package aggregate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	WindowSeconds  int     // consensus window length (default 300)
	MinWorkers     int     // distinct workers needed for a verdict (default 3)
	HealthyRatio   float64 // reachable fraction ⇒ healthy (default 0.9)
	DegradedRatio  float64 // reachable fraction ⇒ degraded (default 0.5)
	LatencyFactor  float64 // p50 vs 6h baseline ⇒ latency anomaly (default 2.0)
	RawRetention   time.Duration // raw observation retention (default 14d)
	WindowRetention time.Duration // consensus window retention (default 90d)
}

func (c Config) withDefaults() Config {
	if c.WindowSeconds <= 0 {
		c.WindowSeconds = 300
	}
	if c.MinWorkers <= 0 {
		c.MinWorkers = 3
	}
	if c.HealthyRatio <= 0 {
		c.HealthyRatio = 0.9
	}
	if c.DegradedRatio <= 0 {
		c.DegradedRatio = 0.5
	}
	if c.LatencyFactor <= 0 {
		c.LatencyFactor = 2.0
	}
	if c.RawRetention <= 0 {
		c.RawRetention = 14 * 24 * time.Hour
	}
	if c.WindowRetention <= 0 {
		c.WindowRetention = 90 * 24 * time.Hour
	}
	return c
}

type Engine struct {
	Pool *pgxpool.Pool
	Cfg  Config
	Log  *slog.Logger
}

// ComputeWindow settles the consensus window starting at windowStart for
// every provider with observations in it, and records per-worker agreement
// against the settled result. Idempotent per window (unique constraint).
//
// Consensus model: per target, each worker's observations reduce to an
// ok-ratio; workers vote with their trust weight (floor 0.1 so brand-new
// workers count a little); a target is "up" if ≥50% of voting weight saw it
// reachable. Only targets that answered *someone* in the trailing 24 h
// ("responsive targets") count toward the provider verdict — an address that
// never answers ICMP is a non-signal, not an outage.
func (e *Engine) ComputeWindow(ctx context.Context, windowStart time.Time) error {
	cfg := e.Cfg.withDefaults()
	windowEnd := windowStart.Add(time.Duration(cfg.WindowSeconds) * time.Second)

	_, err := e.Pool.Exec(ctx, `
	with obs as (
	  select o.provider_id, o.target, o.worker_id, o.ok, o.rtt_ms
	  from measurements.observation o
	  join registry.worker w on w.id = o.worker_id and w.state = 'active'
	  where o.measured_at >= $1 and o.measured_at < $2
	), responsive as (      -- targets that answered anyone in the trailing 24h
	  select distinct provider_id, target from measurements.observation
	  where measured_at >= $1::timestamptz - interval '24 hours'
	    and measured_at < $2 and ok
	), per_worker_target as (
	  select provider_id, target, worker_id,
	         avg(case when ok then 1.0 else 0.0 end) as ok_ratio,
	         count(*) as n
	  from obs group by 1, 2, 3
	), weighted as (
	  select pwt.*, coalesce(greatest(ts.score, 0.1), 0.1) as weight
	  from per_worker_target pwt
	  left join registry.trust_score ts on ts.worker_id = pwt.worker_id
	), per_target as (
	  select w.provider_id, w.target,
	         sum(w.weight) filter (where w.ok_ratio >= 0.5) / nullif(sum(w.weight), 0) as up_weight,
	         count(distinct w.worker_id) as workers
	  from weighted w
	  join responsive r on r.provider_id = w.provider_id and r.target = w.target
	  group by 1, 2
	), provider_rollup as (
	  select pt.provider_id,
	         count(*) as measured_targets,
	         count(*) filter (where pt.up_weight >= 0.5) as up_targets,
	         max(pt.workers) as worker_count,
	         avg(2 * least(coalesce(pt.up_weight, 0), 1 - coalesce(pt.up_weight, 0))) as dissent
	  from per_target pt group by 1
	), rtt as (
	  select provider_id,
	         percentile_cont(0.5)  within group (order by rtt_ms) as p50,
	         percentile_cont(0.95) within group (order by rtt_ms) as p95,
	         percentile_cont(0.99) within group (order by rtt_ms) as p99,
	         avg(case when ok then 0.0 else 1.0 end) as loss_rate
	  from obs where rtt_ms is not null or not ok group by 1
	)
	insert into aggregation.consensus_window
	  (provider_id, region, probe_type, window_start, window_seconds,
	   verdict, confidence, worker_count, dissent_ratio, loss_rate,
	   rtt_p50, rtt_p95, rtt_p99, detail)
	select pr.provider_id, 'global', 'icmp', $1, $3,
	  case
	    when pr.worker_count < $4 or pr.measured_targets = 0 then 'insufficient_data'
	    when pr.up_targets::float / pr.measured_targets >= $5 then 'healthy'
	    when pr.up_targets::float / pr.measured_targets >= $6 then 'degraded'
	    else 'outage'
	  end,
	  case when pr.worker_count < $4 then 0.0
	       else least(1.0, pr.worker_count::float / ($4 * 2)) * (1 - coalesce(pr.dissent, 0)) end,
	  pr.worker_count, coalesce(pr.dissent, 0), r.loss_rate,
	  r.p50, r.p95, r.p99,
	  jsonb_build_object('measured_targets', pr.measured_targets,
	                     'up_targets', pr.up_targets)
	from provider_rollup pr
	left join rtt r on r.provider_id = pr.provider_id
	on conflict (provider_id, region, probe_type, window_start, window_seconds) do nothing`,
		windowStart, windowEnd, cfg.WindowSeconds, cfg.MinWorkers,
		cfg.HealthyRatio, cfg.DegradedRatio)
	if err != nil {
		return fmt.Errorf("consensus window: %w", err)
	}

	// Per-worker agreement against the settled per-target consensus.
	_, err = e.Pool.Exec(ctx, `
	with obs as (
	  select o.provider_id, o.target, o.worker_id, o.ok
	  from measurements.observation o
	  join registry.worker w on w.id = o.worker_id and w.state in ('active','quarantined')
	  where o.measured_at >= $1 and o.measured_at < $2
	), per_worker_target as (
	  select provider_id, target, worker_id,
	         avg(case when ok then 1.0 else 0.0 end) as ok_ratio
	  from obs group by 1, 2, 3
	), weighted as (
	  select pwt.*, coalesce(greatest(ts.score, 0.1), 0.1) as weight
	  from per_worker_target pwt
	  left join registry.trust_score ts on ts.worker_id = pwt.worker_id
	), consensus as (
	  select provider_id, target,
	         (sum(weight) filter (where ok_ratio >= 0.5) / nullif(sum(weight),0)) >= 0.5 as up
	  from weighted group by 1, 2
	)
	insert into aggregation.worker_agreement (worker_id, window_start, agreement, targets)
	select pwt.worker_id, $1,
	       avg(1 - abs(pwt.ok_ratio - case when c.up then 1.0 else 0.0 end)),
	       count(*)
	from per_worker_target pwt
	join consensus c on c.provider_id = pwt.provider_id and c.target = pwt.target
	group by 1
	on conflict (worker_id, window_start) do nothing`,
		windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("worker agreement: %w", err)
	}
	return nil
}

// RollupStatus refreshes aggregation.provider_status from each provider's
// newest settled window, opens/resolves anomalies on transitions, and queues
// publication outbox entries. Providers with no window keep their last state.
func (e *Engine) RollupStatus(ctx context.Context) error {
	cfg := e.Cfg.withDefaults()
	rows, err := e.Pool.Query(ctx, `
		select distinct on (provider_id)
		  provider_id, verdict, confidence, window_start, loss_rate,
		  rtt_p50, rtt_p95, worker_count, dissent_ratio
		from aggregation.consensus_window
		where region = 'global'
		order by provider_id, window_start desc`)
	if err != nil {
		return err
	}
	type winRow struct {
		provider, verdict                 string
		confidence, dissent               float64
		lossRate, p50, p95                *float64
		workerCount                       int
		windowStart                       time.Time
	}
	var wins []winRow
	for rows.Next() {
		var w winRow
		if err := rows.Scan(&w.provider, &w.verdict, &w.confidence, &w.windowStart,
			&w.lossRate, &w.p50, &w.p95, &w.workerCount, &w.dissent); err != nil {
			rows.Close()
			return err
		}
		wins = append(wins, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, w := range wins {
		var prev string
		err := e.Pool.QueryRow(ctx, `select verdict from aggregation.provider_status
			where provider_id = $1 and region = 'global'`, w.provider).Scan(&prev)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		metrics := map[string]any{
			"loss_rate": w.lossRate, "rtt_p50_ms": w.p50, "rtt_p95_ms": w.p95,
			"worker_count": w.workerCount, "dissent_ratio": w.dissent,
		}
		metricsJSON, _ := json.Marshal(metrics)
		if _, err := e.Pool.Exec(ctx, `
			insert into aggregation.provider_status
			  (provider_id, region, verdict, confidence, since, metrics, updated_at)
			values ($1, 'global', $2, $3, $4, $5, now())
			on conflict (provider_id, region) do update set
			  verdict = excluded.verdict, confidence = excluded.confidence,
			  since = case when aggregation.provider_status.verdict = excluded.verdict
			               then aggregation.provider_status.since else excluded.since end,
			  metrics = excluded.metrics, updated_at = now()`,
			w.provider, w.verdict, w.confidence, w.windowStart, metricsJSON); err != nil {
			return err
		}

		if prev != w.verdict {
			e.handleTransition(ctx, w.provider, prev, w.verdict, w.windowStart)
		}
		// Latency regression: current p50 vs trailing 6h baseline.
		if w.p50 != nil {
			e.checkLatencyAnomaly(ctx, w.provider, *w.p50, w.windowStart, cfg.LatencyFactor)
		}
		// Queue the status document for VPS Advisor (drained in Phase 9).
		payload, _ := json.Marshal(map[string]any{
			"provider_id": w.provider, "as_of": w.windowStart,
			"global": map[string]any{"verdict": w.verdict, "confidence": w.confidence,
				"metrics": metrics},
		})
		if _, err := e.Pool.Exec(ctx, `insert into aggregation.publication_outbox
			(kind, payload) values ('provider_status', $1)`, payload); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) handleTransition(ctx context.Context, provider, prev, verdict string, at time.Time) {
	// Opening: any transition into outage (or degraded from healthy).
	if verdict == "outage" || (verdict == "degraded" && prev == "healthy") {
		kind := "reachability_loss"
		severity := "critical"
		if verdict == "degraded" {
			severity = "warning"
		}
		if _, err := e.Pool.Exec(ctx, `insert into aggregation.anomaly
			(provider_id, kind, region, severity, opened_at, evidence)
			select $1, $2, 'global', $3, $4, jsonb_build_object('from', $5::text, 'to', $6::text)
			where not exists (select 1 from aggregation.anomaly
			  where provider_id = $1 and kind = $2 and resolved_at is null)`,
			provider, kind, severity, at, prev, verdict); err != nil {
			e.Log.Error("anomaly open failed", "error", err)
		}
	}
	// Resolving: back to healthy closes open reachability anomalies.
	if verdict == "healthy" {
		if _, err := e.Pool.Exec(ctx, `update aggregation.anomaly
			set resolved_at = $2
			where provider_id = $1 and kind = 'reachability_loss' and resolved_at is null`,
			provider, at); err != nil {
			e.Log.Error("anomaly resolve failed", "error", err)
		}
	}
}

func (e *Engine) checkLatencyAnomaly(ctx context.Context, provider string, p50 float64, at time.Time, factor float64) {
	var baseline *float64
	if err := e.Pool.QueryRow(ctx, `select percentile_cont(0.5) within group (order by rtt_p50)
		from aggregation.consensus_window
		where provider_id = $1 and region = 'global' and rtt_p50 is not null
		  and window_start >= $2::timestamptz - interval '6 hours' and window_start < $2`,
		provider, at).Scan(&baseline); err != nil || baseline == nil || *baseline <= 0 {
		return
	}
	if p50 >= *baseline*factor {
		if _, err := e.Pool.Exec(ctx, `insert into aggregation.anomaly
			(provider_id, kind, region, severity, opened_at, evidence)
			select $1, 'latency_regression', 'global', 'warning', $2,
			       jsonb_build_object('p50_ms', $3::float, 'baseline_ms', $4::float)
			where not exists (select 1 from aggregation.anomaly
			  where provider_id = $1 and kind = 'latency_regression' and resolved_at is null)`,
			provider, at, p50, *baseline); err != nil {
			e.Log.Error("latency anomaly failed", "error", err)
		}
	} else if p50 < *baseline*1.2 {
		_, _ = e.Pool.Exec(ctx, `update aggregation.anomaly set resolved_at = $2
			where provider_id = $1 and kind = 'latency_regression' and resolved_at is null`,
			provider, at)
	}
}

// Retention prunes raw observations and old windows/agreement rows.
func (e *Engine) Retention(ctx context.Context) error {
	cfg := e.Cfg.withDefaults()
	if _, err := e.Pool.Exec(ctx, `delete from measurements.observation
		where measured_at < now() - $1`, cfg.RawRetention); err != nil {
		return err
	}
	if _, err := e.Pool.Exec(ctx, `delete from aggregation.consensus_window
		where window_start < now() - $1`, cfg.WindowRetention); err != nil {
		return err
	}
	if _, err := e.Pool.Exec(ctx, `delete from aggregation.worker_agreement
		where window_start < now() - interval '30 days'`); err != nil {
		return err
	}
	if _, err := e.Pool.Exec(ctx, `delete from measurements.upload_batch
		where received_at < now() - interval '7 days'`); err != nil {
		return err
	}
	return nil
}

// Run drives the pipeline: every WindowSeconds, settle the just-completed
// window, roll up status, and periodically apply retention.
func (e *Engine) Run(ctx context.Context) {
	cfg := e.Cfg.withDefaults()
	windowDur := time.Duration(cfg.WindowSeconds) * time.Second
	ticker := time.NewTicker(windowDur)
	defer ticker.Stop()
	retention := time.NewTicker(6 * time.Hour)
	defer retention.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			start := time.Now().UTC().Add(-windowDur).Truncate(windowDur)
			if err := e.ComputeWindow(ctx, start); err != nil {
				e.Log.Error("window computation failed", "error", err)
				continue
			}
			if err := e.RollupStatus(ctx); err != nil {
				e.Log.Error("status rollup failed", "error", err)
			}
		case <-retention.C:
			if err := e.Retention(ctx); err != nil {
				e.Log.Error("retention failed", "error", err)
			}
		}
	}
}

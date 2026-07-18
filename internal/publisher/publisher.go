// Package publisher drains the aggregation outbox to VPS Advisor's Results
// API with at-least-once delivery: rows are acked only after a successful
// push, failures back off exponentially, and payload documents are
// idempotent upserts on the receiving side.
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/metrics"
)

type Publisher struct {
	Pool   *pgxpool.Pool
	Client *advisor.Client
	Log    *slog.Logger
}

// DrainOnce pushes a batch of unacked, due outbox rows. Row-level locking
// (SKIP LOCKED) makes concurrent publishers safe.
func (p *Publisher) DrainOnce(ctx context.Context) (pushed int, err error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `select id, kind, payload
		from aggregation.publication_outbox
		where acked_at is null and next_attempt_at <= now()
		order by id for update skip locked limit 100`)
	if err != nil {
		return 0, err
	}
	type item struct {
		id      int64
		kind    string
		payload []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.kind, &it.payload); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, it := range items {
		pushErr := p.push(ctx, it.kind, it.payload)
		if pushErr == nil {
			metrics.OutboxPush.WithLabelValues(it.kind, "ok").Inc()
			if _, err := tx.Exec(ctx, `update aggregation.publication_outbox
				set acked_at = now(), attempts = attempts + 1 where id = $1`, it.id); err != nil {
				return pushed, err
			}
			pushed++
			continue
		}
		metrics.OutboxPush.WithLabelValues(it.kind, "error").Inc()
		p.Log.Warn("publication failed; backing off", "kind", it.kind, "id", it.id, "error", pushErr)
		if _, err := tx.Exec(ctx, `update aggregation.publication_outbox
			set attempts = attempts + 1,
			    next_attempt_at = now() + least(power(2, attempts) * interval '1 second', interval '5 minutes')
			where id = $1`, it.id); err != nil {
			return pushed, err
		}
	}
	return pushed, tx.Commit(ctx)
}

func (p *Publisher) push(ctx context.Context, kind string, payload []byte) error {
	switch kind {
	case "provider_status":
		var doc struct {
			ProviderID string `json:"provider_id"`
		}
		if err := json.Unmarshal(payload, &doc); err != nil || doc.ProviderID == "" {
			return fmt.Errorf("malformed provider_status payload: %v", err)
		}
		return p.Client.PutProviderStatus(ctx, doc.ProviderID, payload)
	case "anomaly":
		return p.Client.PostAnomaly(ctx, payload)
	case "telemetry":
		return p.Client.PostFleetTelemetry(ctx, payload)
	default:
		return fmt.Errorf("unknown outbox kind %q", kind)
	}
}

// EnqueueFleetTelemetry snapshots fleet health into the outbox: worker counts
// by state, software versions, the published snapshot, and recent security
// event counts — the admin dashboard's data feed.
func (p *Publisher) EnqueueFleetTelemetry(ctx context.Context) error {
	var doc []byte
	err := p.Pool.QueryRow(ctx, `
		select jsonb_build_object(
		  'as_of', now(),
		  'workers_by_state', (select coalesce(jsonb_object_agg(state, n), '{}'::jsonb)
		     from (select state, count(*) n from registry.worker group by 1) s),
		  'software_versions', (select coalesce(jsonb_object_agg(v, n), '{}'::jsonb)
		     from (select coalesce(software_version,'unknown') v, count(*) n
		           from registry.worker where state = 'active' group by 1) s),
		  'published_snapshot', (select version from routing.snapshot
		     where status = 'published' order by id desc limit 1),
		  'security_events_24h', (select coalesce(jsonb_object_agg(event_type, n), '{}'::jsonb)
		     from (select event_type, count(*) n from registry.trust_event
		           where created_at > now() - interval '24 hours'
		             and event_type in ('bad_signature','replay') group by 1) s)
		)::text`).Scan(&doc)
	if err != nil {
		return err
	}
	_, err = p.Pool.Exec(ctx, `insert into aggregation.publication_outbox (kind, payload)
		values ('telemetry', $1)`, doc)
	return err
}

// Run drains continuously and enqueues fleet telemetry periodically.
func (p *Publisher) Run(ctx context.Context, drainInterval, telemetryInterval time.Duration) {
	drain := time.NewTicker(drainInterval)
	defer drain.Stop()
	telemetry := time.NewTicker(telemetryInterval)
	defer telemetry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-drain.C:
			if n, err := p.DrainOnce(ctx); err != nil {
				p.Log.Error("outbox drain failed", "error", err)
			} else if n > 0 {
				p.Log.Info("published to VPS Advisor", "documents", n)
			}
		case <-telemetry.C:
			if err := p.EnqueueFleetTelemetry(ctx); err != nil {
				p.Log.Error("telemetry enqueue failed", "error", err)
			}
		}
	}
}

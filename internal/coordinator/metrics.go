package coordinator

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// DBStateCollector derives fleet gauges from the database at scrape time, so
// /metrics always reflects reality without a background refresher: workers by
// state, assignment/lease counts, outbox depth, and published snapshot age.
type DBStateCollector struct {
	Pool *pgxpool.Pool

	workers   *prometheus.Desc
	open      *prometheus.Desc
	leases    *prometheus.Desc
	outbox    *prometheus.Desc
	snapAge   *prometheus.Desc
	snapCount *prometheus.Desc
}

func NewDBStateCollector(pool *pgxpool.Pool) *DBStateCollector {
	return &DBStateCollector{
		Pool: pool,
		workers: prometheus.NewDesc("vapn_workers",
			"Workers by lifecycle state.", []string{"state"}, nil),
		open: prometheus.NewDesc("vapn_assignments_active",
			"Assignments in open or leased status.", nil, nil),
		leases: prometheus.NewDesc("vapn_leases_live",
			"Unreleased leases.", nil, nil),
		outbox: prometheus.NewDesc("vapn_outbox_queued",
			"Publication outbox entries not yet acknowledged by VPS Advisor.", nil, nil),
		snapAge: prometheus.NewDesc("vapn_snapshot_age_seconds",
			"Age of the published routing snapshot.", nil, nil),
		snapCount: prometheus.NewDesc("vapn_snapshot_targets",
			"Probe targets in the published snapshot.", nil, nil),
	}
}

func (c *DBStateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.workers
	ch <- c.open
	ch <- c.leases
	ch <- c.outbox
	ch <- c.snapAge
	ch <- c.snapCount
}

func (c *DBStateCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := c.Pool.Query(ctx, `select state, count(*) from registry.worker group by 1`)
	if err == nil {
		for rows.Next() {
			var state string
			var n float64
			if err := rows.Scan(&state, &n); err == nil {
				ch <- prometheus.MustNewConstMetric(c.workers, prometheus.GaugeValue, n, state)
			}
		}
		rows.Close()
	}

	var open, leases, outbox float64
	if err := c.Pool.QueryRow(ctx, `select
		(select count(*) from scheduling.assignment where status in ('open','leased')),
		(select count(*) from scheduling.lease where released_at is null),
		(select count(*) from aggregation.publication_outbox where acked_at is null)`).
		Scan(&open, &leases, &outbox); err == nil {
		ch <- prometheus.MustNewConstMetric(c.open, prometheus.GaugeValue, open)
		ch <- prometheus.MustNewConstMetric(c.leases, prometheus.GaugeValue, leases)
		ch <- prometheus.MustNewConstMetric(c.outbox, prometheus.GaugeValue, outbox)
	}

	var publishedAt time.Time
	var targets float64
	if err := c.Pool.QueryRow(ctx, `
		select s.published_at,
		       (select count(*) from routing.probe_target t where t.snapshot_id = s.id)
		from routing.snapshot s where s.status = 'published'
		order by s.published_at desc limit 1`).Scan(&publishedAt, &targets); err == nil {
		ch <- prometheus.MustNewConstMetric(c.snapAge, prometheus.GaugeValue,
			time.Since(publishedAt).Seconds())
		ch <- prometheus.MustNewConstMetric(c.snapCount, prometheus.GaugeValue, targets)
	}
}

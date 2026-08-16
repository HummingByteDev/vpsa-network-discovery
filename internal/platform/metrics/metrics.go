// Package metrics defines the platform's Prometheus instruments. Each binary
// exposes the default registry on /metrics (see platform/httpserver); only
// the instruments a process actually touches will appear in its scrape.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP layer (all components; labels route pattern and status class).
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vapn_http_requests_total",
		Help: "HTTP requests by route pattern and status code.",
	}, []string{"route", "code"})
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vapn_http_request_duration_seconds",
		Help:    "HTTP request latency by route pattern.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"route"})

	// Coordinator.
	ObservationsIngested = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vapn_observations_ingested_total",
		Help: "Signed observations accepted into measurements storage.",
	})
	ObservationsRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vapn_observations_rejected_total",
		Help: "Observations rejected (bad signature, unknown assignment, ...).",
	})
	LeaseRequests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vapn_lease_requests_total",
		Help: "Assignment lease calls served.",
	})
	TrustEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vapn_trust_events_total",
		Help: "Security/trust events recorded, by type.",
	}, []string{"type"})

	// VPS Advisor synchronization (coordinator). A worker approved on the
	// website only becomes active here once the decisions feed succeeds, so a
	// stalled `last_success` on that feed is the signal behind "the site says
	// approved and the worker says pending".
	AdvisorSync = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vapn_advisor_sync_total",
		Help: "VPS Advisor synchronization passes, by feed and outcome.",
	}, []string{"feed", "outcome"})
	AdvisorSyncLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vapn_advisor_sync_last_success_timestamp_seconds",
		Help: "Unix time of the last successful VPS Advisor sync, by feed.",
	}, []string{"feed"})

	// Aggregator.
	WindowsComputed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vapn_consensus_windows_total",
		Help: "Consensus windows computed.",
	})
	OutboxPush = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vapn_outbox_push_total",
		Help: "Publication outbox pushes to VPS Advisor, by kind and outcome.",
	}, []string{"kind", "outcome"})
)

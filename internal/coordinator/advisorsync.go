package coordinator

import (
	"context"
	"sync"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/metrics"
)

// advisorHealth records how each VPS Advisor feed is faring. The decision feed
// is the one an operator feels: a worker approved on the website stays
// `pending` here until a pass of that feed succeeds, and before this existed
// the only trace of a failing pull was a warning in the log. It is surfaced on
// the admin overview and in Prometheus so "the website says approved, the
// worker says pending" is answerable without reading logs.
type advisorHealth struct {
	mu    sync.Mutex
	feeds map[string]*feedHealth
}

type feedHealth struct {
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastError     string     `json:"last_error,omitempty"`
	FailureStreak int        `json:"consecutive_failures"`
}

func (h *advisorHealth) record(feed string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.feeds == nil {
		h.feeds = map[string]*feedHealth{}
	}
	f := h.feeds[feed]
	if f == nil {
		f = &feedHealth{}
		h.feeds[feed] = f
	}
	now := time.Now().UTC()
	f.LastAttemptAt = now
	if err != nil {
		f.LastError = err.Error()
		f.FailureStreak++
		metrics.AdvisorSync.WithLabelValues(feed, "error").Inc()
		return
	}
	f.LastError = ""
	f.FailureStreak = 0
	stamp := now
	f.LastSuccessAt = &stamp
	metrics.AdvisorSync.WithLabelValues(feed, "ok").Inc()
	metrics.AdvisorSyncLastSuccess.WithLabelValues(feed).Set(float64(now.Unix()))
}

// snapshot copies the current health for rendering.
func (h *advisorHealth) snapshot() map[string]feedHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]feedHealth, len(h.feeds))
	for name, f := range h.feeds {
		out[name] = *f
	}
	return out
}

// AdvisorHealth reports the state of each VPS Advisor feed (admin surface).
func (s *Server) AdvisorHealth() map[string]feedHealth { return s.advisor.snapshot() }

// SyncAdvisor performs one synchronization pass against VPS Advisor:
//  1. provider catalog → local cache (opt-outs drain via the scheduler)
//  2. pending enrollments → provisioned workers + token hashes
//  3. admin decisions → lifecycle transitions
//
// Every step is idempotent; VPS Advisor remains the source of truth.
//
// The returned cursor is derived from the newest `decided_at` the website
// actually returned, never from this host's clock. A local-clock cursor is
// only correct while the two machines agree to within the poll interval:
// running even slightly ahead of the website silently drops every decision
// recorded inside the difference, which presents exactly as an approved worker
// that never leaves `pending`.
func (s *Server) SyncAdvisor(ctx context.Context, since time.Time) time.Time {
	c := s.cfg.AdvisorClient
	if c == nil {
		return since
	}

	_, err := advisor.SyncProviders(ctx, s.reg.Pool, c)
	s.advisor.record("providers", err)
	if err != nil {
		s.log.Error("provider sync failed", "advisor_url", c.BaseURL(), "error", err)
	}

	enrollments, err := c.ListPendingEnrollments(ctx)
	s.advisor.record("enrollments", err)
	if err != nil {
		s.log.Error("enrollment sync failed", "advisor_url", c.BaseURL(), "error", err)
	}
	for _, e := range enrollments {
		created, err := s.reg.IngestEnrollment(ctx, e.WorkerID, e.OperatorID, e.WorkerName,
			e.TokenHash, e.ExpiresAt)
		if err != nil {
			s.log.Warn("enrollment ingest failed", "enrollment", e.EnrollmentID, "error", err)
			continue
		}
		if created {
			s.log.Info("enrollment provisioned", "worker", e.WorkerID, "operator", e.OperatorID)
			if s.audit != nil {
				s.audit.Event(ctx, "auth", "advisor", "enrollment_provisioned", e.WorkerID, nil)
			}
		}
		// Acknowledge on every successful ingest, not only the first. The row
		// is already provisioned either way, and the ack is what drops the
		// enrollment out of the pending feed — gating it on `created` left a
		// re-issued token (regenerate, or a second enrollment for a worker we
		// already hold) being re-ingested on every pass, forever.
		if err := c.MarkEnrollmentRegistered(ctx, e.EnrollmentID); err != nil {
			s.log.Warn("enrollment ack failed", "enrollment", e.EnrollmentID, "error", err)
		}
	}

	decisions, err := c.ListDecisions(ctx, since)
	s.advisor.record("decisions", err)
	if err != nil {
		s.log.Error("decision sync failed — workers approved on VPS Advisor will stay pending",
			"advisor_url", c.BaseURL(), "error", err)
		s.warnPendingStranded(ctx)
		return since
	}
	cursor := since
	for _, d := range decisions {
		if err := s.reg.ApplyDecision(ctx, d.WorkerID, d.State, d.Reason); err != nil {
			s.log.Warn("decision apply failed", "worker", d.WorkerID, "state", d.State, "error", err)
			continue
		}
		s.log.Info("advisor decision applied", "worker", d.WorkerID, "state", d.State)
		if s.audit != nil {
			s.audit.Event(ctx, "admin", "advisor-admin", "decision:"+d.State, d.WorkerID,
				map[string]string{"reason": d.Reason})
		}
		// Advance only past decisions we have actually applied, and only using
		// the website's own timestamp.
		if d.DecidedAt.After(cursor) {
			cursor = d.DecidedAt
		}
	}
	return cursor
}

// warnPendingStranded turns a failing decision feed into the sentence an
// operator is actually looking for when a worker they approved keeps logging
// "awaiting approval".
func (s *Server) warnPendingStranded(ctx context.Context) {
	var pending int
	if err := s.reg.Pool.QueryRow(ctx,
		`select count(*) from registry.worker where state = 'pending'`).Scan(&pending); err != nil {
		return
	}
	if pending > 0 {
		s.log.Error("workers are waiting on approvals that cannot be fetched",
			"pending_workers", pending,
			"hint", "VPS Advisor decisions are unreachable; check VAPN_ADVISOR_URL and VAPN_ADVISOR_TOKEN")
	}
}

// StartAdvisorSync runs SyncAdvisor on an interval until ctx is canceled.
//
// The first pass asks for the whole decision feed (a zero cursor) rather than
// a window off the local clock: decisions are applied idempotently, so a full
// replay is a no-op for everything already in force, and it means a coordinator
// that was down while approvals happened catches up on start instead of
// silently skipping them.
func (s *Server) StartAdvisorSync(ctx context.Context, interval time.Duration) {
	c := s.cfg.AdvisorClient
	if c == nil {
		return
	}
	if err := c.Validate(); err != nil {
		s.log.Error("VPS Advisor URL is misconfigured; provider, enrollment and "+
			"approval sync will all fail", "advisor_url", c.BaseURL(), "error", err)
	}
	go func() {
		if err := c.Ping(ctx); err != nil {
			s.log.Error("VPS Advisor is not answering the monitoring API; "+
				"approvals will not reach this coordinator",
				"advisor_url", c.BaseURL(), "error", err)
		} else {
			s.log.Info("VPS Advisor reachable", "advisor_url", c.BaseURL())
		}
		var since time.Time // zero: full feed on the first pass
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		since = s.SyncAdvisor(ctx, since)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				since = s.SyncAdvisor(ctx, since)
			}
		}
	}()
}

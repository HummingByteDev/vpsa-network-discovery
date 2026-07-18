package coordinator

import (
	"context"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
)

// SyncAdvisor performs one synchronization pass against VPS Advisor:
//  1. provider catalog → local cache (opt-outs drain via the scheduler)
//  2. pending enrollments → provisioned workers + token hashes
//  3. admin decisions → lifecycle transitions
//
// Every step is idempotent; VPS Advisor remains the source of truth.
func (s *Server) SyncAdvisor(ctx context.Context, since time.Time) time.Time {
	c := s.cfg.AdvisorClient
	if c == nil {
		return since
	}
	if _, err := advisor.SyncProviders(ctx, s.reg.Pool, c); err != nil {
		s.log.Warn("provider sync failed", "error", err)
	}

	enrollments, err := c.ListPendingEnrollments(ctx)
	if err != nil {
		s.log.Warn("enrollment sync failed", "error", err)
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
			if err := c.MarkEnrollmentRegistered(ctx, e.EnrollmentID); err != nil {
				s.log.Warn("enrollment ack failed", "enrollment", e.EnrollmentID, "error", err)
			}
		}
	}

	now := time.Now().UTC()
	decisions, err := c.ListDecisions(ctx, since)
	if err != nil {
		s.log.Warn("decision sync failed", "error", err)
		return since
	}
	for _, d := range decisions {
		if err := s.reg.ApplyDecision(ctx, d.WorkerID, d.State, d.Reason); err != nil {
			s.log.Warn("decision apply failed", "worker", d.WorkerID, "state", d.State, "error", err)
			continue
		}
		if s.audit != nil {
			s.audit.Event(ctx, "admin", "advisor-admin", "decision:"+d.State, d.WorkerID,
				map[string]string{"reason": d.Reason})
		}
	}
	return now
}

// StartAdvisorSync runs SyncAdvisor on an interval until ctx is canceled.
func (s *Server) StartAdvisorSync(ctx context.Context, interval time.Duration) {
	if s.cfg.AdvisorClient == nil {
		return
	}
	go func() {
		since := time.Now().UTC().Add(-24 * time.Hour)
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

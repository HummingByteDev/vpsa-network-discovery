package coordinator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/observation"
)

// The scheduler (internal/scheduler) generates redundant assignments; this
// endpoint distributes them: renew, reap expired fleet-wide, then claim under
// the diversity and self-network rules.

type leaseRequest struct {
	Capacity int `json:"capacity"`
}

type leasedAssignment struct {
	ID              int64           `json:"assignment_id"`
	Target          string          `json:"target"`
	ProbeType       string          `json:"probe_type"`
	IntervalSeconds int             `json:"interval_seconds"`
	Params          json.RawMessage `json:"params,omitempty"`
}

func (s *Server) leaseAssignments(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Capacity <= 0 {
		problem(w, http.StatusBadRequest, "capacity must be a positive integer")
		return
	}
	if s.paused.Load() {
		writeJSON(w, http.StatusOK, map[string]any{"assignments": []leasedAssignment{}, "paused": true})
		return
	}
	if max := s.cfg.MaxAssignmentsPerWorker; max > 0 && req.Capacity > max {
		req.Capacity = max
	}
	workerID := identity(r).ID
	ctx := r.Context()
	tx, err := s.reg.Pool.Begin(ctx)
	if err != nil {
		problem(w, http.StatusInternalServerError, "lease failed")
		return
	}
	defer tx.Rollback(ctx)

	// Renew this worker's live leases.
	if _, err := tx.Exec(ctx, `update scheduling.lease set expires_at = now() + $2
		where worker_id = $1 and released_at is null`, workerID, s.leaseTTL()); err != nil {
		problem(w, http.StatusInternalServerError, "lease failed")
		return
	}
	// Reap expired leases fleet-wide so their assignments reopen.
	if _, err := tx.Exec(ctx, `with expired as (
			update scheduling.lease set released_at = now(), release_reason = 'expired'
			where released_at is null and expires_at < now()
			returning assignment_id)
		update scheduling.assignment a set status = 'open'
		from expired where a.id = expired.assignment_id and a.status = 'leased'`); err != nil {
		problem(w, http.StatusInternalServerError, "lease failed")
		return
	}
	// Claim open assignments up to capacity.
	var held int
	if err := tx.QueryRow(ctx, `select count(*) from scheduling.lease
		where worker_id = $1 and released_at is null`, workerID).Scan(&held); err != nil {
		problem(w, http.StatusInternalServerError, "lease failed")
		return
	}
	if want := req.Capacity - held; want > 0 {
		// Claim rules: never two replicas of one redundancy group on the same
		// worker; never a target inside the worker's own network (self-ASN
		// exclusion); SKIP LOCKED keeps concurrent claimants from colliding.
		if _, err := tx.Exec(ctx, `with me as (
				select source_asn from registry.worker where id = $1
			), mine as (
				select distinct a.redundancy_group
				from scheduling.lease l
				join scheduling.assignment a on a.id = l.assignment_id
				where l.worker_id = $1 and l.released_at is null
			), claimed as (
				select a.id from scheduling.assignment a, me
				where a.status = 'open'
				  and not exists (select 1 from mine m where m.redundancy_group = a.redundancy_group)
				  and not exists (
				    select 1 from routing.asn ra
				    where ra.provider_id = a.provider_id and ra.asn = me.source_asn)
				order by a.id
				for update skip locked limit $2
			), marked as (
				update scheduling.assignment a set status = 'leased'
				from claimed where a.id = claimed.id returning a.id)
			insert into scheduling.lease (assignment_id, worker_id, expires_at)
			select id, $1, now() + $3 from marked`,
			workerID, want, s.leaseTTL()); err != nil {
			problem(w, http.StatusInternalServerError, "lease failed")
			return
		}
	}
	rows, err := tx.Query(ctx, `
		select a.id, host(t.address), a.probe_type, a.interval_seconds, a.params
		from scheduling.lease l
		join scheduling.assignment a on a.id = l.assignment_id
		join routing.probe_target t on t.id = a.target_id
		where l.worker_id = $1 and l.released_at is null and a.status = 'leased'`,
		workerID)
	if err != nil {
		problem(w, http.StatusInternalServerError, "lease failed")
		return
	}
	out := []leasedAssignment{}
	for rows.Next() {
		var a leasedAssignment
		if err := rows.Scan(&a.ID, &a.Target, &a.ProbeType, &a.IntervalSeconds, &a.Params); err != nil {
			rows.Close()
			problem(w, http.StatusInternalServerError, "lease failed")
			return
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil || tx.Commit(ctx) != nil {
		problem(w, http.StatusInternalServerError, "lease failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"assignments": out})
}

func (s *Server) releaseAssignments(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AssignmentIDs []int64 `json:"assignment_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx := r.Context()
	if _, err := s.reg.Pool.Exec(ctx, `with released as (
			update scheduling.lease set released_at = now(), release_reason = 'released by worker'
			where worker_id = $1 and released_at is null and assignment_id = any($2)
			returning assignment_id)
		update scheduling.assignment a set status = 'open'
		from released where a.id = released.assignment_id and a.status = 'leased'`,
		identity(r).ID, req.AssignmentIDs); err != nil {
		problem(w, http.StatusInternalServerError, "release failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type uploadRequest struct {
	BatchID      string                    `json:"batch_id"`
	Observations []observation.Observation `json:"observations"`
}

func (s *Server) uploadObservations(w http.ResponseWriter, r *http.Request) {
	var req uploadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.BatchID == "" || len(req.Observations) == 0 {
		problem(w, http.StatusUnprocessableEntity, "batch_id and observations are required")
		return
	}
	workerID := identity(r).ID
	ctx := r.Context()
	keys, _, err := s.reg.ActiveKeys(ctx, workerID)
	if err != nil {
		problem(w, http.StatusUnauthorized, "no active key")
		return
	}
	verifyObs := func(o observation.Observation) bool {
		for _, pub := range keys {
			if observation.Verify(o, pub) == nil {
				return true
			}
		}
		return false
	}

	// Idempotency: a known batch is acknowledged, never re-inserted.
	tx, err := s.reg.Pool.Begin(ctx)
	if err != nil {
		problem(w, http.StatusInternalServerError, "upload failed")
		return
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `insert into measurements.upload_batch
		(batch_id, worker_id, observation_count) values ($1, $2, $3)
		on conflict (batch_id) do nothing`,
		req.BatchID, workerID, len(req.Observations))
	if err != nil {
		problem(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if ct.RowsAffected() == 0 {
		_ = tx.Commit(ctx)
		writeJSON(w, http.StatusOK, map[string]any{"accepted": 0, "duplicate": true})
		return
	}

	// Per-observation validation: signature, sane target, held assignment
	// (which also yields the provider the measurement belongs to).
	accepted := make([]observation.Observation, 0, len(req.Observations))
	rejected := 0
	heldRows, err := tx.Query(ctx, `select l.assignment_id, a.provider_id::text
		from scheduling.lease l
		join scheduling.assignment a on a.id = l.assignment_id
		where l.worker_id = $1 and l.released_at is null`, workerID)
	if err != nil {
		problem(w, http.StatusInternalServerError, "upload failed")
		return
	}
	held := map[int64]string{}
	for heldRows.Next() {
		var id int64
		var provider string
		if err := heldRows.Scan(&id, &provider); err != nil {
			heldRows.Close()
			problem(w, http.StatusInternalServerError, "upload failed")
			return
		}
		held[id] = provider
	}
	heldRows.Close()
	providers := make([]string, 0, len(req.Observations))
	for _, obs := range req.Observations {
		if !verifyObs(obs) {
			rejected++
			continue
		}
		if _, err := netip.ParseAddr(obs.Target); err != nil {
			rejected++
			continue
		}
		provider, ok := held[obs.AssignmentID]
		if !ok {
			rejected++
			continue
		}
		accepted = append(accepted, obs)
		providers = append(providers, provider)
	}
	if rejected > 0 {
		s.log.Warn("observations rejected in batch", "worker", workerID,
			"batch", req.BatchID, "rejected", rejected)
	}

	if len(accepted) > 0 {
		_, err = tx.CopyFrom(ctx,
			pgx.Identifier{"measurements", "observation"},
			[]string{"worker_id", "assignment_id", "provider_id", "target", "probe_type",
				"measured_at", "ok", "rtt_ms", "packets_sent", "packets_lost", "metrics", "signature"},
			pgx.CopyFromSlice(len(accepted), func(i int) ([]any, error) {
				o := accepted[i]
				metrics, err := json.Marshal(o.Metrics)
				if err != nil {
					return nil, err
				}
				var rtt any
				if o.RTTMillis != nil {
					rtt = *o.RTTMillis
				}
				return []any{workerID, o.AssignmentID, providers[i], o.Target, o.ProbeType,
					o.MeasuredAt, o.OK, rtt, o.PacketsSent, o.PacketsLost, metrics, []byte(o.Signature)}, nil
			}))
		if err != nil {
			s.log.Error("observation insert failed", "error", err)
			problem(w, http.StatusInternalServerError, "upload failed")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		problem(w, http.StatusInternalServerError, "upload failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": len(accepted), "rejected": rejected,
	})
}

// Admin surface beyond worker management: fleet overview, snapshot
// list/rollback, and audit queries. Everything here sits behind the admin
// bearer token and is consumed by vapnctl (and, later, dashboards).
package coordinator

import (
	"net/http"
	"strconv"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
)

type fleetOverview struct {
	WorkersByState   map[string]int    `json:"workers_by_state"`
	SoftwareVersions map[string]int    `json:"software_versions"`
	Snapshot         *overviewSnapshot `json:"published_snapshot"`
	OpenAssignments  int               `json:"open_assignments"`
	LiveLeases       int               `json:"live_leases"`
	OutboxQueued     int               `json:"outbox_queued"`
	SecurityEvents24 map[string]int    `json:"security_events_24h"`
	SchedulerPaused  bool              `json:"scheduler_paused"`
	// AdvisorSync is per-feed health of the VPS Advisor pull. Workers approved
	// on the website reach `active` only through the `decisions` feed, so this
	// is the first thing to read when a worker is stuck pending.
	AdvisorSync map[string]feedHealth `json:"advisor_sync,omitempty"`
}

type overviewSnapshot struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	Targets     int       `json:"targets"`
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := fleetOverview{
		WorkersByState:   map[string]int{},
		SoftwareVersions: map[string]int{},
		SecurityEvents24: map[string]int{},
		SchedulerPaused:  s.paused.Load(),
		AdvisorSync:      s.AdvisorHealth(),
	}
	rows, err := s.reg.Pool.Query(ctx, `select state, count(*) from registry.worker group by 1`)
	if err != nil {
		problem(w, http.StatusInternalServerError, "overview failed")
		return
	}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err == nil {
			out.WorkersByState[state] = n
		}
	}
	rows.Close()
	rows, err = s.reg.Pool.Query(ctx, `select coalesce(software_version,'unknown'), count(*)
		from registry.worker where state = 'active' group by 1`)
	if err == nil {
		for rows.Next() {
			var v string
			var n int
			if err := rows.Scan(&v, &n); err == nil {
				out.SoftwareVersions[v] = n
			}
		}
		rows.Close()
	}
	var snap overviewSnapshot
	err = s.reg.Pool.QueryRow(ctx, `
		select s.version, s.published_at,
		       (select count(*) from routing.probe_target t where t.snapshot_id = s.id)
		from routing.snapshot s where s.status = 'published'
		order by s.published_at desc limit 1`).Scan(&snap.Version, &snap.PublishedAt, &snap.Targets)
	if err == nil {
		out.Snapshot = &snap
	}
	_ = s.reg.Pool.QueryRow(ctx, `select
		(select count(*) from scheduling.assignment where status in ('open','leased')),
		(select count(*) from scheduling.lease where released_at is null),
		(select count(*) from aggregation.publication_outbox where acked_at is null)`).
		Scan(&out.OpenAssignments, &out.LiveLeases, &out.OutboxQueued)
	rows, err = s.reg.Pool.Query(ctx, `select event_type, count(*) from registry.trust_event
		where created_at > now() - interval '24 hours' group by 1`)
	if err == nil {
		for rows.Next() {
			var kind string
			var n int
			if err := rows.Scan(&kind, &n); err == nil {
				out.SecurityEvents24[kind] = n
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, out)
}

type snapshotSummary struct {
	Version       string     `json:"version"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at"`
	PrefixCountV4 int        `json:"prefix_count_v4"`
	PrefixCountV6 int        `json:"prefix_count_v6"`
	HasData       bool       `json:"has_data"` // false once pruned: not a rollback candidate
}

func (s *Server) adminListSnapshots(w http.ResponseWriter, r *http.Request) {
	rows, err := s.reg.Pool.Query(r.Context(), `
		select version, status, created_at, published_at,
		       coalesce(prefix_count_v4,0), coalesce(prefix_count_v6,0),
		       exists (select 1 from routing.prefix p where p.snapshot_id = s.id)
		from routing.snapshot s order by id desc limit 50`)
	if err != nil {
		problem(w, http.StatusInternalServerError, "snapshot list failed")
		return
	}
	defer rows.Close()
	out := []snapshotSummary{}
	for rows.Next() {
		var sn snapshotSummary
		if err := rows.Scan(&sn.Version, &sn.Status, &sn.CreatedAt, &sn.PublishedAt,
			&sn.PrefixCountV4, &sn.PrefixCountV6, &sn.HasData); err != nil {
			problem(w, http.StatusInternalServerError, "snapshot list failed")
			return
		}
		out = append(out, sn)
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": out})
}

// adminRollbackSnapshot re-publishes a previous snapshot. Workers converge on
// their next heartbeat (they accept version changes in either direction, with
// a warning on downgrade). Rollback needs no signing key: the target
// version's manifest in the store already carries a valid signature.
func (s *Server) adminRollbackSnapshot(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")
	p := &artifact.Publisher{Pool: s.reg.Pool, Store: s.store, Log: s.log}
	if err := p.RollbackTo(r.Context(), version); err != nil {
		problem(w, http.StatusConflict, err.Error())
		return
	}
	s.invalidateManifestCache()
	s.audit.Event(r.Context(), "admin", "admin", "snapshot_rollback", version, nil)
	writeJSON(w, http.StatusOK, map[string]string{"published": version})
}

type auditEvent struct {
	ID        int64          `json:"id"`
	Category  string         `json:"category"`
	Actor     string         `json:"actor"`
	Action    string         `json:"action"`
	Subject   *string        `json:"subject"`
	Detail    map[string]any `json:"detail"`
	CreatedAt time.Time      `json:"created_at"`
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 1000 {
		limit = n
	}
	since := time.Now().Add(-24 * time.Hour)
	if t, err := time.Parse(time.RFC3339, r.URL.Query().Get("since")); err == nil {
		since = t
	}
	category := r.URL.Query().Get("category")
	rows, err := s.reg.Pool.Query(r.Context(), `
		select id, category, actor, action, subject, detail, created_at
		from audit.event
		where created_at > $1 and ($2 = '' or category = $2)
		order by id desc limit $3`, since, category, limit)
	if err != nil {
		problem(w, http.StatusInternalServerError, "audit query failed")
		return
	}
	defer rows.Close()
	out := []auditEvent{}
	for rows.Next() {
		var e auditEvent
		if err := rows.Scan(&e.ID, &e.Category, &e.Actor, &e.Action, &e.Subject,
			&e.Detail, &e.CreatedAt); err != nil {
			problem(w, http.StatusInternalServerError, "audit query failed")
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

type workerDetail struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	State           string         `json:"state"`
	StateReason     *string        `json:"state_reason"`
	SoftwareVersion *string        `json:"software_version"`
	SourceASN       *int64         `json:"source_asn"`
	EnrolledAt      time.Time      `json:"enrolled_at"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at"`
	TrustScore      *float64       `json:"trust_score"`
	TrustComponents map[string]any `json:"trust_components"`
	LiveLeases      int            `json:"live_leases"`
	RecentEvents    []auditEvent   `json:"recent_trust_events"`
}

func (s *Server) adminWorkerDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var d workerDetail
	err := s.reg.Pool.QueryRow(ctx, `
		select w.id, w.name, w.state, w.state_reason, w.software_version,
		       w.source_asn, w.enrolled_at, w.last_heartbeat_at,
		       ts.score, ts.components,
		       (select count(*) from scheduling.lease l
		        where l.worker_id = w.id and l.released_at is null)
		from registry.worker w
		left join registry.trust_score ts on ts.worker_id = w.id
		where w.id = $1`, id).Scan(&d.ID, &d.Name, &d.State, &d.StateReason,
		&d.SoftwareVersion, &d.SourceASN, &d.EnrolledAt, &d.LastHeartbeatAt,
		&d.TrustScore, &d.TrustComponents, &d.LiveLeases)
	if err != nil {
		problem(w, http.StatusNotFound, "unknown worker")
		return
	}
	rows, err := s.reg.Pool.Query(ctx, `
		select id, 'trust', actor, event_type, null::text, detail, created_at
		from registry.trust_event where worker_id = $1
		order by id desc limit 20`, id)
	if err == nil {
		d.RecentEvents = []auditEvent{}
		for rows.Next() {
			var e auditEvent
			if err := rows.Scan(&e.ID, &e.Category, &e.Actor, &e.Action, &e.Subject,
				&e.Detail, &e.CreatedAt); err == nil {
				d.RecentEvents = append(d.RecentEvents, e)
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, d)
}

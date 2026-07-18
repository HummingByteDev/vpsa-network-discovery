package coordinator

import (
	"context"
	"encoding/json"

	"io"
	"net/http"
	"strings"
	"testing"
)

func adminReq(t *testing.T, e *env, method, path string, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, body)
	}
	return body
}

// TestAdminSurface: fleet overview, snapshot listing, rollback to a previous
// version (and refusal for pruned ones), and the audit query.
func TestAdminSurface(t *testing.T) {
	e := setup(t, "")
	ctx := context.Background()

	if _, err := e.pool.Exec(ctx, `truncate routing.snapshot cascade`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `truncate audit.event`); err != nil {
		t.Fatal(err)
	}
	// v1: superseded but with data (rollback candidate). v0: pruned.
	// v2: published — mirrors what the env's store pointer serves.
	var v1ID int64
	for _, row := range []struct {
		version, status string
		withData        bool
		idOut           *int64
	}{
		{"20260701T0000Z-0", "superseded", false, nil},
		{"20260710T0000Z-1", "superseded", true, &v1ID},
		{e.version, "published", true, nil},
	} {
		var id int64
		if err := e.pool.QueryRow(ctx, `insert into routing.snapshot
			(version, source_uri, source_timestamp, status, prefix_count_v4, prefix_count_v6, published_at)
			values ($1, 'test://bview', now(), $2, 1, 0, now()) returning id`,
			row.version, row.status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if row.idOut != nil {
			*row.idOut = id
		}
		if row.withData {
			if _, err := e.pool.Exec(ctx, `
				insert into routing.provider (provider_id, name, monitoring_enabled, synced_at)
				values ('33333333-3333-3333-3333-333333333333', 'AdminHost', true, now())
				on conflict do nothing`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(ctx, `
				insert into routing.asn (asn, provider_id, synced_at)
				values (64800, '33333333-3333-3333-3333-333333333333', now())
				on conflict do nothing`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(ctx, `insert into routing.prefix
				(snapshot_id, prefix, origin_asn) values ($1, '198.51.100.0/24', 64800)`, id); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Overview reflects a registered worker and the published snapshot.
	c := makeSimWorkers(t, e, 1)[0]
	var ov fleetOverview
	if err := json.Unmarshal(adminReq(t, e, "GET", "/admin/v1/overview", 200), &ov); err != nil {
		t.Fatal(err)
	}
	if ov.WorkersByState["active"] == 0 {
		t.Fatalf("overview missing active worker: %+v", ov.WorkersByState)
	}
	if ov.Snapshot == nil || ov.Snapshot.Version != e.version {
		t.Fatalf("overview snapshot = %+v, want %s", ov.Snapshot, e.version)
	}

	// Snapshot list shows all three, flagging the pruned one.
	var list struct {
		Snapshots []snapshotSummary `json:"snapshots"`
	}
	if err := json.Unmarshal(adminReq(t, e, "GET", "/admin/v1/snapshots", 200), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Snapshots) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(list.Snapshots))
	}
	for _, sn := range list.Snapshots {
		if sn.Version == "20260701T0000Z-0" && sn.HasData {
			t.Fatal("pruned snapshot reported as having data")
		}
	}

	// Rollback to the pruned version is refused; to v1 succeeds and flips
	// both database status and the store pointer.
	adminReq(t, e, "POST", "/admin/v1/snapshots/20260701T0000Z-0/rollback", 409)
	adminReq(t, e, "POST", "/admin/v1/snapshots/20260710T0000Z-1/rollback", 200)
	var status string
	if err := e.pool.QueryRow(ctx, `select status from routing.snapshot
		where id = $1`, v1ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "published" {
		t.Fatalf("rollback target status = %s, want published", status)
	}
	if err := e.pool.QueryRow(ctx, `select status from routing.snapshot
		where version = $1`, e.version).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "superseded" {
		t.Fatalf("old published status = %s, want superseded", status)
	}

	// Worker detail resolves; audit shows the rollback (audit logger is only
	// wired when cfg.Audit is set — here we assert the endpoint shape).
	var detail workerDetail
	if err := json.Unmarshal(adminReq(t, e, "GET", "/admin/v1/workers/"+c.WorkerID, 200), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.State != "active" {
		t.Fatalf("worker detail state = %s", detail.State)
	}
	var audit struct {
		Events []auditEvent `json:"events"`
	}
	if err := json.Unmarshal(adminReq(t, e, "GET", "/admin/v1/audit?limit=10", 200), &audit); err != nil {
		t.Fatal(err)
	}
	adminReq(t, e, "GET", "/admin/v1/workers/00000000-0000-0000-0000-00000000dead", 404)

	// Fail closed without the credential.
	req, _ := http.NewRequest("GET", e.srv.URL+"/admin/v1/overview", strings.NewReader(""))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated overview = %d, want 401", resp.StatusCode)
	}
	_ = audit.Events // shape asserted by decode above
}

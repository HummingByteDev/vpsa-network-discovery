// vapnctl is the platform operator's command line for the coordinator admin
// API: fleet overview, worker lifecycle, snapshot promote/rollback, scheduler
// kill switch, and audit queries.
//
// Configuration: --url/--token flags or VAPN_COORDINATOR_URL /
// VAPN_ADMIN_TOKEN environment variables.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

const usage = `vapnctl — VAPN platform administration

Usage:
  vapnctl [--url URL] [--token TOKEN] <command> [args]

Commands:
  status                              fleet overview
  workers list                        list workers
  workers show <id>                   worker detail (state, trust, leases, events)
  workers create --name N             create a worker; prints one-time token
  workers approve|suspend|quarantine|retire <id> --reason R
                                      change worker state
  workers rotate-key <id>             demand a key rotation at next heartbeat
  snapshots list                      list routing snapshots
  snapshots rollback <version>        re-publish a previous snapshot
  scheduler pause|resume              global assignment kill switch
  audit [--category C] [--since RFC3339] [--limit N]
                                      query the audit log

Environment: VAPN_COORDINATOR_URL, VAPN_ADMIN_TOKEN
`

type client struct {
	url, token string
}

func (c *client) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.url, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var prob struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &prob) == nil && prob.Detail != "" {
			return fmt.Errorf("%s: %s", resp.Status, prob.Detail)
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "vapnctl:", err)
	os.Exit(1)
}

func tab() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func main() {
	fs := flag.NewFlagSet("vapnctl", flag.ExitOnError)
	url := fs.String("url", os.Getenv("VAPN_COORDINATOR_URL"), "coordinator base URL")
	token := fs.String("token", os.Getenv("VAPN_ADMIN_TOKEN"), "admin bearer token")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if *url == "" || *token == "" {
		die(fmt.Errorf("coordinator URL and admin token required (--url/--token or VAPN_COORDINATOR_URL/VAPN_ADMIN_TOKEN)"))
	}
	c := &client{url: *url, token: *token}

	var err error
	switch args[0] {
	case "status":
		err = cmdStatus(c)
	case "workers":
		err = cmdWorkers(c, args[1:])
	case "snapshots":
		err = cmdSnapshots(c, args[1:])
	case "scheduler":
		err = cmdScheduler(c, args[1:])
	case "audit":
		err = cmdAudit(c, args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		die(err)
	}
}

func cmdStatus(c *client) error {
	var ov struct {
		WorkersByState   map[string]int `json:"workers_by_state"`
		SoftwareVersions map[string]int `json:"software_versions"`
		Snapshot         *struct {
			Version     string    `json:"version"`
			PublishedAt time.Time `json:"published_at"`
			Targets     int       `json:"targets"`
		} `json:"published_snapshot"`
		OpenAssignments  int            `json:"open_assignments"`
		LiveLeases       int            `json:"live_leases"`
		OutboxQueued     int            `json:"outbox_queued"`
		SecurityEvents24 map[string]int `json:"security_events_24h"`
		SchedulerPaused  bool           `json:"scheduler_paused"`
		AdvisorSync      map[string]struct {
			LastAttemptAt time.Time  `json:"last_attempt_at"`
			LastSuccessAt *time.Time `json:"last_success_at"`
			LastError     string     `json:"last_error"`
			FailureStreak int        `json:"consecutive_failures"`
		} `json:"advisor_sync"`
	}
	if err := c.do("GET", "/admin/v1/overview", nil, &ov); err != nil {
		return err
	}
	kv := func(m map[string]int) string {
		if len(m) == 0 {
			return "none"
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
		}
		return strings.Join(parts, "  ")
	}
	w := tab()
	fmt.Fprintf(w, "Workers:\t%s\n", kv(ov.WorkersByState))
	fmt.Fprintf(w, "Versions:\t%s\n", kv(ov.SoftwareVersions))
	if ov.Snapshot != nil {
		fmt.Fprintf(w, "Snapshot:\t%s (%d targets, published %s)\n",
			ov.Snapshot.Version, ov.Snapshot.Targets, ov.Snapshot.PublishedAt.Local().Format(time.RFC3339))
	} else {
		fmt.Fprintf(w, "Snapshot:\tnone published\n")
	}
	fmt.Fprintf(w, "Assignments:\t%d open/leased, %d live leases\n", ov.OpenAssignments, ov.LiveLeases)
	fmt.Fprintf(w, "Outbox:\t%d queued\n", ov.OutboxQueued)
	fmt.Fprintf(w, "Security (24h):\t%s\n", kv(ov.SecurityEvents24))
	sched := "running"
	if ov.SchedulerPaused {
		sched = "PAUSED"
	}
	fmt.Fprintf(w, "Scheduler:\t%s\n", sched)
	// VPS Advisor sync is reported per feed because a worker approved on the
	// website only becomes active here once `decisions` succeeds. A failing
	// feed is the answer to "I approved it and the worker still says pending".
	if len(ov.AdvisorSync) > 0 {
		feeds := make([]string, 0, len(ov.AdvisorSync))
		for name := range ov.AdvisorSync {
			feeds = append(feeds, name)
		}
		sort.Strings(feeds)
		for _, name := range feeds {
			f := ov.AdvisorSync[name]
			switch {
			case f.FailureStreak > 0:
				fmt.Fprintf(w, "Advisor %s:\tFAILING (%d in a row): %s\n",
					name, f.FailureStreak, f.LastError)
			case f.LastSuccessAt != nil:
				fmt.Fprintf(w, "Advisor %s:\tok (last success %s)\n",
					name, f.LastSuccessAt.Local().Format(time.RFC3339))
			default:
				fmt.Fprintf(w, "Advisor %s:\tno pass yet\n", name)
			}
		}
	}
	return w.Flush()
}

func cmdWorkers(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workers: subcommand required (list|show|create|approve|suspend|quarantine|retire|rotate-key)")
	}
	switch sub := args[0]; sub {
	case "list":
		var resp struct {
			Workers []struct {
				ID              string     `json:"worker_id"`
				Name            string     `json:"name"`
				State           string     `json:"state"`
				StateReason     string     `json:"state_reason"`
				SoftwareVersion string     `json:"software_version"`
				LastHeartbeat   *time.Time `json:"last_heartbeat_at"`
			} `json:"workers"`
		}
		if err := c.do("GET", "/admin/v1/workers", nil, &resp); err != nil {
			return err
		}
		w := tab()
		fmt.Fprintln(w, "ID\tNAME\tSTATE\tVERSION\tLAST HEARTBEAT\tREASON")
		for _, wk := range resp.Workers {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", wk.ID, wk.Name, wk.State,
				wk.SoftwareVersion, fmtTime(wk.LastHeartbeat), wk.StateReason)
		}
		return w.Flush()
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("workers show: worker id required")
		}
		var d struct {
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
			RecentEvents    []struct {
				Action    string    `json:"action"`
				Actor     string    `json:"actor"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"recent_trust_events"`
		}
		if err := c.do("GET", "/admin/v1/workers/"+args[1], nil, &d); err != nil {
			return err
		}
		w := tab()
		fmt.Fprintf(w, "ID:\t%s\nName:\t%s\nState:\t%s", d.ID, d.Name, d.State)
		if d.StateReason != nil && *d.StateReason != "" {
			fmt.Fprintf(w, " (%s)", *d.StateReason)
		}
		fmt.Fprintln(w)
		if d.SoftwareVersion != nil {
			fmt.Fprintf(w, "Version:\t%s\n", *d.SoftwareVersion)
		}
		if d.SourceASN != nil {
			fmt.Fprintf(w, "Source ASN:\t%d\n", *d.SourceASN)
		}
		fmt.Fprintf(w, "Enrolled:\t%s\n", d.EnrolledAt.Local().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(w, "Heartbeat:\t%s\n", fmtTime(d.LastHeartbeatAt))
		if d.TrustScore != nil {
			fmt.Fprintf(w, "Trust:\t%.3f\n", *d.TrustScore)
		} else {
			fmt.Fprintf(w, "Trust:\tnot yet scored\n")
		}
		fmt.Fprintf(w, "Live leases:\t%d\n", d.LiveLeases)
		if len(d.RecentEvents) > 0 {
			fmt.Fprintln(w, "Recent trust events:")
			for _, e := range d.RecentEvents {
				fmt.Fprintf(w, "  %s\t%s\tby %s\n",
					e.CreatedAt.Local().Format("01-02 15:04"), e.Action, e.Actor)
			}
		}
		return w.Flush()
	case "create":
		cfs := flag.NewFlagSet("workers create", flag.ExitOnError)
		name := cfs.String("name", "", "worker name (required)")
		operator := cfs.String("operator", "", "operator id (optional)")
		_ = cfs.Parse(args[1:])
		if *name == "" {
			return fmt.Errorf("workers create: --name required")
		}
		var resp struct {
			WorkerID string `json:"worker_id"`
			Token    string `json:"enrollment_token"`
		}
		if err := c.do("POST", "/admin/v1/workers",
			map[string]string{"name": *name, "operator_id": *operator}, &resp); err != nil {
			return err
		}
		fmt.Printf("Worker ID:        %s\n", resp.WorkerID)
		fmt.Printf("Enrollment token: %s\n", resp.Token)
		fmt.Println("\nThe token is shown once and expires in 24 hours.")
		return nil
	case "approve", "suspend", "quarantine", "retire":
		if len(args) < 2 {
			return fmt.Errorf("workers %s: worker id required", sub)
		}
		cfs := flag.NewFlagSet("workers "+sub, flag.ExitOnError)
		reason := cfs.String("reason", "", "reason (recorded in audit trail)")
		_ = cfs.Parse(args[2:])
		state := map[string]string{"approve": "active", "suspend": "suspended",
			"quarantine": "quarantined", "retire": "retired"}[sub]
		if err := c.do("POST", "/admin/v1/workers/"+args[1]+"/state",
			map[string]string{"state": state, "reason": *reason}, nil); err != nil {
			return err
		}
		fmt.Printf("worker %s → %s\n", args[1], state)
		return nil
	case "rotate-key":
		if len(args) < 2 {
			return fmt.Errorf("workers rotate-key: worker id required")
		}
		if err := c.do("POST", "/admin/v1/workers/"+args[1]+"/rotate-key", struct{}{}, nil); err != nil {
			return err
		}
		fmt.Println("rotation demanded; the worker rotates at its next heartbeat")
		return nil
	default:
		return fmt.Errorf("workers: unknown subcommand %q", sub)
	}
}

func cmdSnapshots(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("snapshots: subcommand required (list|rollback)")
	}
	switch args[0] {
	case "list":
		var resp struct {
			Snapshots []struct {
				Version       string     `json:"version"`
				Status        string     `json:"status"`
				CreatedAt     time.Time  `json:"created_at"`
				PublishedAt   *time.Time `json:"published_at"`
				PrefixCountV4 int        `json:"prefix_count_v4"`
				PrefixCountV6 int        `json:"prefix_count_v6"`
				HasData       bool       `json:"has_data"`
			} `json:"snapshots"`
		}
		if err := c.do("GET", "/admin/v1/snapshots", nil, &resp); err != nil {
			return err
		}
		w := tab()
		fmt.Fprintln(w, "VERSION\tSTATUS\tPREFIXES(v4/v6)\tPUBLISHED\tROLLBACK?")
		for _, sn := range resp.Snapshots {
			rb := "yes"
			if !sn.HasData {
				rb = "pruned"
			}
			fmt.Fprintf(w, "%s\t%s\t%d/%d\t%s\t%s\n", sn.Version, sn.Status,
				sn.PrefixCountV4, sn.PrefixCountV6, fmtTime(sn.PublishedAt), rb)
		}
		return w.Flush()
	case "rollback":
		if len(args) < 2 {
			return fmt.Errorf("snapshots rollback: version required")
		}
		if err := c.do("POST", "/admin/v1/snapshots/"+args[1]+"/rollback", struct{}{}, nil); err != nil {
			return err
		}
		fmt.Printf("published %s; workers converge on their next heartbeat\n", args[1])
		return nil
	default:
		return fmt.Errorf("snapshots: unknown subcommand %q", args[0])
	}
}

func cmdScheduler(c *client, args []string) error {
	if len(args) == 0 || (args[0] != "pause" && args[0] != "resume") {
		return fmt.Errorf("scheduler: pause or resume required")
	}
	if err := c.do("POST", "/admin/v1/scheduler/"+args[0], struct{}{}, nil); err != nil {
		return err
	}
	if args[0] == "pause" {
		fmt.Println("scheduler paused: lease requests return empty; the fleet idles within one lease interval")
	} else {
		fmt.Println("scheduler resumed")
	}
	return nil
}

func cmdAudit(c *client, args []string) error {
	afs := flag.NewFlagSet("audit", flag.ExitOnError)
	category := afs.String("category", "", "filter by category")
	since := afs.String("since", "", "RFC3339 lower bound (default: 24h ago)")
	limit := afs.Int("limit", 100, "max events")
	_ = afs.Parse(args)
	q := fmt.Sprintf("?limit=%d", *limit)
	if *category != "" {
		q += "&category=" + *category
	}
	if *since != "" {
		q += "&since=" + *since
	}
	var resp struct {
		Events []struct {
			Category  string         `json:"category"`
			Actor     string         `json:"actor"`
			Action    string         `json:"action"`
			Subject   *string        `json:"subject"`
			Detail    map[string]any `json:"detail"`
			CreatedAt time.Time      `json:"created_at"`
		} `json:"events"`
	}
	if err := c.do("GET", "/admin/v1/audit"+q, nil, &resp); err != nil {
		return err
	}
	w := tab()
	fmt.Fprintln(w, "TIME\tCATEGORY\tACTOR\tACTION\tSUBJECT\tDETAIL")
	for _, e := range resp.Events {
		subject := "-"
		if e.Subject != nil {
			subject = *e.Subject
		}
		detail := ""
		if len(e.Detail) > 0 {
			raw, _ := json.Marshal(e.Detail)
			detail = string(raw)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.CreatedAt.Local().Format("01-02 15:04:05"), e.Category, e.Actor,
			e.Action, subject, detail)
	}
	return w.Flush()
}

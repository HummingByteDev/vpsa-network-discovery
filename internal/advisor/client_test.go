package advisor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestValidateRejectsBaseURLWithPath: the contract fixes the paths, so the
// configured value must be a bare address. `https://site.example/api` is the
// slip that produces `/api/api/v1/monitoring/...` and a 404 on every feed —
// including the approvals one, which strands workers in `pending`.
func TestValidateRejectsBaseURLWithPath(t *testing.T) {
	for _, tc := range []struct {
		url     string
		wantErr bool
	}{
		{"https://www.example.com", false},
		{"https://www.example.com/", false}, // trailing slash is normalized away
		{"http://mockadvisor:8081", false},
		{"https://www.example.com/api", true},
		{"https://www.example.com/api/v1/monitoring", true},
		{"ftp://www.example.com", true},
		{"", true},
	} {
		err := New(tc.url, "t").Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("Validate(%q) error = %v, wantErr %v", tc.url, err, tc.wantErr)
		}
	}
}

// TestRedirectIsRefusedWithAnActionableError: Go drops the Authorization
// header across a host change, so following a `www` → apex redirect turns an
// authenticated pull into an anonymous 401 that reads like a bad credential.
// The client must refuse and name the target instead.
func TestRedirectIsRefusedWithAnActionableError(t *testing.T) {
	var reachedTarget bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedTarget = true
		if r.Header.Get("Authorization") == "" {
			t.Error("request reached the redirect target without its credential")
		}
		fmt.Fprint(w, `{"decisions": [], "next_cursor": null}`)
	}))
	defer target.Close()

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer front.Close()

	_, err := New(front.URL, "svc").ListDecisions(context.Background(), time.Time{})
	if err == nil {
		t.Fatal("a redirected feed was reported as a success")
	}
	if !strings.Contains(err.Error(), "redirects to") || !strings.Contains(err.Error(), target.URL) {
		t.Fatalf("error does not name the redirect target: %v", err)
	}
	if !strings.Contains(err.Error(), "VAPN_ADVISOR_URL") {
		t.Fatalf("error does not say what to fix: %v", err)
	}
	if reachedTarget {
		t.Fatal("the redirect was followed; the credential would have been stripped")
	}
}

// TestErrorsNameTheURLTheyCameFrom: a 401 body alone cannot distinguish a bad
// token from a call to the wrong site.
func TestErrorsNameTheURLTheyCameFrom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"detail":"invalid platform service token"}`)
	}))
	defer srv.Close()

	err := New(srv.URL, "wrong").Ping(context.Background())
	if err == nil {
		t.Fatal("401 reported as success")
	}
	for _, want := range []string{srv.URL, "401", "invalid platform service token"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q is missing %q", err, want)
		}
	}
}

// TestBaseURLRedactsCredentials: the base URL is logged on every failed pull
// and quoted in every error, so a URL carrying userinfo must not leak it.
func TestBaseURLRedactsCredentials(t *testing.T) {
	c := New("https://svc:hunter2@site.example", "t")
	if strings.Contains(c.BaseURL(), "hunter2") {
		t.Fatalf("BaseURL leaks the password: %s", c.BaseURL())
	}
	if !strings.Contains(c.BaseURL(), "site.example") {
		t.Fatalf("BaseURL lost the host: %s", c.BaseURL())
	}
}

// pagedFeed serves `rows` one page at a time, keyed on an index cursor.
func pagedFeed(t *testing.T, key string, rows []string, perPage int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			if _, err := fmt.Sscanf(c, "%d", &start); err != nil {
				t.Errorf("malformed cursor %q", c)
			}
		}
		end := min(start+perPage, len(rows))
		next := "null"
		if end < len(rows) {
			next = fmt.Sprintf("%q", fmt.Sprint(end))
		}
		fmt.Fprintf(w, `{%q: [%s], "next_cursor": %s}`,
			key, strings.Join(rows[start:end], ","), next)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPaginationIsFollowedToTheEnd: the website pages at 500 rows by default.
// A client that reads page one silently loses every provider, enrollment and
// approval past it — a failure with no error to notice.
func TestPaginationIsFollowedToTheEnd(t *testing.T) {
	t.Run("decisions", func(t *testing.T) {
		rows := make([]string, 0, 7)
		for i := range 7 {
			rows = append(rows, fmt.Sprintf(
				`{"decision_id":"d-%d","worker_id":"w","state":"active","reason":"r",
				  "decided_at":"2026-08-16T10:0%d:00Z"}`, i, i))
		}
		srv := pagedFeed(t, "decisions", rows, 2)
		got, err := New(srv.URL, "t").ListDecisions(context.Background(), time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 7 {
			t.Fatalf("got %d decisions across pages, want 7", len(got))
		}
		if got[6].DecisionID != "d-6" {
			t.Fatalf("last decision = %q, want d-6", got[6].DecisionID)
		}
	})

	t.Run("enrollments", func(t *testing.T) {
		rows := []string{
			`{"enrollment_id":"e-0","worker_id":"w0","worker_name":"a","operator_id":"o","token_hash":"ab","expires_at":"2026-08-17T10:00:00Z"}`,
			`{"enrollment_id":"e-1","worker_id":"w1","worker_name":"b","operator_id":"o","token_hash":"cd","expires_at":"2026-08-17T10:00:00Z"}`,
			`{"enrollment_id":"e-2","worker_id":"w2","worker_name":"c","operator_id":"o","token_hash":"ef","expires_at":"2026-08-17T10:00:00Z"}`,
		}
		srv := pagedFeed(t, "enrollments", rows, 1)
		got, err := New(srv.URL, "t").ListPendingEnrollments(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d enrollments across pages, want 3", len(got))
		}
	})

	t.Run("providers", func(t *testing.T) {
		rows := []string{
			`{"provider_id":"p0","name":"A","asns":[64500],"monitoring_enabled":true,"priority":1,"updated_at":"2026-08-16T10:00:00Z"}`,
			`{"provider_id":"p1","name":"B","asns":[64501],"monitoring_enabled":true,"priority":1,"updated_at":"2026-08-16T10:00:00Z"}`,
		}
		srv := pagedFeed(t, "providers", rows, 1)
		got, err := New(srv.URL, "t").ListProviders(context.Background(), true)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d providers across pages, want 2", len(got))
		}
	})
}

// TestPaginationTerminatesOnARepeatedCursor: a server that never advances its
// cursor must produce an error, not an endless loop inside the sync goroutine.
func TestPaginationTerminatesOnARepeatedCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"decisions": [], "next_cursor": "stuck"}`)
	}))
	defer srv.Close()

	done := make(chan error, 1)
	go func() {
		_, err := New(srv.URL, "t").ListDecisions(context.Background(), time.Time{})
		done <- err
	}()
	select {
	case <-done: // an error or an empty result, either way it returned
	case <-time.After(10 * time.Second):
		t.Fatal("pagination did not terminate on a repeated cursor")
	}
}

// TestSinceIsOmittedWhenZero: a zero cursor asks for the whole feed, which is
// how a restarted coordinator catches up on approvals it was down for.
func TestSinceIsOmittedWhenZero(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		fmt.Fprint(w, `{"decisions": [], "next_cursor": null}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "t")

	if _, err := c.ListDecisions(context.Background(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if queries[0] != "" {
		t.Fatalf("zero cursor sent %q, want no query", queries[0])
	}

	at := time.Date(2026, 8, 16, 15, 4, 15, 123456000, time.UTC)
	if _, err := c.ListDecisions(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	// Sub-second precision matters: truncating to the second would re-deliver
	// every decision sharing that second on the next pass.
	if !strings.Contains(queries[1], "15%3A04%3A15.123456Z") {
		t.Fatalf("since query = %q, want full-precision timestamp", queries[1])
	}
}

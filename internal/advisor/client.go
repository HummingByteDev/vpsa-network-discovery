// Package advisor is the HTTP client for the VPS Advisor monitoring API
// (contract A in docs/architecture/04-api-contracts.md). In development it
// talks to the mockadvisor stub; the contract is identical.
package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxPages bounds a cursor walk so a server that returns a constant cursor
// cannot spin this loop forever. Every feed is far smaller than this.
const maxPages = 1000

type Provider struct {
	ProviderID        string    `json:"provider_id"`
	Name              string    `json:"name"`
	ASNs              []int64   `json:"asns"`
	MonitoringEnabled bool      `json:"monitoring_enabled"`
	Priority          int       `json:"priority"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// Never follow a redirect. Go strips the Authorization header on a
			// cross-host hop, so a www → apex 301 turns an authenticated pull
			// into an anonymous 401 that looks like a bad credential; an
			// APPEND_SLASH 301 lands on a path the contract does not define.
			// Surfacing the redirect instead names the misconfiguration.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// BaseURL is the configured address, normalized (no trailing slash) and safe
// to log: any userinfo is redacted, because this string appears in error
// messages and log lines on every failed pull.
func (c *Client) BaseURL() string { return redactUserinfo(c.baseURL) }

func redactUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("[redacted]")
	return u.String()
}

// Validate reports whether the configured base URL is shaped the way the
// contract expects: an absolute http(s) address with **no path**, because the
// client appends `/api/v1/monitoring/...` itself. A base URL carrying a path
// (the common `.../api` slip) makes every call 404 — silently, since each feed
// only warns. Callers check this at startup so the mistake is caught before it
// costs an operator a debugging session.
func (c *Client) Validate() error {
	if c.baseURL == "" {
		return fmt.Errorf("VAPN_ADVISOR_URL is empty")
	}
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("VAPN_ADVISOR_URL is not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("VAPN_ADVISOR_URL must be http or https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("VAPN_ADVISOR_URL has no host")
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return fmt.Errorf("VAPN_ADVISOR_URL must be a bare base URL with no path "+
			"(got path %q — the client appends /api/v1/monitoring/... itself, so this "+
			"would call %s/api/v1/monitoring/...)", u.Path, c.baseURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("VAPN_ADVISOR_URL must not carry a query or fragment")
	}
	return nil
}

// Ping makes the cheapest authenticated call in the contract, so a
// misconfigured URL or credential is reported once at startup rather than
// discovered as an unexplained absence of workers, providers, or approvals.
func (c *Client) Ping(ctx context.Context) error {
	var body struct {
		Providers []json.RawMessage `json:"providers"`
	}
	return c.doJSON(ctx, http.MethodGet,
		"/api/v1/monitoring/providers?enabled=true&limit=1", nil, &body)
}

// ListProviders returns providers eligible for monitoring, following the
// contract's cursor pagination to the end of the feed.
func (c *Client) ListProviders(ctx context.Context, enabledOnly bool) ([]Provider, error) {
	path := "/api/v1/monitoring/providers"
	if enabledOnly {
		path += "?enabled=true"
	}
	return fetchPages[Provider](ctx, c, path, "providers")
}

// withCursor appends an opaque pagination cursor to a contract path.
func withCursor(path, cursor string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "cursor=" + url.QueryEscape(cursor)
}

// fetchPages walks a cursor-paginated feed to its end and returns every row.
//
// Following `next_cursor` is not optional: the website pages at 500 rows by
// default, so a client that reads only the first page silently loses providers
// once the catalog grows past it — and, on the decision feed, loses approvals.
func fetchPages[T any](ctx context.Context, c *Client, path, key string) ([]T, error) {
	var out []T
	cursor := ""
	for page := 0; page < maxPages; page++ {
		next := path
		if cursor != "" {
			next = withCursor(path, cursor)
		}
		var body map[string]json.RawMessage
		if err := c.doJSON(ctx, http.MethodGet, next, nil, &body); err != nil {
			return nil, err
		}
		var rows []T
		if raw, ok := body[key]; ok && len(raw) > 0 {
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nil, fmt.Errorf("decode %s: %w", key, err)
			}
		}
		out = append(out, rows...)

		var advance string
		if raw, ok := body["next_cursor"]; ok {
			// A JSON null leaves this empty, which is the contract's "no more".
			_ = json.Unmarshal(raw, &advance)
		}
		if advance == "" || advance == cursor {
			return out, nil
		}
		cursor = advance
	}
	return nil, fmt.Errorf("%s: pagination did not terminate after %d pages", path, maxPages)
}

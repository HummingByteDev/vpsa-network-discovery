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
	"time"
)

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
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ListProviders returns providers eligible for monitoring.
func (c *Client) ListProviders(ctx context.Context, enabledOnly bool) ([]Provider, error) {
	u, err := url.Parse(c.baseURL + "/api/v1/monitoring/providers")
	if err != nil {
		return nil, err
	}
	if enabledOnly {
		q := u.Query()
		q.Set("enabled", "true")
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("advisor request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("advisor returned %s for %s", resp.Status, u.Path)
	}
	var body struct {
		Providers []Provider `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode providers: %w", err)
	}
	return body.Providers, nil
}

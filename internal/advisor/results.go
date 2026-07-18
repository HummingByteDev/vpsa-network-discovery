package advisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Push/pull surfaces of the monitoring contract beyond the provider catalog:
// results ingestion (A4), enrollment sync and admin decisions (A2/A3).

func (c *Client) doJSON(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("advisor request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("advisor %s %s: %s: %s", method, path, resp.Status, string(raw))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// PutProviderStatus upserts an aggregated status document.
func (c *Client) PutProviderStatus(ctx context.Context, providerID string, doc json.RawMessage) error {
	return c.doJSON(ctx, http.MethodPut, "/api/v1/monitoring/results/providers/"+providerID, doc, nil)
}

// PostAnomaly reports an anomaly event document.
func (c *Client) PostAnomaly(ctx context.Context, doc json.RawMessage) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/monitoring/results/anomalies", doc, nil)
}

// PostFleetTelemetry reports a fleet summary for the admin dashboard.
func (c *Client) PostFleetTelemetry(ctx context.Context, doc json.RawMessage) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/monitoring/telemetry/fleet", doc, nil)
}

// PendingEnrollment is a worker created on VPS Advisor awaiting platform-side
// provisioning: the platform stores the token hash so the worker container
// can redeem the operator's one-time token against the coordinator.
type PendingEnrollment struct {
	EnrollmentID string    `json:"enrollment_id"`
	WorkerID     string    `json:"worker_id"`
	WorkerName   string    `json:"worker_name"`
	OperatorID   string    `json:"operator_id"`
	TokenHash    string    `json:"token_hash"` // hex sha256 of the one-time token
	ExpiresAt    time.Time `json:"expires_at"`
}

func (c *Client) ListPendingEnrollments(ctx context.Context) ([]PendingEnrollment, error) {
	var body struct {
		Enrollments []PendingEnrollment `json:"enrollments"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/monitoring/enrollments/pending", nil, &body); err != nil {
		return nil, err
	}
	return body.Enrollments, nil
}

func (c *Client) MarkEnrollmentRegistered(ctx context.Context, enrollmentID string) error {
	return c.doJSON(ctx, http.MethodPost,
		"/api/v1/monitoring/enrollments/"+enrollmentID+"/registered", nil, nil)
}

// Decision is an admin action taken on the VPS Advisor dashboard.
type Decision struct {
	DecisionID string    `json:"decision_id"`
	WorkerID   string    `json:"worker_id"`
	State      string    `json:"state"` // active|suspended|quarantined|retired
	Reason     string    `json:"reason"`
	DecidedAt  time.Time `json:"decided_at"`
}

func (c *Client) ListDecisions(ctx context.Context, since time.Time) ([]Decision, error) {
	path := "/api/v1/monitoring/admin/decisions?since=" + since.UTC().Format(time.RFC3339)
	var body struct {
		Decisions []Decision `json:"decisions"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &body); err != nil {
		return nil, err
	}
	return body.Decisions, nil
}

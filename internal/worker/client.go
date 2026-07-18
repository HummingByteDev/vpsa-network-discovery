package worker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/wireauth"
)

// Client talks to the coordinator, signing every request after registration.
type Client struct {
	BaseURL  string
	WorkerID string
	Key      ed25519.PrivateKey
	HTTP     *http.Client
}

func NewClient(baseURL string, key ed25519.PrivateKey) *Client {
	return &Client{BaseURL: baseURL, Key: key, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// WithID sets the worker identity and returns the client (test/tooling aid).
func (c *Client) WithID(id string) *Client {
	c.WorkerID = id
	return c
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.WorkerID != "" {
		if err := wireauth.Sign(req, c.WorkerID, c.Key, payload); err != nil {
			return err
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, string(raw))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type RegisterResponse struct {
	WorkerID string `json:"worker_id"`
	State    string `json:"state"`
}

func (c *Client) Register(ctx context.Context, enrollmentToken, name, version string) (*RegisterResponse, error) {
	pub := c.Key.Public().(ed25519.PublicKey)
	var resp RegisterResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/workers/register", map[string]string{
		"enrollment_token": enrollmentToken,
		"public_key":       base64.StdEncoding.EncodeToString(pub),
		"name":             name,
		"software_version": version,
	}, &resp)
	if err != nil {
		return nil, err
	}
	c.WorkerID = resp.WorkerID
	return &resp, nil
}

type HeartbeatResponse struct {
	State    string          `json:"state"`
	Config   json.RawMessage `json:"config"`
	Snapshot *struct {
		Version string `json:"version"`
	} `json:"snapshot"`
}

func (c *Client) Heartbeat(ctx context.Context, version string) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/workers/heartbeat",
		map[string]string{"software_version": version}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

type ManifestResponse struct {
	Manifest     artifact.Manifest `json:"manifest"`
	DownloadPath string            `json:"download_path"`
}

func (c *Client) CurrentManifest(ctx context.Context) (*ManifestResponse, error) {
	var resp ManifestResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/artifacts/routing/current", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DownloadArtifact streams the current artifact to a temp file in dir.
func (c *Client) DownloadArtifact(ctx context.Context, downloadPath, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+downloadPath, nil)
	if err != nil {
		return "", err
	}
	if err := wireauth.Sign(req, c.WorkerID, c.Key, nil); err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	tmp, err := os.CreateTemp(dir, "routing-*.sqlite.partial")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

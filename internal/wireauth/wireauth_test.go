package wireauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"software_version":"0.1.0"}`)
	req := httptest.NewRequest("POST", "/api/v1/workers/heartbeat", bytes.NewReader(body))
	if err := Sign(req, "w-1", priv, body); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify("POST", "/api/v1/workers/heartbeat", req.Header, body, pub, time.Now()); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	// Tampered body fails.
	if _, err := Verify("POST", "/api/v1/workers/heartbeat", req.Header,
		[]byte(`{"software_version":"9.9.9"}`), pub, time.Now()); err == nil {
		t.Fatal("tampered body accepted")
	}
	// Different path fails.
	if _, err := Verify("POST", "/api/v1/observations", req.Header, body, pub, time.Now()); err == nil {
		t.Fatal("replay against a different path accepted")
	}
	// Wrong key fails.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify("POST", "/api/v1/workers/heartbeat", req.Header, body, otherPub, time.Now()); err == nil {
		t.Fatal("wrong key accepted")
	}
	// Stale timestamp fails.
	if _, err := Verify("POST", "/api/v1/workers/heartbeat", req.Header, body, pub,
		time.Now().Add(3*time.Minute)); err == nil {
		t.Fatal("stale timestamp accepted")
	}
}

func TestVerifyMissingHeaders(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	req := httptest.NewRequest("GET", "/api/v1/workers/me", nil)
	if _, err := Verify("GET", "/api/v1/workers/me", req.Header, nil, pub, time.Now()); err == nil {
		t.Fatal("unsigned request accepted")
	}
}

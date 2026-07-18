// Package wireauth implements the worker↔coordinator request signing scheme
// (docs/architecture/05-security-trust-model.md §2): Ed25519 over
// method|path|timestamp|nonce|sha256(body). Both sides use this package, so
// the canonical string cannot drift. Phase 4 verifies signatures and the
// timestamp window; nonce replay tracking and trust events land in Phase 6.
package wireauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	HeaderWorkerID  = "X-Worker-Id"
	HeaderTimestamp = "X-Timestamp"
	HeaderNonce     = "X-Nonce"
	HeaderSignature = "X-Signature"

	// MaxSkew is the accepted difference between the request timestamp and
	// the server clock.
	MaxSkew = 2 * time.Minute
)

func canonical(method, path, timestamp, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{
		method, path, timestamp, nonce, hex.EncodeToString(sum[:]),
	}, "|"))
}

// Sign stamps the four auth headers onto req. body must be the exact request
// body bytes (nil for empty).
func Sign(req *http.Request, workerID string, key ed25519.PrivateKey, body []byte) error {
	nonceRaw := make([]byte, 16)
	if _, err := rand.Read(nonceRaw); err != nil {
		return err
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	nonce := hex.EncodeToString(nonceRaw)
	sig := ed25519.Sign(key, canonical(req.Method, req.URL.Path, ts, nonce, body))
	req.Header.Set(HeaderWorkerID, workerID)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(sig))
	return nil
}

// Verify checks the signature headers of a request whose body bytes are
// supplied by the caller. Returns the nonce for replay tracking.
func Verify(method, path string, header http.Header, body []byte, pub ed25519.PublicKey, now time.Time) (nonce string, err error) {
	ts := header.Get(HeaderTimestamp)
	nonce = header.Get(HeaderNonce)
	sigB64 := header.Get(HeaderSignature)
	if ts == "" || nonce == "" || sigB64 == "" {
		return "", fmt.Errorf("missing signature headers")
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "", fmt.Errorf("bad timestamp: %w", err)
	}
	skew := now.Sub(t)
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxSkew {
		return "", fmt.Errorf("timestamp outside accepted window")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return "", fmt.Errorf("bad signature encoding: %w", err)
	}
	if !ed25519.Verify(pub, canonical(method, path, ts, nonce, body), sig) {
		return "", fmt.Errorf("signature verification failed")
	}
	return nonce, nil
}

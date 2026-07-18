// Package artifact implements snapshot artifact publication and verification:
// the SQLite export workers download, the Ed25519-signed manifest that makes
// the artifact store untrusted by design, the current-version pointer, and
// retention pruning. The builder publishes through this package; workers
// verify through it (Phase 4).
package artifact

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Manifest describes one published snapshot artifact. The signature covers
// the canonical JSON encoding of the manifest with Signature empty; workers
// verify both the signature and the SHA-256 of the downloaded object.
type Manifest struct {
	Version          string    `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	ObjectKey        string    `json:"object_key"`
	SHA256           string    `json:"sha256"`
	SizeBytes        int64     `json:"size_bytes"`
	PrefixCountV4    int       `json:"prefix_count_v4"`
	PrefixCountV6    int       `json:"prefix_count_v6"`
	TargetCount      int       `json:"target_count"`
	MinWorkerVersion string    `json:"min_worker_version"`
	Signature        string    `json:"signature,omitempty"`
}

// Pointer is the small mutable object naming the version currently in force.
// Its integrity comes from the signed manifest it references, so the pointer
// itself needs no signature; a tampered pointer can only ever select another
// validly signed version (downgrade attempts are caught by the worker's
// monotonic version check).
type Pointer struct {
	Version     string    `json:"version"`
	ManifestKey string    `json:"manifest_key"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (m Manifest) payload() ([]byte, error) {
	m.Signature = ""
	return json.Marshal(m)
}

// ParseSigningKey decodes a base64-encoded 32-byte Ed25519 seed.
func ParseSigningKey(b64seed string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(b64seed)
	if err != nil {
		return nil, fmt.Errorf("signing key is not valid base64: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("signing key seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// ParsePublicKey decodes a base64-encoded 32-byte Ed25519 public key.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("public key is not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

func Sign(m *Manifest, key ed25519.PrivateKey) error {
	payload, err := m.payload()
	if err != nil {
		return err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))
	return nil
}

// Verify checks the manifest signature. It fails closed: any decoding or
// signature problem is an error.
func Verify(m Manifest, pub ed25519.PublicKey) error {
	if m.Signature == "" {
		return fmt.Errorf("manifest is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("signature is not valid base64: %w", err)
	}
	payload, err := m.payload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return fmt.Errorf("manifest signature verification failed")
	}
	return nil
}

// HashFile returns the hex SHA-256 and size of the file at path.
func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// VerifyFile checks a downloaded artifact against its manifest.
func VerifyFile(path string, m Manifest) error {
	sum, size, err := HashFile(path)
	if err != nil {
		return err
	}
	if size != m.SizeBytes {
		return fmt.Errorf("artifact size %d does not match manifest %d", size, m.SizeBytes)
	}
	if sum != m.SHA256 {
		return fmt.Errorf("artifact checksum does not match manifest")
	}
	return nil
}

// Object keys within the artifact store.
func ObjectKeySQLite(version string) string   { return "snapshots/" + version + "/routing.sqlite" }
func ObjectKeyManifest(version string) string { return "snapshots/" + version + "/manifest.json" }

const PointerKey = "snapshots/current.json"

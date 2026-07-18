package artifact

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func testManifest() Manifest {
	return Manifest{
		Version: "20260718T0000Z-1", CreatedAt: time.Now().UTC(),
		ObjectKey: ObjectKeySQLite("20260718T0000Z-1"),
		SHA256:    "abc123", SizeBytes: 42,
		PrefixCountV4: 10, PrefixCountV6: 2, TargetCount: 5,
		MinWorkerVersion: "0.1.0",
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	key := testKey(t)
	m := testManifest()
	if err := Sign(&m, key); err != nil {
		t.Fatal(err)
	}
	if err := Verify(m, key.Public().(ed25519.PublicKey)); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestVerifyFailsClosed(t *testing.T) {
	key := testKey(t)
	pub := key.Public().(ed25519.PublicKey)

	unsigned := testManifest()
	if err := Verify(unsigned, pub); err == nil {
		t.Fatal("unsigned manifest accepted")
	}

	tampered := testManifest()
	if err := Sign(&tampered, key); err != nil {
		t.Fatal(err)
	}
	tampered.SHA256 = "d0d0" // attacker swaps the artifact hash
	if err := Verify(tampered, pub); err == nil {
		t.Fatal("tampered manifest accepted")
	}

	wrongKey := testManifest()
	if err := Sign(&wrongKey, testKey(t)); err != nil {
		t.Fatal(err)
	}
	if err := Verify(wrongKey, pub); err == nil {
		t.Fatal("manifest signed by a different key accepted")
	}
}

func TestVerifyFileDetectsTamper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(path, []byte("snapshot-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, size, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{SHA256: sum, SizeBytes: size}
	if err := VerifyFile(path, m); err != nil {
		t.Fatalf("intact file rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("snapshot-bytEs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(path, m); err == nil {
		t.Fatal("tampered file accepted")
	}
}

func TestKeyParsing(t *testing.T) {
	if _, err := ParseSigningKey("not-base64!"); err == nil {
		t.Fatal("garbage seed accepted")
	}
	if _, err := ParseSigningKey("c2hvcnQ="); err == nil {
		t.Fatal("short seed accepted")
	}
}

// Package worker implements the community measurement agent: identity
// management, registration, the heartbeat loop, and verified snapshot sync.
// The probe engine lands in Phase 5.
package worker

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// State is the worker's persistent directory: the private key (never leaves
// this volume), the assigned worker ID, and the verified snapshot artifact.
type State struct {
	Dir string
}

func (s State) keyPath() string      { return filepath.Join(s.Dir, "worker.key") }
func (s State) idPath() string       { return filepath.Join(s.Dir, "worker.id") }
func (s State) SnapshotPath() string { return filepath.Join(s.Dir, "routing.sqlite") }
func (s State) versionPath() string  { return filepath.Join(s.Dir, "routing.version") }

func (s State) Ensure() error {
	return os.MkdirAll(s.Dir, 0o700)
}

// Key loads the Ed25519 key, generating one on first boot.
func (s State) Key() (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(s.keyPath())
	if err == nil {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("corrupt worker key file %s", s.keyPath())
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(priv.Seed())
	if err := os.WriteFile(s.keyPath(), []byte(enc+"\n"), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// ReplaceKey atomically persists a new private key seed.
func (s State) ReplaceKey(priv ed25519.PrivateKey) error {
	enc := base64.StdEncoding.EncodeToString(priv.Seed())
	tmp := s.keyPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(enc+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.keyPath())
}

// WorkerID returns the persisted ID, or "" before first registration.
func (s State) WorkerID() (string, error) {
	raw, err := os.ReadFile(s.idPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func (s State) SaveWorkerID(id string) error {
	return os.WriteFile(s.idPath(), []byte(id+"\n"), 0o600)
}

// SnapshotVersion returns the version of the locally installed artifact.
func (s State) SnapshotVersion() string {
	raw, err := os.ReadFile(s.versionPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// InstallSnapshot atomically swaps in a verified artifact file.
func (s State) InstallSnapshot(tmpPath, version string) error {
	if err := os.Rename(tmpPath, s.SnapshotPath()); err != nil {
		return err
	}
	return os.WriteFile(s.versionPath(), []byte(version+"\n"), 0o644)
}

// Package observation defines the signed measurement record exchanged between
// workers and the coordinator. Each observation is individually signed by the
// worker (docs/architecture/05-security-trust-model.md §2) so provenance
// survives any future ingestion path; the signature covers the canonical JSON
// encoding with Signature empty (encoding/json sorts map keys, so the
// encoding is deterministic).
package observation

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type Observation struct {
	AssignmentID int64          `json:"assignment_id"`
	Target       string         `json:"target"`
	ProbeType    string         `json:"probe_type"`
	MeasuredAt   time.Time      `json:"measured_at"`
	OK           bool           `json:"ok"`
	RTTMillis    *float64       `json:"rtt_ms,omitempty"`
	PacketsSent  int            `json:"packets_sent"`
	PacketsLost  int            `json:"packets_lost"`
	Metrics      map[string]any `json:"metrics,omitempty"`
	Signature    string         `json:"signature,omitempty"` // base64 Ed25519
}

func (o Observation) payload() ([]byte, error) {
	o.Signature = ""
	return json.Marshal(o)
}

func Sign(o *Observation, key ed25519.PrivateKey) error {
	payload, err := o.payload()
	if err != nil {
		return err
	}
	o.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))
	return nil
}

func Verify(o Observation, pub ed25519.PublicKey) error {
	if o.Signature == "" {
		return fmt.Errorf("observation is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(o.Signature)
	if err != nil {
		return fmt.Errorf("bad signature encoding: %w", err)
	}
	payload, err := o.payload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return fmt.Errorf("observation signature verification failed")
	}
	return nil
}

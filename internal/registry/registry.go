// Package registry manages worker identity and lifecycle state in the
// registry schema: enrollment tokens, key registration, state transitions,
// heartbeats. State semantics follow docs/architecture/06-lifecycles.md §1.
package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DevOperatorID owns workers created through the platform admin API in
// development; production workers carry their VPS Advisor operator ID.
const DevOperatorID = "00000000-0000-0000-0000-000000000001"

var (
	ErrBadToken      = errors.New("enrollment token invalid, used, or expired")
	ErrUnknownWorker = errors.New("unknown worker")
	ErrBadTransition = errors.New("illegal state transition")
)

type Worker struct {
	ID              string
	Name            string
	State           string
	StateReason     string
	SoftwareVersion string
	LastHeartbeat   *time.Time
	Config          []byte // raw JSON pushed to the worker
}

type Store struct{ Pool *pgxpool.Pool }

// CreateWorker provisions a worker record plus a one-time enrollment token
// (returned in plaintext exactly once; only its hash is stored).
func (s *Store) CreateWorker(ctx context.Context, operatorID, name string, tokenTTL time.Duration) (workerID, token string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	workerID = uuid.NewString()

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `insert into registry.worker (id, operator_id, name)
		values ($1, $2, $3)`, workerID, operatorID, name); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `insert into registry.enrollment_token (token_hash, worker_id, expires_at)
		values ($1, $2, $3)`, hash[:], workerID, time.Now().Add(tokenTTL)); err != nil {
		return "", "", err
	}
	return workerID, token, tx.Commit(ctx)
}

// Register redeems an enrollment token: stores the worker's public key and
// marks the token used. The worker stays pending until approved.
func (s *Store) Register(ctx context.Context, token string, pub ed25519.PublicKey, softwareVersion string) (workerID string, state string, err error) {
	hash := sha256.Sum256([]byte(token))
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		update registry.enrollment_token set used_at = now()
		where token_hash = $1 and used_at is null and expires_at > now()
		returning worker_id`, hash[:]).Scan(&workerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrBadToken
	}
	if err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `insert into registry.worker_key (worker_id, public_key)
		values ($1, $2)`, workerID, []byte(pub)); err != nil {
		return "", "", err
	}
	if err := tx.QueryRow(ctx, `update registry.worker
		set software_version = $2 where id = $1
		returning state`, workerID, softwareVersion).Scan(&state); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `insert into registry.trust_event (worker_id, event_type, actor)
		values ($1, 'registered', 'system')`, workerID); err != nil {
		return "", "", err
	}
	return workerID, state, tx.Commit(ctx)
}

var transitions = map[string][]string{
	"pending":     {"active", "suspended", "retired"},
	"active":      {"suspended", "quarantined", "retired"},
	"suspended":   {"active", "retired"},
	"quarantined": {"active", "suspended", "retired"},
	"retired":     {},
}

// SetState applies an admin/system lifecycle transition and records it.
func (s *Store) SetState(ctx context.Context, workerID, newState, reason, actor string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current string
	err = tx.QueryRow(ctx, `select state from registry.worker where id = $1 for update`,
		workerID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnknownWorker
	}
	if err != nil {
		return err
	}
	allowed := false
	for _, t := range transitions[current] {
		if t == newState {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: %s → %s", ErrBadTransition, current, newState)
	}
	cols := ""
	switch newState {
	case "active":
		if current == "pending" {
			cols = ", approved_at = now()"
		}
	case "retired":
		cols = ", retired_at = now()"
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`update registry.worker
		set state = $2, state_reason = $3%s where id = $1`, cols),
		workerID, newState, reason); err != nil {
		return err
	}
	if newState == "retired" {
		if _, err := tx.Exec(ctx, `update registry.worker_key
			set revoked_at = now(), revoke_reason = 'worker retired'
			where worker_id = $1 and revoked_at is null`, workerID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `insert into registry.trust_event (worker_id, event_type, detail, actor)
		values ($1, $2, $3, $4)`, workerID, "state:"+newState,
		fmt.Sprintf(`{"from": %q, "reason": %q}`, current, reason), actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Heartbeat records liveness and returns the worker's current standing.
func (s *Store) Heartbeat(ctx context.Context, workerID, softwareVersion string) (*Worker, error) {
	w := &Worker{ID: workerID}
	err := s.Pool.QueryRow(ctx, `update registry.worker
		set last_heartbeat_at = now(), software_version = $2
		where id = $1
		returning name, state, coalesce(state_reason,''), config`,
		workerID, softwareVersion).Scan(&w.Name, &w.State, &w.StateReason, &w.Config)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnknownWorker
	}
	if err != nil {
		return nil, err
	}
	w.SoftwareVersion = softwareVersion
	return w, nil
}

// ActiveKeys returns every key currently valid for the worker (during a
// rotation overlap there are two) plus its state. Revoked or expired keys
// never verify.
func (s *Store) ActiveKeys(ctx context.Context, workerID string) ([]ed25519.PublicKey, string, error) {
	rows, err := s.Pool.Query(ctx, `
		select k.public_key, w.state
		from registry.worker w
		join registry.worker_key k on k.worker_id = w.id
		where w.id = $1 and k.revoked_at is null
		  and k.valid_from <= now()
		  and (k.valid_until is null or k.valid_until > now())
		order by k.id desc`, workerID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var keys []ed25519.PublicKey
	var state string
	for rows.Next() {
		var key []byte
		if err := rows.Scan(&key, &state); err != nil {
			return nil, "", err
		}
		if len(key) == ed25519.PublicKeySize {
			keys = append(keys, ed25519.PublicKey(key))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(keys) == 0 {
		return nil, "", ErrUnknownWorker
	}
	return keys, state, nil
}

// RotateKey installs nextPub as the worker's new key, keeping the old one
// valid for the overlap window, and clears any pending rotation demand.
func (s *Store) RotateKey(ctx context.Context, workerID string, nextPub ed25519.PublicKey, overlap time.Duration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update registry.worker_key
		set valid_until = now() + $2
		where worker_id = $1 and revoked_at is null and valid_until is null`,
		workerID, overlap); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into registry.worker_key (worker_id, public_key)
		values ($1, $2)`, workerID, []byte(nextPub)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update registry.worker
		set config = config - 'rotate_requested' where id = $1`, workerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into registry.trust_event (worker_id, event_type, actor)
		values ($1, 'key_rotated', 'worker')`, workerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestRotation flags the worker so its next heartbeat carries the
// rotate_key control action.
func (s *Store) RequestRotation(ctx context.Context, workerID string) error {
	ct, err := s.Pool.Exec(ctx, `update registry.worker
		set config = config || '{"rotate_requested": true}' where id = $1`, workerID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrUnknownWorker
	}
	return nil
}

// SeenNonce records a request nonce; returns true if it was already seen
// inside the replay window (a replay).
func (s *Store) SeenNonce(ctx context.Context, workerID, nonce string) (bool, error) {
	ct, err := s.Pool.Exec(ctx, `insert into registry.replay_nonce (worker_id, nonce)
		values ($1, $2) on conflict do nothing`, workerID, []byte(nonce))
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 0, nil
}

// PruneNonces drops nonces older than the replay window.
func (s *Store) PruneNonces(ctx context.Context, window time.Duration) (int64, error) {
	ct, err := s.Pool.Exec(ctx,
		`delete from registry.replay_nonce where seen_at < now() - $1`, window)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// IngestEnrollment provisions a worker created on VPS Advisor: the worker
// row (with the advisor's worker ID, keeping identities aligned across
// systems) plus the one-time token hash. Idempotent; returns whether a new
// worker was created.
func (s *Store) IngestEnrollment(ctx context.Context, workerID, operatorID, name, tokenHashHex string, expiresAt time.Time) (bool, error) {
	hash, err := hex.DecodeString(tokenHashHex)
	if err != nil || len(hash) != sha256.Size {
		return false, fmt.Errorf("token_hash must be hex sha256")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `insert into registry.worker (id, operator_id, name)
		values ($1, $2, $3) on conflict (id) do nothing`, workerID, operatorID, name)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `insert into registry.enrollment_token (token_hash, worker_id, expires_at)
		values ($1, $2, $3) on conflict (token_hash) do nothing`, hash, workerID, expiresAt); err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, tx.Commit(ctx)
}

// ApplyDecision applies a VPS Advisor admin decision, ignoring no-ops (the
// decision feed is replayed with overlap).
func (s *Store) ApplyDecision(ctx context.Context, workerID, state, reason string) error {
	var current string
	err := s.Pool.QueryRow(ctx, `select state from registry.worker where id = $1`,
		workerID).Scan(&current)
	if err != nil {
		return ErrUnknownWorker
	}
	if current == state {
		return nil
	}
	return s.SetState(ctx, workerID, state, reason, "advisor-admin")
}

// RecordTrustEvent appends a discrete trust-affecting event.
func (s *Store) RecordTrustEvent(ctx context.Context, workerID, eventType, actor string) {
	_, _ = s.Pool.Exec(ctx, `insert into registry.trust_event (worker_id, event_type, actor)
		values ($1, $2, $3)`, workerID, eventType, actor)
}

// List returns all workers, newest first (admin surface).
func (s *Store) List(ctx context.Context) ([]Worker, error) {
	rows, err := s.Pool.Query(ctx, `select id, name, state, coalesce(state_reason,''),
		coalesce(software_version,''), last_heartbeat_at, config
		from registry.worker order by enrolled_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var w Worker
		if err := rows.Scan(&w.ID, &w.Name, &w.State, &w.StateReason,
			&w.SoftwareVersion, &w.LastHeartbeat, &w.Config); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

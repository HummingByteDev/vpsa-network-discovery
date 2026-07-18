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
	"pending":     {"active", "retired"},
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

// ActiveKey returns the worker's current public key and state for request
// verification. Retired/revoked keys never verify.
func (s *Store) ActiveKey(ctx context.Context, workerID string) (ed25519.PublicKey, string, error) {
	var key []byte
	var state string
	err := s.Pool.QueryRow(ctx, `
		select k.public_key, w.state
		from registry.worker w
		join registry.worker_key k on k.worker_id = w.id
		where w.id = $1 and k.revoked_at is null and k.valid_until is null`,
		workerID).Scan(&key, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrUnknownWorker
	}
	if err != nil {
		return nil, "", err
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, "", fmt.Errorf("stored key has wrong size %d", len(key))
	}
	return ed25519.PublicKey(key), state, nil
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

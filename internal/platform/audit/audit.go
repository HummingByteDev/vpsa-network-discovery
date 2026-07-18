// Package audit appends to the append-only audit.event log. Failures are
// logged, never fatal — an audit outage must not take the platform down, but
// it must be loud.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Logger struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

// Event records one audit entry. detail must marshal to JSON (nil is fine).
// A nil Logger discards events (tests, components without an audit sink).
func (a *Logger) Event(ctx context.Context, category, actor, action, subject string, detail any) {
	if a == nil {
		return
	}
	raw := []byte("{}")
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			raw = b
		}
	}
	if _, err := a.Pool.Exec(ctx, `insert into audit.event
		(category, actor, action, subject, detail) values ($1, $2, $3, $4, $5)`,
		category, actor, action, subject, raw); err != nil {
		a.Log.Error("AUDIT WRITE FAILED", "category", category, "action", action,
			"subject", subject, "error", err)
	}
}

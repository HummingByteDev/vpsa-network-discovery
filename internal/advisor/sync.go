package advisor

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncProviders pulls the monitored provider list and upserts the local
// cache (routing.provider / routing.asn), soft-delisting providers that
// disappeared upstream. Used by the builder before each snapshot and by the
// coordinator continuously — an opt-out on VPS Advisor drains assignments
// within one scheduler pass of the next sync. Returns ASN → provider_id.
func SyncProviders(ctx context.Context, pool *pgxpool.Pool, c *Client) (map[uint32]string, error) {
	providers, err := c.ListProviders(ctx, true)
	if err != nil {
		return nil, err
	}
	asnToProvider := map[uint32]string{}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	ids := make([]string, 0, len(providers))
	for _, p := range providers {
		ids = append(ids, p.ProviderID)
		if _, err := tx.Exec(ctx, `
			insert into routing.provider (provider_id, name, monitoring_enabled, priority, synced_at, delisted_at)
			values ($1, $2, $3, $4, $5, null)
			on conflict (provider_id) do update set
			  name = excluded.name, monitoring_enabled = excluded.monitoring_enabled,
			  priority = excluded.priority, synced_at = excluded.synced_at, delisted_at = null`,
			p.ProviderID, p.Name, p.MonitoringEnabled, p.Priority, now); err != nil {
			return nil, err
		}
		for _, asn := range p.ASNs {
			if existing, dup := asnToProvider[uint32(asn)]; dup && existing != p.ProviderID {
				// ASN claimed by two providers: an upstream data error the
				// invariants forbid us to resolve silently (docs/architecture/02).
				return nil, fmt.Errorf("ASN %d claimed by two providers (%s, %s)", asn, existing, p.ProviderID)
			}
			asnToProvider[uint32(asn)] = p.ProviderID
			if _, err := tx.Exec(ctx, `
				insert into routing.asn (asn, provider_id, synced_at) values ($1, $2, $3)
				on conflict (asn) do update set provider_id = excluded.provider_id, synced_at = excluded.synced_at`,
				asn, p.ProviderID, now); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		update routing.provider set delisted_at = now()
		where delisted_at is null and not (provider_id = any($1::text[]))`, ids); err != nil {
		return nil, err
	}
	return asnToProvider, tx.Commit(ctx)
}

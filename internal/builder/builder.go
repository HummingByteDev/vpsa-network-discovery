// Package builder implements the routing snapshot pipeline
// (docs/architecture/06-lifecycles.md §2): provider sync → MRT extraction →
// validation → GeoIP enrichment → PostgreSQL load → probe-target derivation →
// sanity gate → publish. Artifact export arrives in Phase 3.
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/bogon"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/geo"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/mrtreader"
)

type Config struct {
	BviewPath             string
	CityMMDB              string  // empty: skip geo enrichment
	MaxTargetsPerProvider int     // per address family
	SanityMaxDelta        float64 // max fractional prefix-count change vs previous
	SanityForce           bool    // publish even when the gate trips
	RetainSnapshots       int     // superseded snapshots to keep un-pruned
}

type Builder struct {
	cfg       Config
	pool      *pgxpool.Pool
	advisor   *advisor.Client
	publisher *artifact.Publisher // nil: skip artifact publication (dev-only)
	log       *slog.Logger
}

func New(cfg Config, pool *pgxpool.Pool, adv *advisor.Client, pub *artifact.Publisher, log *slog.Logger) *Builder {
	return &Builder{cfg: cfg, pool: pool, advisor: adv, publisher: pub, log: log}
}

// ErrSanityGate is returned when the new snapshot deviates too much from the
// previous one and SanityForce is not set. The snapshot row is left in state
// 'building' for admin review; the previous snapshot stays published.
var ErrSanityGate = fmt.Errorf("sanity gate tripped; snapshot held in 'building' (set CNIP_SANITY_FORCE=true to override)")

type prefixRow struct {
	prefix   netip.Prefix
	origin   uint32
	provider string
	flags    map[string]any
	geo      geo.Info
}

func (b *Builder) Run(ctx context.Context) error {
	start := time.Now()

	providers, err := b.advisor.ListProviders(ctx, true)
	if err != nil {
		return fmt.Errorf("provider sync: %w", err)
	}
	asnToProvider, err := b.syncProviders(ctx, providers)
	if err != nil {
		return fmt.Errorf("store providers: %w", err)
	}
	b.log.Info("provider sync complete", "providers", len(providers), "asns", len(asnToProvider))

	monitored := make(map[uint32]bool, len(asnToProvider))
	for asn := range asnToProvider {
		monitored[asn] = true
	}

	rows, stats, err := b.extract(monitored, asnToProvider)
	if err != nil {
		return fmt.Errorf("mrt extraction: %w", err)
	}
	b.log.Info("extraction complete",
		"rib_records", stats.Records, "matched", stats.MatchedRecords,
		"skipped", stats.Skipped, "prefix_rows", len(rows),
		"elapsed", time.Since(start).Round(time.Second).String())

	if b.cfg.CityMMDB != "" {
		if err := b.enrich(rows); err != nil {
			return fmt.Errorf("geo enrichment: %w", err)
		}
	} else {
		b.log.Warn("no GeoIP database configured; skipping enrichment")
	}

	snapshotID, version, err := b.load(ctx, rows, stats)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}
	targets, err := b.deriveTargets(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("derive targets: %w", err)
	}
	b.log.Info("snapshot loaded", "version", version, "prefixes", len(rows), "targets", targets)

	if err := b.sanityGate(ctx, snapshotID); err != nil {
		return err
	}
	if b.publisher != nil {
		if _, err := b.publisher.Publish(ctx, snapshotID, version); err != nil {
			return fmt.Errorf("artifact publication: %w", err)
		}
	} else {
		b.log.Warn("no artifact store configured; snapshot will not be distributable to workers")
	}
	if err := b.publish(ctx, snapshotID); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if b.publisher != nil {
		if err := b.publisher.SetCurrent(ctx, version); err != nil {
			return fmt.Errorf("set current pointer: %w", err)
		}
		if err := b.publisher.Prune(ctx, b.cfg.RetainSnapshots); err != nil {
			return fmt.Errorf("prune: %w", err)
		}
	}
	b.log.Info("snapshot published", "version", version,
		"elapsed", time.Since(start).Round(time.Second).String())
	return nil
}

// syncProviders upserts the provider/ASN cache and soft-deletes providers no
// longer returned by VPS Advisor. Returns ASN → provider_id for this run.
func (b *Builder) syncProviders(ctx context.Context, providers []advisor.Provider) (map[uint32]string, error) {
	asnToProvider := map[uint32]string{}
	tx, err := b.pool.Begin(ctx)
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
		where delisted_at is null and not (provider_id = any($1::uuid[]))`, ids); err != nil {
		return nil, err
	}
	return asnToProvider, tx.Commit(ctx)
}

// extract streams the bview and returns deduplicated monitored prefix rows.
func (b *Builder) extract(monitored map[uint32]bool, asnToProvider map[uint32]string) ([]*prefixRow, *mrtreader.Stats, error) {
	seen := map[string]*prefixRow{}
	bogonsDropped := 0
	stats, err := mrtreader.Stream(b.cfg.BviewPath, monitored, func(rec mrtreader.Record) error {
		if bogon.IsBogon(rec.Prefix) {
			bogonsDropped++
			return nil
		}
		for _, origin := range rec.Origins {
			if !monitored[origin] {
				continue
			}
			key := fmt.Sprintf("%s|%d", rec.Prefix, origin)
			if row, ok := seen[key]; ok {
				// Same prefix+origin in another record (e.g. add-path dump):
				// keep the higher visibility count.
				if pc := rec.PeerCounts[origin]; pc > row.flags["peer_count"].(int) {
					row.flags["peer_count"] = pc
				}
				continue
			}
			flags := map[string]any{"peer_count": rec.PeerCounts[origin]}
			if len(rec.Origins) > 1 {
				flags["moas"] = true
				others := make([]uint32, 0, len(rec.Origins)-1)
				for _, o := range rec.Origins {
					if o != origin {
						others = append(others, o)
					}
				}
				flags["other_origins"] = others
			}
			if bogon.TooLong(rec.Prefix) {
				flags["long_prefix"] = true
			}
			seen[key] = &prefixRow{
				prefix:   rec.Prefix,
				origin:   origin,
				provider: asnToProvider[origin],
				flags:    flags,
			}
		}
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	if bogonsDropped > 0 {
		b.log.Warn("dropped bogon announcements", "count", bogonsDropped)
	}
	rows := make([]*prefixRow, 0, len(seen))
	for _, r := range seen {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].prefix.Addr() != rows[j].prefix.Addr() {
			return rows[i].prefix.Addr().Less(rows[j].prefix.Addr())
		}
		return rows[i].prefix.Bits() < rows[j].prefix.Bits()
	})
	return rows, stats, nil
}

func (b *Builder) enrich(rows []*prefixRow) error {
	enricher, err := geo.Open(b.cfg.CityMMDB)
	if err != nil {
		return err
	}
	defer enricher.Close()
	hits := 0
	for _, r := range rows {
		r.geo = enricher.Lookup(r.prefix)
		if r.geo.OK {
			hits++
		}
	}
	b.log.Info("geo enrichment complete", "prefixes", len(rows), "resolved", hits)
	return nil
}

// load creates the snapshot row and bulk-inserts prefixes.
func (b *Builder) load(ctx context.Context, rows []*prefixRow, stats *mrtreader.Stats) (int64, string, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback(ctx)

	sourceTS := time.Unix(int64(stats.FirstTimestamp), 0).UTC()
	version := fmt.Sprintf("%s-%d", sourceTS.Format("20060102T1504Z"), time.Now().UnixMilli())
	v4, v6 := 0, 0
	asns := map[uint32]bool{}
	for _, r := range rows {
		if r.prefix.Addr().Is4() {
			v4++
		} else {
			v6++
		}
		asns[r.origin] = true
	}

	var snapshotID int64
	if err := tx.QueryRow(ctx, `
		insert into routing.snapshot (version, source_uri, source_timestamp, status,
		  asn_count, prefix_count_v4, prefix_count_v6, built_at)
		values ($1, $2, $3, 'building', $4, $5, $6, now())
		returning id`,
		version, b.cfg.BviewPath, sourceTS, len(asns), v4, v6).Scan(&snapshotID); err != nil {
		return 0, "", err
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"routing", "prefix"},
		[]string{"snapshot_id", "prefix", "origin_asn", "geo_country", "geo_city", "geo_lat", "geo_lon", "flags"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			r := rows[i]
			flags, err := json.Marshal(r.flags)
			if err != nil {
				return nil, err
			}
			var country, city any
			var lat, lon any
			if r.geo.OK {
				country, lat, lon = r.geo.Country, r.geo.Lat, r.geo.Lon
				if r.geo.City != "" {
					city = r.geo.City
				}
			}
			return []any{snapshotID, r.prefix.String(), int64(r.origin), country, city, lat, lon, flags}, nil
		}))
	if err != nil {
		return 0, "", fmt.Errorf("copy prefixes: %w", err)
	}
	return snapshotID, version, tx.Commit(ctx)
}

// deriveTargets picks one representative probeable address per prefix (first
// usable host), capped per provider and family, preferring the largest and
// most widely seen prefixes.
func (b *Builder) deriveTargets(ctx context.Context, snapshotID int64) (int64, error) {
	tag, err := b.pool.Exec(ctx, `
		with candidates as (
		  select p.snapshot_id, a.provider_id, p.id as prefix_id,
		         host(network(p.prefix))::inet + 1 as address,
		         'first usable address of ' || p.prefix::text as rationale,
		         masklen(p.prefix) as masklen,
		         coalesce((p.flags->>'peer_count')::int, 0) as peer_count,
		         p.prefix
		  from routing.prefix p
		  join routing.asn a on a.asn = p.origin_asn
		  where p.snapshot_id = $1
		    and coalesce((p.flags->>'long_prefix')::bool, false) = false
		), deduped as (
		  -- Overlapping announcements (covering + more-specific, or one MOAS
		  -- prefix per origin) share a first-usable address; keep one target
		  -- per address, preferring the least-specific covering prefix.
		  select distinct on (address) *
		  from candidates
		  order by address, masklen asc, peer_count desc, prefix_id asc
		), ranked as (
		  select *, row_number() over (
		    partition by provider_id, family(prefix)
		    order by masklen asc, peer_count desc, prefix asc
		  ) as rank
		  from deduped
		)
		insert into routing.probe_target (snapshot_id, provider_id, prefix_id, address, rationale)
		select snapshot_id, provider_id, prefix_id, address, rationale
		from ranked where rank <= $2`, snapshotID, b.cfg.MaxTargetsPerProvider)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// sanityGate compares prefix counts with the previous published snapshot and
// refuses to publish on a wild swing (route leak, truncated dump, bad sync).
func (b *Builder) sanityGate(ctx context.Context, snapshotID int64) error {
	var prevTotal, newTotal int
	err := b.pool.QueryRow(ctx, `
		select coalesce(prefix_count_v4,0) + coalesce(prefix_count_v6,0)
		from routing.snapshot where status = 'published'
		order by published_at desc limit 1`).Scan(&prevTotal)
	if err == pgx.ErrNoRows {
		return nil // first snapshot: nothing to compare against
	}
	if err != nil {
		return err
	}
	if err := b.pool.QueryRow(ctx, `
		select coalesce(prefix_count_v4,0) + coalesce(prefix_count_v6,0)
		from routing.snapshot where id = $1`, snapshotID).Scan(&newTotal); err != nil {
		return err
	}
	if prevTotal == 0 {
		return nil
	}
	delta := float64(newTotal-prevTotal) / float64(prevTotal)
	if delta < 0 {
		delta = -delta
	}
	if delta > b.cfg.SanityMaxDelta {
		b.log.Error("prefix count swing exceeds threshold",
			"previous", prevTotal, "new", newTotal,
			"delta", fmt.Sprintf("%.0f%%", delta*100),
			"threshold", fmt.Sprintf("%.0f%%", b.cfg.SanityMaxDelta*100),
			"forced", b.cfg.SanityForce)
		if !b.cfg.SanityForce {
			return ErrSanityGate
		}
	}
	return nil
}

// publish atomically marks the snapshot published and supersedes the previous one.
func (b *Builder) publish(ctx context.Context, snapshotID int64) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		update routing.snapshot set status = 'superseded'
		where status = 'published' and id <> $1`, snapshotID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update routing.snapshot set status = 'published', published_at = now()
		where id = $1`, snapshotID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

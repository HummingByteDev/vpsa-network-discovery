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
	"math"
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
	CityMMDB              string // empty: skip geo enrichment
	MaxTargetsPerProvider int    // per address family
	// MaxTargetsPerCountry caps how many targets one country may take from a
	// provider's budget before the next country is served. It is what makes a
	// multi-country provider measurable everywhere rather than only where its
	// largest announcements are.
	MaxTargetsPerCountry int
	SanityMaxDelta       float64 // max fractional prefix-count change vs previous
	SanityForce          bool    // publish even when the gate trips
	RetainSnapshots      int     // superseded snapshots to keep un-pruned
	// GeoSource replaces the GeoIP database at CityMMDB when set. Callers that
	// hold their own geographic source (and the pipeline tests, which place
	// address space deterministically) supply it; the builder command leaves
	// it nil and opens CityMMDB.
	GeoSource GeoSource
}

// GeoSource is everything the pipeline asks of a GeoIP database: the single
// country/city label carried per prefix, and the record-boundary split the
// address-space accounting needs. *geo.Enricher implements it.
type GeoSource interface {
	Lookup(netip.Prefix) geo.Info
	Ranges(netip.Prefix) []geo.Range
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
var ErrSanityGate = fmt.Errorf("sanity gate tripped; snapshot held in 'building' (set VAPN_SANITY_FORCE=true to override)")

type prefixRow struct {
	prefix   netip.Prefix
	origin   uint32
	provider string
	flags    map[string]any
	geo      geo.Info
}

func (b *Builder) Run(ctx context.Context) error {
	start := time.Now()

	asnToProvider, err := advisor.SyncProviders(ctx, b.pool, b.advisor)
	if err != nil {
		return fmt.Errorf("provider sync: %w", err)
	}
	b.log.Info("provider sync complete", "asns", len(asnToProvider))

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

	// One GeoIP handle serves both jobs: the per-prefix country/city label and
	// the record-boundary split the address-space accounting needs.
	src := b.cfg.GeoSource
	switch {
	case src != nil:
	case b.cfg.CityMMDB != "":
		enricher, err := geo.Open(b.cfg.CityMMDB)
		if err != nil {
			return fmt.Errorf("geo enrichment: %w", err)
		}
		defer enricher.Close()
		src = enricher
	default:
		b.log.Warn("no GeoIP database configured; skipping enrichment — " +
			"the published snapshot will carry no country distribution")
	}
	if src != nil {
		b.enrich(rows, src)
	}

	snapshotID, version, err := b.load(ctx, rows, stats)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}
	countries, err := b.loadDistribution(ctx, snapshotID, rows, src)
	if err != nil {
		return fmt.Errorf("country distribution: %w", err)
	}
	targets, err := b.deriveTargets(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("derive targets: %w", err)
	}
	if err := b.countTargetsByCountry(ctx, snapshotID); err != nil {
		return fmt.Errorf("country target counts: %w", err)
	}
	b.log.Info("snapshot loaded", "version", version, "prefixes", len(rows),
		"targets", targets, "country_rows", countries)

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

func (b *Builder) enrich(rows []*prefixRow, src GeoSource) {
	hits := 0
	for _, r := range rows {
		r.geo = src.Lookup(r.prefix)
		if r.geo.OK {
			hits++
		}
	}
	b.log.Info("geo enrichment complete", "prefixes", len(rows), "resolved", hits)
}

// loadDistribution computes each provider's country distribution from the
// deduplicated prefix set and stores it against the snapshot. Returns the
// number of (provider, country) rows written.
func (b *Builder) loadDistribution(ctx context.Context, snapshotID int64, rows []*prefixRow, src GeoSource) (int, error) {
	var r ranger
	if src != nil {
		r = src
	}
	byProvider := distribute(rows, r)

	written := 0
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	for provider, stats := range byProvider {
		pct := shares(stats)
		for code, s := range stats {
			if _, err := tx.Exec(ctx, `
				insert into routing.provider_geo
				  (snapshot_id, provider_id, country_code, country_name,
				   continent_code, continent_name, ipv4_addresses, ipv6_net64s,
				   ipv4_share, prefix_count_v4, prefix_count_v6)
				values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				on conflict (snapshot_id, provider_id, country_code) do update set
				  ipv4_addresses = excluded.ipv4_addresses,
				  ipv6_net64s = excluded.ipv6_net64s,
				  ipv4_share = excluded.ipv4_share,
				  prefix_count_v4 = excluded.prefix_count_v4,
				  prefix_count_v6 = excluded.prefix_count_v6`,
				snapshotID, provider, code, nullable(s.Name),
				nullable(s.ContinentCode), nullable(s.ContinentName),
				clampInt64(s.IPv4Addresses), clampInt64(s.IPv6Net64s),
				pct[code], s.PrefixCountV4, s.PrefixCountV6); err != nil {
				return 0, err
			}
			written++
		}
	}
	return written, tx.Commit(ctx)
}

// countTargetsByCountry records how many probe targets each country ended up
// with, so a consumer can tell "no measurements here" (targets exist, nobody
// probed them) from "nothing to measure here" (no targets at all).
func (b *Builder) countTargetsByCountry(ctx context.Context, snapshotID int64) error {
	_, err := b.pool.Exec(ctx, `
		update routing.provider_geo g set target_count = (
		  select count(*) from routing.probe_target t
		  where t.snapshot_id = g.snapshot_id
		    and t.provider_id = g.provider_id
		    and coalesce(nullif(t.geo_country, ''), 'ZZ') = g.country_code
		)
		where g.snapshot_id = $1`, snapshotID)
	return err
}

// nullable keeps empty strings out of the database as NULL, so "no name known"
// is distinguishable from "named the empty string".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// clampInt64 saturates an unsigned count into the signed range PostgreSQL
// stores. Only pathological IPv6 announcements can reach it.
func clampInt64(v uint64) int64 {
	if v > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v)
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
//
// Targets are spread across the provider's countries rather than taken in size
// order: the budget is filled country by country (every country's best target
// first, then every country's second-best, and so on), capped per country by
// MaxTargetsPerCountry. A provider whose largest announcements are all in one
// country is therefore still measured everywhere it has address space, which
// is what country-level verdicts require.
func (b *Builder) deriveTargets(ctx context.Context, snapshotID int64) (int64, error) {
	perCountry := b.cfg.MaxTargetsPerCountry
	if perCountry <= 0 {
		perCountry = b.cfg.MaxTargetsPerProvider
	}
	tag, err := b.pool.Exec(ctx, `
		with candidates as (
		  select p.snapshot_id, a.provider_id, p.id as prefix_id,
		         host(network(p.prefix))::inet + 1 as address,
		         'first usable address of ' || p.prefix::text as rationale,
		         masklen(p.prefix) as masklen,
		         coalesce((p.flags->>'peer_count')::int, 0) as peer_count,
		         p.prefix,
		         coalesce(nullif(p.geo_country, ''), 'ZZ') as country,
		         p.geo_city as city
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
		), per_country as (
		  select *, row_number() over (
		    partition by provider_id, family(prefix), country
		    order by masklen asc, peer_count desc, prefix asc
		  ) as country_rank
		  from deduped
		), ranked as (
		  select *, row_number() over (
		    partition by provider_id, family(prefix)
		    order by country_rank asc, masklen asc, peer_count desc, prefix asc
		  ) as rank
		  from per_country
		  where country_rank <= $3
		)
		insert into routing.probe_target
		  (snapshot_id, provider_id, prefix_id, address, rationale, geo_country, geo_city)
		select snapshot_id, provider_id, prefix_id, address, rationale, country, city
		from ranked where rank <= $2`,
		snapshotID, b.cfg.MaxTargetsPerProvider, perCountry)
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

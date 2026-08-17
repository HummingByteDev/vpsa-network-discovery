package aggregate

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// The provider status document pushed to VPS Advisor (contract A4).
//
// It answers three different questions, and keeps them apart on purpose:
//
//	global    how is this provider behaving overall?
//	regions   how is it behaving in each country it has address space in?
//	network   where is that address space, and how much of it is where?
//	networks  the individual monitored networks behind those numbers
//
// `network` is derived from BGP and GeoIP — it exists whether or not anyone
// ever probed the provider. `regions`/`networks` are measurements. A country
// can hold 60% of a provider's addresses and still have no measurement
// coverage; the document says so rather than blending the two into one number.
//
// The shape is additive: `provider_id`, `as_of` and `global` are byte-for-byte
// what earlier releases sent, so a consumer reading only those keeps working.

// unknownRegion is the region code for address space no GeoIP record places.
// It is reported as such and must never be rendered as a country.
const unknownRegion = "ZZ"

type statusDoc struct {
	ProviderID string       `json:"provider_id"`
	AsOf       time.Time    `json:"as_of"`
	Global     verdictDoc   `json:"global"`
	Regions    []regionDoc  `json:"regions"`
	Network    networkDoc   `json:"network"`
	Networks   []networkRow `json:"networks"`
}

type verdictDoc struct {
	Verdict    string         `json:"verdict"`
	Confidence float64        `json:"confidence"`
	Metrics    map[string]any `json:"metrics"`
}

type regionDoc struct {
	Region        string         `json:"region"` // country code, or "ZZ"
	CountryCode   string         `json:"country_code"`
	Country       string         `json:"country,omitempty"`
	ContinentCode string         `json:"continent_code,omitempty"`
	Continent     string         `json:"continent,omitempty"`
	Verdict       string         `json:"verdict"`
	Confidence    float64        `json:"confidence"`
	Metrics       map[string]any `json:"metrics"`
	Coverage      coverageDoc    `json:"coverage"`
	AsOf          time.Time      `json:"as_of"`
}

// coverageDoc separates "how much could be measured here" from "how much was".
// targets_total comes from the routing snapshot, the rest from measurements —
// so a consumer can tell an unmonitored country from a broken one.
type coverageDoc struct {
	TargetsTotal    int `json:"targets_total"`
	TargetsMeasured int `json:"targets_measured"`
	TargetsUp       int `json:"targets_up"`
	WorkerCount     int `json:"worker_count"`
}

type networkDoc struct {
	SnapshotVersion string       `json:"snapshot_version,omitempty"`
	AsOf            *time.Time   `json:"as_of,omitempty"`
	ASNs            []int64      `json:"asns"`
	IPv4Addresses   int64        `json:"ipv4_addresses"`
	IPv6Net64s      int64        `json:"ipv6_net64s"`
	PrefixCountV4   int          `json:"prefix_count_v4"`
	PrefixCountV6   int          `json:"prefix_count_v6"`
	Countries       []countryDoc `json:"countries"`
}

type countryDoc struct {
	CountryCode      string  `json:"country_code"`
	Country          string  `json:"country,omitempty"`
	ContinentCode    string  `json:"continent_code,omitempty"`
	Continent        string  `json:"continent,omitempty"`
	IPv4Addresses    int64   `json:"ipv4_addresses"`
	IPv4SharePct     float64 `json:"ipv4_share_pct"`
	IPv6Net64s       int64   `json:"ipv6_net64s"`
	PrefixCountV4    int     `json:"prefix_count_v4"`
	PrefixCountV6    int     `json:"prefix_count_v6"`
	MonitoredTargets int     `json:"monitored_targets"`
}

// networkRow is one monitored network: an announced prefix, where it is, and
// how the address probed inside it is behaving. Only prefixes that carry a
// probe target appear — the complete announced footprint is in
// network.countries.
type networkRow struct {
	Prefix         string     `json:"prefix,omitempty"`
	OriginASN      *int64     `json:"origin_asn,omitempty"`
	Target         string     `json:"target"`
	CountryCode    string     `json:"country_code"`
	Country        string     `json:"country,omitempty"`
	ContinentCode  string     `json:"continent_code,omitempty"`
	Continent      string     `json:"continent,omitempty"`
	City           string     `json:"city,omitempty"`
	Verdict        string     `json:"verdict"`
	Availability   *float64   `json:"availability,omitempty"`
	LossRate       *float64   `json:"loss_rate,omitempty"`
	RTTp50Millis   *float64   `json:"rtt_p50_ms,omitempty"`
	RTTp95Millis   *float64   `json:"rtt_p95_ms,omitempty"`
	WorkerCount    int        `json:"worker_count"`
	LastMeasuredAt *time.Time `json:"last_measured_at,omitempty"`
}

// countryMeta is the naming/geography of a country, looked up once per
// provider from the routing snapshot and shared by regions and networks.
type countryMeta struct {
	name, continentCode, continentName string
	targets                            int
}

// statusDocument assembles the document for one provider from its settled
// windows, the published snapshot's geography, and current per-target health.
func (e *Engine) statusDocument(ctx context.Context, provider string, wins []winRow, global winRow) ([]byte, error) {
	network, meta, err := e.networkFootprint(ctx, provider)
	if err != nil {
		return nil, err
	}
	doc := statusDoc{
		ProviderID: provider,
		AsOf:       global.windowStart,
		Global: verdictDoc{
			Verdict: global.verdict, Confidence: global.confidence,
			Metrics: global.metrics(),
		},
		Regions:  make([]regionDoc, 0, len(wins)),
		Network:  network,
		Networks: nil,
	}
	for _, w := range wins {
		if w.region == "global" {
			continue
		}
		r := regionDoc{
			Region: w.region, CountryCode: w.region,
			Verdict: w.verdict, Confidence: w.confidence,
			Metrics: w.metrics(),
			Coverage: coverageDoc{
				TargetsMeasured: detailInt(w.detail, "measured_targets"),
				TargetsUp:       detailInt(w.detail, "up_targets"),
				WorkerCount:     w.workerCount,
			},
			AsOf: w.windowStart,
		}
		if m, ok := meta[w.region]; ok {
			r.Country, r.ContinentCode, r.Continent = m.name, m.continentCode, m.continentName
			r.Coverage.TargetsTotal = m.targets
		}
		if r.Country == "" && w.region == unknownRegion {
			r.Country = "Unknown"
		}
		doc.Regions = append(doc.Regions, r)
	}
	sort.Slice(doc.Regions, func(i, j int) bool { return doc.Regions[i].Region < doc.Regions[j].Region })

	networks, err := e.monitoredNetworks(ctx, provider, meta)
	if err != nil {
		return nil, err
	}
	doc.Networks = networks
	return json.Marshal(doc)
}

// networkFootprint reads the provider's country distribution from the
// published snapshot. A provider with no snapshot data yet gets an empty
// footprint rather than an error: measurements still publish.
func (e *Engine) networkFootprint(ctx context.Context, provider string) (networkDoc, map[string]countryMeta, error) {
	doc := networkDoc{ASNs: []int64{}, Countries: []countryDoc{}}
	meta := map[string]countryMeta{}

	var snapshotID *int64
	var version *string
	var publishedAt *time.Time
	if err := e.Pool.QueryRow(ctx, `select id, version, published_at
		from routing.snapshot where status = 'published'
		order by published_at desc limit 1`).Scan(&snapshotID, &version, &publishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return doc, meta, nil
		}
		return doc, meta, err
	}
	if version != nil {
		doc.SnapshotVersion = *version
	}
	doc.AsOf = publishedAt

	asnRows, err := e.Pool.Query(ctx,
		`select asn from routing.asn where provider_id = $1 order by asn`, provider)
	if err != nil {
		return doc, meta, err
	}
	for asnRows.Next() {
		var asn int64
		if err := asnRows.Scan(&asn); err != nil {
			asnRows.Close()
			return doc, meta, err
		}
		doc.ASNs = append(doc.ASNs, asn)
	}
	asnRows.Close()
	if err := asnRows.Err(); err != nil {
		return doc, meta, err
	}

	rows, err := e.Pool.Query(ctx, `
		select country_code, coalesce(country_name, ''), coalesce(continent_code, ''),
		       coalesce(continent_name, ''), ipv4_addresses, ipv4_share, ipv6_net64s,
		       prefix_count_v4, prefix_count_v6, target_count
		from routing.provider_geo
		where snapshot_id = $1 and provider_id = $2
		order by ipv4_addresses desc, country_code`, *snapshotID, provider)
	if err != nil {
		return doc, meta, err
	}
	for rows.Next() {
		var c countryDoc
		if err := rows.Scan(&c.CountryCode, &c.Country, &c.ContinentCode, &c.Continent,
			&c.IPv4Addresses, &c.IPv4SharePct, &c.IPv6Net64s,
			&c.PrefixCountV4, &c.PrefixCountV6, &c.MonitoredTargets); err != nil {
			rows.Close()
			return doc, meta, err
		}
		if c.CountryCode == unknownRegion && c.Country == "" {
			c.Country = "Unknown"
		}
		doc.Countries = append(doc.Countries, c)
		doc.IPv4Addresses += c.IPv4Addresses
		doc.IPv6Net64s += c.IPv6Net64s
		doc.PrefixCountV4 += c.PrefixCountV4
		doc.PrefixCountV6 += c.PrefixCountV6
		meta[c.CountryCode] = countryMeta{
			name: c.Country, continentCode: c.ContinentCode,
			continentName: c.Continent, targets: c.MonitoredTargets,
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return doc, meta, err
	}

	// Coverage counts come from the snapshot's own targets rather than from the
	// distribution's target_count, so "how much could be measured here" stays
	// truthful for a region the distribution does not describe — a snapshot
	// built before geography existed, or one built without a GeoIP database,
	// where every target lands in ZZ. Otherwise the document would report more
	// targets measured than exist.
	tgt, err := e.Pool.Query(ctx, `
		select coalesce(nullif(geo_country, ''), $1), count(*)
		from routing.probe_target
		where snapshot_id = $2 and provider_id = $3
		group by 1`, unknownRegion, *snapshotID, provider)
	if err != nil {
		return doc, meta, err
	}
	defer tgt.Close()
	for tgt.Next() {
		var code string
		var n int
		if err := tgt.Scan(&code, &n); err != nil {
			return doc, meta, err
		}
		m := meta[code]
		m.targets = n
		meta[code] = m
	}
	return doc, meta, tgt.Err()
}

func (e *Engine) monitoredNetworks(ctx context.Context, provider string, meta map[string]countryMeta) ([]networkRow, error) {
	rows, err := e.Pool.Query(ctx, `
		select host(target), region, coalesce(prefix::text, ''), origin_asn,
		       coalesce(city, ''), verdict, availability, loss_rate,
		       rtt_p50, rtt_p95, worker_count, last_measured_at
		from aggregation.target_status
		where provider_id = $1
		order by region, prefix, target`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []networkRow{}
	for rows.Next() {
		var n networkRow
		if err := rows.Scan(&n.Target, &n.CountryCode, &n.Prefix, &n.OriginASN,
			&n.City, &n.Verdict, &n.Availability, &n.LossRate, &n.RTTp50Millis,
			&n.RTTp95Millis, &n.WorkerCount, &n.LastMeasuredAt); err != nil {
			return nil, err
		}
		if m, ok := meta[n.CountryCode]; ok {
			n.Country, n.ContinentCode, n.Continent = m.name, m.continentCode, m.continentName
		}
		if n.Country == "" && n.CountryCode == unknownRegion {
			n.Country = "Unknown"
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func detailInt(detail map[string]any, key string) int {
	if v, ok := detail[key].(float64); ok {
		return int(v)
	}
	return 0
}

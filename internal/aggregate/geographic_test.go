package aggregate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Country-level monitoring, end to end: a provider whose address space sits in
// four countries, measured differently in each, must produce four separate
// verdicts, a network distribution independent of those measurements, and one
// status document that keeps the two apart.

type geoPrefix struct {
	prefix, target, country, city string
	addresses                     int64
}

// countryNames mirrors what the builder stores alongside each country code.
var countryNames = map[string][2]string{
	"MD": {"Moldova", "Europe"},
	"NL": {"Netherlands", "Europe"},
	"RO": {"Romania", "Europe"},
	"BG": {"Bulgaria", "Europe"},
}

func seedSnapshot(t *testing.T, pool *pgxpool.Pool, prefixes []geoPrefix) {
	t.Helper()
	ctx := context.Background()
	var snapshotID int64
	if err := pool.QueryRow(ctx, `insert into routing.snapshot
		(version, source_uri, source_timestamp, status, asn_count,
		 prefix_count_v4, prefix_count_v6, built_at, published_at)
		values ('geo-test', 'test', now(), 'published', 1, $1, 0, now(), now())
		returning id`, len(prefixes)).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into routing.asn
		(asn, provider_id, registry_name, synced_at) values (64500, $1, 'Test', now())`,
		provider); err != nil {
		t.Fatal(err)
	}

	totals := map[string]int64{}
	for _, p := range prefixes {
		totals[p.country] += p.addresses
	}
	var total int64
	for _, n := range totals {
		total += n
	}
	for _, p := range prefixes {
		var prefixID int64
		if err := pool.QueryRow(ctx, `insert into routing.prefix
			(snapshot_id, prefix, origin_asn, geo_country, geo_city)
			values ($1, $2, 64500, $3, $4) returning id`,
			snapshotID, p.prefix, p.country, p.city).Scan(&prefixID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into routing.probe_target
			(snapshot_id, provider_id, prefix_id, address, rationale, geo_country, geo_city)
			values ($1, $2, $3, $4, 'test', $5, $6)`,
			snapshotID, provider, prefixID, p.target, p.country, p.city); err != nil {
			t.Fatal(err)
		}
	}
	for code, n := range totals {
		meta := countryNames[code]
		if _, err := pool.Exec(ctx, `insert into routing.provider_geo
			(snapshot_id, provider_id, country_code, country_name, continent_code,
			 continent_name, ipv4_addresses, ipv4_share, prefix_count_v4, target_count)
			values ($1, $2, $3, $4, 'EU', $5, $6, $7, 1,
			  (select count(*) from routing.probe_target
			   where snapshot_id = $1 and geo_country = $3))`,
			snapshotID, provider, code, meta[0], meta[1], n,
			float64(n)/float64(total)*100); err != nil {
			t.Fatal(err)
		}
	}
}

func fixture() []geoPrefix {
	return []geoPrefix{
		{"185.0.0.0/16", "185.0.0.1", "MD", "Chisinau", 65536},
		{"185.1.0.0/24", "185.1.0.1", "NL", "Amsterdam", 256},
		{"185.2.0.0/24", "185.2.0.1", "RO", "Bucharest", 256},
		{"185.3.0.0/24", "185.3.0.1", "BG", "Sofia", 256},
	}
}

// setupGeo prepares the database and returns an engine plus three workers.
func setupGeo(t *testing.T) (*pgxpool.Pool, *Engine, []string) {
	t.Helper()
	p := setupDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		"truncate aggregation.target_status",
		"truncate routing.probe_target, routing.provider_geo, routing.prefix, routing.snapshot cascade",
	} {
		if _, err := p.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	seedSnapshot(t, p, fixture())
	e := engine(p, 3)
	// Per-network health is measured over a trailing window of wall-clock
	// time; these fixtures are anchored to a fixed past instant, so the window
	// is widened to reach them.
	e.Cfg.TargetWindow = 365 * 24 * time.Hour
	return p, e, seedWorkers(t, p, 3)
}

// responsive records that a target answered someone before the window, so it
// counts as a target that *should* answer rather than a dead address.
func responsive(t *testing.T, p *pgxpool.Pool, workers []string, target string) {
	t.Helper()
	ctx := context.Background()
	for _, w := range workers {
		if _, err := p.Exec(ctx, `insert into measurements.observation
			(worker_id, assignment_id, provider_id, target, probe_type, measured_at,
			 ok, rtt_ms, packets_sent, packets_lost, signature)
			values ($1, 1, $2, $3, 'icmp', $4, true, 20, 4, 0, 'sig')`,
			w, provider, target, windowStart.Add(-2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCountryVerdictsAreIndependent: one provider, four countries, four
// different measurement situations, four different answers — and a global
// verdict that does not hide any of them.
func TestCountryVerdictsAreIndependent(t *testing.T) {
	p, e, workers := setupGeo(t)
	ctx := context.Background()

	// Moldova: everyone sees it up.
	for _, w := range workers {
		observe(t, p, w, "185.0.0.1", true, 20, 6)
	}
	// Netherlands: previously responsive, now dark to everyone.
	responsive(t, p, workers, "185.1.0.1")
	for _, w := range workers {
		observe(t, p, w, "185.1.0.1", false, 0, 6)
	}
	// Romania: only one worker looked — not enough to call it anything.
	observe(t, p, workers[0], "185.2.0.1", true, 40, 6)
	// Bulgaria: address space and a target, but nobody probed it at all.

	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"MD":     "healthy",
		"NL":     "outage",
		"RO":     "insufficient_data",
		"global": "degraded", // 2 of 3 measured targets up
	}
	for region, verdict := range want {
		var got string
		var confidence float64
		if err := p.QueryRow(ctx, `select verdict, confidence
			from aggregation.provider_status where provider_id = $1 and region = $2`,
			provider, region).Scan(&got, &confidence); err != nil {
			t.Fatalf("no status for region %s: %v", region, err)
		}
		if got != verdict {
			t.Errorf("region %s verdict = %s, want %s", region, got, verdict)
		}
		if verdict == "insufficient_data" && confidence != 0 {
			t.Errorf("region %s confidence = %v, want 0", region, confidence)
		}
		if verdict == "healthy" && confidence <= 0 {
			t.Errorf("region %s confidence = %v, want > 0", region, confidence)
		}
	}

	// An unprobed country gets no verdict at all rather than a guessed one.
	var bg int
	if err := p.QueryRow(ctx, `select count(*) from aggregation.provider_status
		where provider_id = $1 and region = 'BG'`, provider).Scan(&bg); err != nil {
		t.Fatal(err)
	}
	if bg != 0 {
		t.Errorf("unprobed country produced %d status rows, want 0", bg)
	}

	// Regional latency is regional: Moldova's p50 must not be the global one.
	var mdP50, roP50 *float64
	if err := p.QueryRow(ctx, `select rtt_p50 from aggregation.consensus_window
		where provider_id = $1 and region = 'MD'`, provider).Scan(&mdP50); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `select rtt_p50 from aggregation.consensus_window
		where provider_id = $1 and region = 'RO'`, provider).Scan(&roP50); err != nil {
		t.Fatal(err)
	}
	if mdP50 == nil || roP50 == nil || *mdP50 != 20 || *roP50 != 40 {
		t.Errorf("regional p50 MD=%v RO=%v, want 20 and 40", mdP50, roP50)
	}
}

// TestStatusDocument checks the document VPS Advisor actually receives.
func TestStatusDocument(t *testing.T) {
	p, e, workers := setupGeo(t)
	ctx := context.Background()
	for _, w := range workers {
		observe(t, p, w, "185.0.0.1", true, 20, 6)
		observe(t, p, w, "185.1.0.1", true, 60, 6)
	}
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}

	var raw []byte
	if err := p.QueryRow(ctx, `select payload from aggregation.publication_outbox
		where kind = 'provider_status' order by id desc limit 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ProviderID string    `json:"provider_id"`
		AsOf       time.Time `json:"as_of"`
		Global     struct {
			Verdict    string             `json:"verdict"`
			Confidence float64            `json:"confidence"`
			Metrics    map[string]float64 `json:"metrics"`
		} `json:"global"`
		Regions []struct {
			Region     string  `json:"region"`
			Country    string  `json:"country"`
			Continent  string  `json:"continent"`
			Verdict    string  `json:"verdict"`
			Confidence float64 `json:"confidence"`
			Coverage   struct {
				TargetsTotal    int `json:"targets_total"`
				TargetsMeasured int `json:"targets_measured"`
			} `json:"coverage"`
			AsOf time.Time `json:"as_of"`
		} `json:"regions"`
		Network struct {
			SnapshotVersion string  `json:"snapshot_version"`
			ASNs            []int64 `json:"asns"`
			IPv4Addresses   int64   `json:"ipv4_addresses"`
			Countries       []struct {
				CountryCode      string  `json:"country_code"`
				Country          string  `json:"country"`
				IPv4Addresses    int64   `json:"ipv4_addresses"`
				IPv4SharePct     float64 `json:"ipv4_share_pct"`
				MonitoredTargets int     `json:"monitored_targets"`
			} `json:"countries"`
		} `json:"network"`
		Networks []struct {
			Prefix       string   `json:"prefix"`
			Target       string   `json:"target"`
			CountryCode  string   `json:"country_code"`
			City         string   `json:"city"`
			Verdict      string   `json:"verdict"`
			Availability *float64 `json:"availability"`
			RTTp50       *float64 `json:"rtt_p50_ms"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	// The pre-existing contract is untouched.
	if doc.ProviderID != provider {
		t.Errorf("provider_id = %q, want %q", doc.ProviderID, provider)
	}
	if !doc.AsOf.Equal(windowStart) {
		t.Errorf("as_of = %s, want %s", doc.AsOf, windowStart)
	}
	if doc.Global.Verdict != "healthy" || doc.Global.Confidence <= 0 {
		t.Errorf("global = %s/%v, want healthy with confidence", doc.Global.Verdict, doc.Global.Confidence)
	}
	if _, ok := doc.Global.Metrics["rtt_p50_ms"]; !ok {
		t.Errorf("global metrics lost rtt_p50_ms: %v", doc.Global.Metrics)
	}

	// Country monitoring.
	regions := map[string]int{}
	for i, r := range doc.Regions {
		regions[r.Region] = i
		if r.AsOf.IsZero() {
			t.Errorf("region %s has no timestamp", r.Region)
		}
	}
	md, ok := regions["MD"]
	if !ok {
		t.Fatalf("no Moldova region in %v", doc.Regions)
	}
	if doc.Regions[md].Country != "Moldova" || doc.Regions[md].Continent != "Europe" {
		t.Errorf("region naming = %+v, want Moldova/Europe", doc.Regions[md])
	}
	if doc.Regions[md].Verdict != "healthy" {
		t.Errorf("Moldova verdict = %s, want healthy", doc.Regions[md].Verdict)
	}
	if c := doc.Regions[md].Coverage; c.TargetsTotal != 1 || c.TargetsMeasured != 1 {
		t.Errorf("Moldova coverage = %+v, want 1 target measured of 1", c)
	}

	// Network distribution: present, complete, and independent of measurement.
	if doc.Network.SnapshotVersion != "geo-test" {
		t.Errorf("snapshot version = %q", doc.Network.SnapshotVersion)
	}
	if len(doc.Network.ASNs) != 1 || doc.Network.ASNs[0] != 64500 {
		t.Errorf("asns = %v, want [64500]", doc.Network.ASNs)
	}
	if doc.Network.IPv4Addresses != 65536+3*256 {
		t.Errorf("total IPv4 = %d, want %d", doc.Network.IPv4Addresses, 65536+3*256)
	}
	byCode := map[string]int{}
	for i, c := range doc.Network.Countries {
		byCode[c.CountryCode] = i
	}
	if len(byCode) != 4 {
		t.Fatalf("countries = %v, want all four", doc.Network.Countries)
	}
	mdNet := doc.Network.Countries[byCode["MD"]]
	if mdNet.Country != "Moldova" || mdNet.IPv4Addresses != 65536 {
		t.Errorf("Moldova network = %+v", mdNet)
	}
	if want := 65536.0 / float64(65536+3*256) * 100; mdNet.IPv4SharePct < want-0.01 || mdNet.IPv4SharePct > want+0.01 {
		t.Errorf("Moldova share = %v, want %v", mdNet.IPv4SharePct, want)
	}
	// Bulgaria: address space, a target, no measurements. Both facts survive.
	bg := doc.Network.Countries[byCode["BG"]]
	if bg.IPv4Addresses != 256 || bg.MonitoredTargets != 1 {
		t.Errorf("Bulgaria network = %+v, want 256 addresses and 1 target", bg)
	}
	if _, measured := regions["BG"]; measured {
		t.Error("Bulgaria has a monitoring verdict despite no measurements")
	}

	// Monitored networks: one row per probed network, with its place and health.
	if len(doc.Networks) != 2 {
		t.Fatalf("monitored networks = %d, want 2", len(doc.Networks))
	}
	for _, n := range doc.Networks {
		if n.Prefix == "" || n.Target == "" || n.CountryCode == "" || n.City == "" {
			t.Errorf("incomplete monitored network: %+v", n)
		}
		if n.Verdict != "healthy" || n.Availability == nil || *n.Availability != 1 {
			t.Errorf("network %s verdict/availability = %s/%v", n.Prefix, n.Verdict, n.Availability)
		}
		if n.RTTp50 == nil {
			t.Errorf("network %s has no latency", n.Prefix)
		}
	}
}

// A snapshot that carries no geography — one built before the distribution
// existed, or by a builder with no GeoIP database — puts every target in ZZ and
// describes no countries. The document must stay internally consistent: it may
// say "I do not know where this is", but it must never claim more targets were
// measured than the snapshot contains.
func TestSnapshotWithoutGeographyReportsHonestCoverage(t *testing.T) {
	p := setupDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		"truncate aggregation.target_status",
		"truncate routing.probe_target, routing.provider_geo, routing.prefix, routing.snapshot cascade",
	} {
		if _, err := p.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	var snapshotID int64
	if err := p.QueryRow(ctx, `insert into routing.snapshot
		(version, source_uri, source_timestamp, status, asn_count,
		 prefix_count_v4, prefix_count_v6, built_at, published_at)
		values ('pre-geo', 'test', now(), 'published', 1, 2, 0, now(), now())
		returning id`).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `insert into routing.asn
		(asn, provider_id, registry_name, synced_at) values (64500, $1, 'Test', now())`,
		provider); err != nil {
		t.Fatal(err)
	}
	for _, pfx := range []struct{ prefix, target string }{
		{"185.0.0.0/24", "185.0.0.1"},
		{"185.1.0.0/24", "185.1.0.1"},
	} {
		var prefixID int64
		if err := p.QueryRow(ctx, `insert into routing.prefix
			(snapshot_id, prefix, origin_asn) values ($1, $2, 64500) returning id`,
			snapshotID, pfx.prefix).Scan(&prefixID); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Exec(ctx, `insert into routing.probe_target
			(snapshot_id, provider_id, prefix_id, address, rationale)
			values ($1, $2, $3, $4, 'test')`,
			snapshotID, provider, prefixID, pfx.target); err != nil {
			t.Fatal(err)
		}
	}

	e := engine(p, 3)
	e.Cfg.TargetWindow = 365 * 24 * time.Hour
	for _, w := range seedWorkers(t, p, 3) {
		observe(t, p, w, "185.0.0.1", true, 20, 6)
		observe(t, p, w, "185.1.0.1", true, 30, 6)
	}
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := e.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}

	var raw []byte
	if err := p.QueryRow(ctx, `select payload from aggregation.publication_outbox
		where kind = 'provider_status' order by id desc limit 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Regions []struct {
			Region   string `json:"region"`
			Country  string `json:"country"`
			Coverage struct {
				TargetsTotal    int `json:"targets_total"`
				TargetsMeasured int `json:"targets_measured"`
			} `json:"coverage"`
		} `json:"regions"`
		Network struct {
			Countries []struct {
				CountryCode string `json:"country_code"`
			} `json:"countries"`
		} `json:"network"`
		Networks []struct {
			CountryCode string `json:"country_code"`
			Country     string `json:"country"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	if len(doc.Regions) != 1 || doc.Regions[0].Region != unknownRegion {
		t.Fatalf("regions = %+v, want a single ZZ region", doc.Regions)
	}
	if doc.Regions[0].Country != "Unknown" {
		t.Errorf("ZZ region country = %q, want Unknown", doc.Regions[0].Country)
	}
	if c := doc.Regions[0].Coverage; c.TargetsTotal != 2 || c.TargetsMeasured != 2 {
		t.Errorf("coverage = %+v, want 2 of 2 — measured must never exceed total", c)
	}
	// No geography must never be reported as a country.
	if len(doc.Network.Countries) != 0 {
		t.Errorf("network countries = %+v, want none", doc.Network.Countries)
	}
	for _, n := range doc.Networks {
		if n.CountryCode != unknownRegion || n.Country != "Unknown" {
			t.Errorf("monitored network placed at %q/%q, want ZZ/Unknown", n.CountryCode, n.Country)
		}
	}
}

// TestPartialCountryCoverage: half a country's targets answering is a degraded
// country, not a healthy one and not an outage.
func TestPartialCountryCoverage(t *testing.T) {
	p, e, workers := setupGeo(t)
	ctx := context.Background()
	// A second Moldovan target, both previously responsive; one now dark.
	var prefixID int64
	if err := p.QueryRow(ctx, `select id from routing.prefix where geo_country = 'MD'`).
		Scan(&prefixID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `insert into routing.probe_target
		(snapshot_id, provider_id, prefix_id, address, rationale, geo_country, geo_city)
		select snapshot_id, provider_id, prefix_id, '185.0.1.1', 'test', 'MD', 'Chisinau'
		from routing.probe_target where address = '185.0.0.1'`); err != nil {
		t.Fatal(err)
	}
	responsive(t, p, workers, "185.0.1.1")
	for _, w := range workers {
		observe(t, p, w, "185.0.0.1", true, 20, 6)
		observe(t, p, w, "185.0.1.1", false, 0, 6)
	}
	if err := e.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	var verdict string
	var detail []byte
	if err := p.QueryRow(ctx, `select verdict, detail from aggregation.consensus_window
		where provider_id = $1 and region = 'MD'`, provider).Scan(&verdict, &detail); err != nil {
		t.Fatal(err)
	}
	if verdict != "degraded" {
		t.Fatalf("half-dark country verdict = %s, want degraded", verdict)
	}
	var d struct {
		Measured int `json:"measured_targets"`
		Up       int `json:"up_targets"`
	}
	if err := json.Unmarshal(detail, &d); err != nil {
		t.Fatal(err)
	}
	if d.Measured != 2 || d.Up != 1 {
		t.Fatalf("coverage detail = %+v, want 2 measured / 1 up", d)
	}
}

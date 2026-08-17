// Package e2e drives the whole platform over one realistic provider: BGP
// announcements in, a published snapshot and a VPS Advisor status document
// out. It exists because the interesting failures of this pipeline are not
// inside any one component — they are the joins between them.
//
// The subject is AS200019 (AlexHost SRL), whose announced IPv4 space is spread
// across ten countries. The dataset is testdata/as200019.txt.
package e2e

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"github.com/osrg/gobgp/v3/pkg/packet/mrt"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/advisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/aggregate"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/builder"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/mockadvisor"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/platform/migrate"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/geo"
)

const (
	providerID = "alexhost-com"
	originASN  = 200019
)

var windowStart = time.Now().UTC().Add(-10 * time.Minute).Truncate(5 * time.Minute)

type netblock struct {
	prefix            netip.Prefix
	code, countryName string
}

func loadDataset(t *testing.T) []netblock {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "as200019.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []netblock
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			t.Fatalf("malformed dataset line: %q", line)
		}
		out = append(out, netblock{netip.MustParsePrefix(parts[0]), parts[1], parts[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// datasetGeo answers like a GeoIP database that places each announced netblock
// in the country the dataset records.
type datasetGeo map[netip.Prefix]geo.Info

func newDatasetGeo(blocks []netblock) datasetGeo {
	continents := map[string][2]string{
		"US": {"NA", "North America"}, "TR": {"AS", "Asia"},
	}
	g := datasetGeo{}
	for _, b := range blocks {
		continent := [2]string{"EU", "Europe"}
		if c, ok := continents[b.code]; ok {
			continent = c
		}
		g[b.prefix] = geo.Info{
			Country: b.code, CountryName: b.countryName,
			ContinentCode: continent[0], ContinentName: continent[1],
			City: b.countryName + " City", OK: true,
		}
	}
	return g
}

func (g datasetGeo) Ranges(p netip.Prefix) []geo.Range {
	// Every dataset netblock is announced whole, so a query either is one of
	// them or is covered by one.
	for rec, info := range g {
		if rec == p || (rec.Contains(p.Addr()) && rec.Bits() <= p.Bits()) {
			return []geo.Range{{Prefix: p, Info: info}}
		}
	}
	return nil
}

func (g datasetGeo) Lookup(p netip.Prefix) geo.Info {
	if r := g.Ranges(p); len(r) > 0 {
		return r[0].Info
	}
	return geo.Info{}
}

func writeBview(t *testing.T, blocks []netblock) string {
	t.Helper()
	var out []byte
	add := func(subtype mrt.MRTSubTypeTableDumpv2, body mrt.Body) {
		msg, err := mrt.NewMRTMessage(1752800000, mrt.TABLE_DUMPv2, subtype, body)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := msg.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw...)
	}
	attrs := []bgp.PathAttributeInterface{
		bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{
			bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, []uint32{65001, originASN}),
		}),
	}
	add(mrt.PEER_INDEX_TABLE, mrt.NewPeerIndexTable("10.0.0.0", "e2e",
		[]*mrt.Peer{mrt.NewPeer("10.0.0.1", "10.0.0.1", 65001, true)}))
	for i, b := range blocks {
		// Each netblock is announced by three different peers, the way a real
		// bview reports it. Deduplication must collapse them to one prefix.
		for peer := 0; peer < 3; peer++ {
			add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(uint32(i+1),
				bgp.NewIPAddrPrefix(uint8(b.prefix.Bits()), b.prefix.Addr().String()),
				[]*mrt.RibEntry{mrt.NewRibEntry(0, 1752800000, 0, attrs, false)}))
		}
	}
	path := filepath.Join(t.TempDir(), "bview.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write(out); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("VAPN_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("VAPN_TEST_DB_DSN not set; skipping end-to-end test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := migrate.Apply(ctx, pool, dir, discard()); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"truncate measurements.observation, measurements.upload_batch",
		`truncate aggregation.consensus_window, aggregation.provider_status,
		          aggregation.anomaly, aggregation.publication_outbox,
		          aggregation.worker_agreement, aggregation.target_status`,
		"truncate registry.worker cascade",
		`truncate routing.probe_target, routing.provider_geo, routing.prefix,
		          routing.snapshot, routing.asn, routing.provider cascade`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

// TestAS200019EndToEnd runs the documented pipeline in one go:
//
//	BGP prefixes → deduplication → IPv4 address-space accounting → GeoIP
//	enrichment → country distribution → country probe targets → worker probes
//	→ country aggregation → global aggregation → published document.
func TestAS200019EndToEnd(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	blocks := loadDataset(t)

	// --- the platform's view of VPS Advisor ---------------------------------
	fixtures, err := mockadvisor.LoadFixtures([]byte(`{"providers":[
		{"provider_id":"alexhost-com","name":"AlexHost SRL","asns":[200019],
		 "monitoring_enabled":true,"priority":10,"updated_at":"2026-08-01T00:00:00Z"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	site := httptest.NewServer(mockadvisor.NewServer(fixtures, "t", discard()))
	defer site.Close()

	// --- build ---------------------------------------------------------------
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := t.TempDir()
	pub := &artifact.Publisher{Pool: pool, Store: artifact.FSStore{Root: storeRoot},
		Key: signingKey, MinWorkerVersion: "0.1.0", Log: discard()}
	b := builder.New(builder.Config{
		BviewPath:             writeBview(t, blocks),
		MaxTargetsPerProvider: 100,
		MaxTargetsPerCountry:  10,
		SanityMaxDelta:        0.5,
		RetainSnapshots:       2,
		GeoSource:             newDatasetGeo(blocks),
	}, pool, advisor.New(site.URL, "t"), pub, discard())
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Deduplication: three peer observations per netblock, one prefix each.
	var prefixes int
	if err := pool.QueryRow(ctx, `select count(*) from routing.prefix`).Scan(&prefixes); err != nil {
		t.Fatal(err)
	}
	if prefixes != len(blocks) {
		t.Fatalf("stored prefixes = %d, want %d (one per announcement)", prefixes, len(blocks))
	}

	// --- country distribution ------------------------------------------------
	wantAddresses := map[string]int64{}
	var wantTotal int64
	for _, blk := range blocks {
		n := int64(1) << uint(32-blk.prefix.Bits())
		wantAddresses[blk.code] += n
		wantTotal += n
	}
	rows, err := pool.Query(ctx, `select country_code, ipv4_addresses, ipv4_share, target_count
		from routing.provider_geo where provider_id = $1`, providerID)
	if err != nil {
		t.Fatal(err)
	}
	gotAddresses := map[string]int64{}
	gotShare := map[string]float64{}
	gotTargets := map[string]int{}
	for rows.Next() {
		var code string
		var addresses int64
		var share float64
		var targets int
		if err := rows.Scan(&code, &addresses, &share, &targets); err != nil {
			t.Fatal(err)
		}
		gotAddresses[code], gotShare[code], gotTargets[code] = addresses, share, targets
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(gotAddresses) != len(wantAddresses) {
		t.Fatalf("countries = %v, want %v", gotAddresses, wantAddresses)
	}
	var total int64
	for code, want := range wantAddresses {
		if gotAddresses[code] != want {
			t.Errorf("%s addresses = %d, want %d", code, gotAddresses[code], want)
		}
		total += gotAddresses[code]
		if wantPct := float64(want) / float64(wantTotal) * 100; !near(gotShare[code], wantPct, 0.01) {
			t.Errorf("%s share = %.2f%%, want %.2f%%", code, gotShare[code], wantPct)
		}
		// Every country must be measurable, not just the biggest.
		if gotTargets[code] == 0 {
			t.Errorf("%s has address space but no probe target", code)
		}
	}
	if total != wantTotal {
		t.Errorf("total IPv4 = %d, want %d", total, wantTotal)
	}
	// Moldova is where most of this network lives; the share must reflect
	// address space, not the number of prefixes (the UK announces four /22s
	// against Moldova's many /24s).
	if gotShare["MD"] < gotShare["GB"] {
		t.Errorf("MD share %.2f%% below GB %.2f%% — shares are not address-weighted",
			gotShare["MD"], gotShare["GB"])
	}

	// --- measure -------------------------------------------------------------
	workers := make([]string, 3)
	for i := range workers {
		workers[i] = uuid.NewString()
		if _, err := pool.Exec(ctx, `insert into registry.worker
			(id, operator_id, name, state, approved_at, last_heartbeat_at)
			values ($1, $2, $3, 'active', now() - interval '60 days', now())`,
			workers[i], uuid.NewString(), "e2e-worker"); err != nil {
			t.Fatal(err)
		}
	}
	targetRows, err := pool.Query(ctx,
		`select host(address), geo_country from routing.probe_target where provider_id = $1`,
		providerID)
	if err != nil {
		t.Fatal(err)
	}
	type target struct{ addr, country string }
	var targets []target
	for targetRows.Next() {
		var tg target
		if err := targetRows.Scan(&tg.addr, &tg.country); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, tg)
	}
	targetRows.Close()
	if len(targets) == 0 {
		t.Fatal("no probe targets derived")
	}

	// Every target answers, except Bulgaria's, which went dark after having
	// answered earlier — an outage in one country, not everywhere.
	for _, tg := range targets {
		for _, w := range workers {
			if tg.country == "BG" {
				observe(t, pool, w, tg.addr, windowStart.Add(-2*time.Hour), true, 30)
			}
			for i := 0; i < 4; i++ {
				at := windowStart.Add(time.Duration(i*10) * time.Second)
				observe(t, pool, w, tg.addr, at, tg.country != "BG", 30)
			}
		}
	}

	engine := &aggregate.Engine{Pool: pool, Log: discard(),
		Cfg: aggregate.Config{WindowSeconds: 300, MinWorkers: 3}}
	if err := engine.ComputeWindow(ctx, windowStart); err != nil {
		t.Fatal(err)
	}
	if err := engine.RollupStatus(ctx); err != nil {
		t.Fatal(err)
	}

	// --- the document VPS Advisor receives -----------------------------------
	var raw []byte
	if err := pool.QueryRow(ctx, `select payload from aggregation.publication_outbox
		where kind = 'provider_status' order by id desc limit 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ProviderID string    `json:"provider_id"`
		AsOf       time.Time `json:"as_of"`
		Global     struct {
			Verdict string             `json:"verdict"`
			Metrics map[string]float64 `json:"metrics"`
		} `json:"global"`
		Regions []struct {
			Region    string `json:"region"`
			Country   string `json:"country"`
			Continent string `json:"continent"`
			Verdict   string `json:"verdict"`
			Coverage  struct {
				TargetsTotal    int `json:"targets_total"`
				TargetsMeasured int `json:"targets_measured"`
			} `json:"coverage"`
		} `json:"regions"`
		Network struct {
			ASNs      []int64 `json:"asns"`
			Countries []struct {
				CountryCode  string  `json:"country_code"`
				Country      string  `json:"country"`
				IPv4SharePct float64 `json:"ipv4_share_pct"`
			} `json:"countries"`
		} `json:"network"`
		Networks []struct {
			Prefix      string `json:"prefix"`
			CountryCode string `json:"country_code"`
			Verdict     string `json:"verdict"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	if doc.ProviderID != providerID {
		t.Errorf("provider_id = %q, want %q", doc.ProviderID, providerID)
	}
	if !doc.AsOf.Equal(windowStart) {
		t.Errorf("as_of = %s, want %s", doc.AsOf, windowStart)
	}
	if len(doc.Network.ASNs) != 1 || doc.Network.ASNs[0] != originASN {
		t.Errorf("asns = %v, want [%d]", doc.Network.ASNs, originASN)
	}
	if len(doc.Network.Countries) != len(wantAddresses) {
		t.Errorf("document lists %d countries, want %d", len(doc.Network.Countries), len(wantAddresses))
	}
	verdicts := map[string]string{}
	for _, r := range doc.Regions {
		verdicts[r.Region] = r.Verdict
		if r.Country == "" || r.Continent == "" {
			t.Errorf("region %s is unnamed: %+v", r.Region, r)
		}
		if r.Coverage.TargetsTotal == 0 {
			t.Errorf("region %s reports no targets", r.Region)
		}
	}
	if verdicts["MD"] != "healthy" {
		t.Errorf("Moldova verdict = %q, want healthy", verdicts["MD"])
	}
	if verdicts["BG"] != "outage" {
		t.Errorf("Bulgaria verdict = %q, want outage", verdicts["BG"])
	}
	// One country dark out of ten is a degraded provider, not a dead one.
	if doc.Global.Verdict != "degraded" {
		t.Errorf("global verdict = %q, want degraded", doc.Global.Verdict)
	}
	if _, ok := doc.Global.Metrics["rtt_p50_ms"]; !ok {
		t.Errorf("global metrics missing latency: %v", doc.Global.Metrics)
	}
	if len(doc.Networks) != len(targets) {
		t.Errorf("monitored networks = %d, want %d", len(doc.Networks), len(targets))
	}
	for _, n := range doc.Networks {
		if n.Prefix == "" || n.CountryCode == "" {
			t.Errorf("monitored network without identity: %+v", n)
		}
	}
}

func observe(t *testing.T, pool *pgxpool.Pool, worker, target string, at time.Time, ok bool, rtt float64) {
	t.Helper()
	var rttArg any
	lost := 4
	if ok {
		rttArg, lost = rtt, 0
	}
	if _, err := pool.Exec(context.Background(), `insert into measurements.observation
		(worker_id, assignment_id, provider_id, target, probe_type, measured_at,
		 ok, rtt_ms, packets_sent, packets_lost, signature)
		values ($1, 1, $2, $3, 'icmp', $4, $5, $6, 4, $7, 'sig')`,
		worker, providerID, target, at, ok, rttArg, lost); err != nil {
		t.Fatal(err)
	}
}

func near(got, want, tolerance float64) bool {
	d := got - want
	return d < tolerance && d > -tolerance
}

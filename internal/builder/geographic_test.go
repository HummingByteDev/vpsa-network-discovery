package builder

import (
	"context"
	"database/sql"
	"net/netip"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/artifact"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/geo"
)

// End-to-end geography against a real database: from the bview fixture's
// prefixes to a stored country distribution, targets spread across those
// countries, and an artifact that carries them.
//
// The fixture bview gives provider A (64500) 185.0.0.0/16, 185.0.0.0/17 and
// the MOAS 185.1.0.0/24, and provider B (64501) 185.2.0.0/22, a /25 that is
// too long to probe, and 2001:41d0::/32.

// placedGeo is a GeoSource that puts the fixture's address space in three
// countries, including one announcement split across two of them.
type placedGeo struct{ fakeRanger }

func (p placedGeo) Lookup(pfx netip.Prefix) geo.Info {
	// The per-prefix label is the record covering its first address, which is
	// exactly what the production enricher does.
	if r := p.Ranges(netip.PrefixFrom(pfx.Addr(), pfx.Addr().BitLen())); len(r) > 0 {
		info := r[0].Info
		info.City = cityFor(info.Country)
		return info
	}
	return geo.Info{}
}

func cityFor(code string) string {
	switch code {
	case "MD":
		return "Chisinau"
	case "NL":
		return "Amsterdam"
	case "RO":
		return "Bucharest"
	}
	return ""
}

func placedGeoSource() placedGeo {
	return placedGeo{fakeRanger{
		// Provider A's /16: three quarters Moldova, one quarter Netherlands.
		"185.0.0.0/18":   country("MD", "Moldova", "EU"),
		"185.0.64.0/18":  country("MD", "Moldova", "EU"),
		"185.0.128.0/17": country("NL", "Netherlands", "EU"),
		"185.1.0.0/24":   country("NL", "Netherlands", "EU"),
		// Provider B.
		"185.2.0.0/22":   country("RO", "Romania", "EU"),
		"185.3.0.0/25":   country("RO", "Romania", "EU"),
		"2001:41d0::/32": country("RO", "Romania", "EU"),
	}}
}

const providerA = "11111111-1111-1111-1111-111111111111"

func TestCountryDistributionIsStored(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	env := newBuilder(t, pool, writeBview(t))
	env.b.cfg.GeoSource = placedGeoSource()
	env.b.cfg.MaxTargetsPerCountry = 1
	env.b.cfg.MaxTargetsPerProvider = 100
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Provider A announces 185.0.0.0/16 (with a nested /17 that must not be
	// counted again) plus the MOAS /24: 65 536 + 256 addresses, three quarters
	// of the /16 in Moldova and the rest in the Netherlands.
	type row struct {
		code   string
		v4     int64
		share  float64
		pfxV4  int
		target int
	}
	rows, err := pool.Query(ctx, `select country_code, ipv4_addresses, ipv4_share,
		prefix_count_v4, target_count from routing.provider_geo
		where provider_id = $1 order by country_code`, providerA)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.code, &r.v4, &r.share, &r.pfxV4, &r.target); err != nil {
			t.Fatal(err)
		}
		got[r.code] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("countries for provider A = %v, want MD and NL", got)
	}
	if got["MD"].v4 != 32768 { // two /18s
		t.Errorf("MD addresses = %d, want 32768", got["MD"].v4)
	}
	if got["NL"].v4 != 32768+256 { // the /17 plus the MOAS /24
		t.Errorf("NL addresses = %d, want %d", got["NL"].v4, 32768+256)
	}
	if total := got["MD"].v4 + got["NL"].v4; total != 65536+256 {
		t.Errorf("total addresses = %d, want %d — the nested /17 was counted twice",
			total, 65536+256)
	}
	wantShare := float64(32768) / float64(65536+256) * 100
	if d := got["MD"].share - wantShare; d > 0.001 || d < -0.001 {
		t.Errorf("MD share = %.3f, want %.3f", got["MD"].share, wantShare)
	}
	// Every country with address space must be measurable in it.
	if got["MD"].target < 1 || got["NL"].target < 1 {
		t.Errorf("targets per country MD=%d NL=%d, want at least one each",
			got["MD"].target, got["NL"].target)
	}
}

// A provider whose largest announcements are all in one country must still get
// a target in its smaller countries: country verdicts are impossible without.
//
// The fixture gives provider A two Moldovan announcements that outrank its
// single Dutch one on size, and a budget of two targets. Picking purely by
// size — what the builder used to do — puts both targets in Moldova and leaves
// the Netherlands permanently unmeasurable.
func TestTargetsSpreadAcrossCountries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	bview := writeBviewFrom(t, []bviewRecord{
		{"185.0.0.0/16", []uint32{64500}},   // Moldova, largest
		{"185.0.128.0/18", []uint32{64500}}, // Moldova, second largest
		{"185.1.0.0/24", []uint32{64500}},   // Netherlands, smallest
	})
	env := newBuilder(t, pool, bview)
	env.b.cfg.GeoSource = placedGeo{fakeRanger{
		"185.0.0.0/16": country("MD", "Moldova", "EU"),
		"185.1.0.0/24": country("NL", "Netherlands", "EU"),
	}}
	// A budget of two with room for two per country: only the country-by-country
	// fill order can produce coverage of both.
	env.b.cfg.MaxTargetsPerProvider = 2
	env.b.cfg.MaxTargetsPerCountry = 2
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `select geo_country, host(address)
		from routing.probe_target where provider_id = $1 order by address`, providerA)
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{}
	for rows.Next() {
		var c, addr string
		if err := rows.Scan(&c, &addr); err != nil {
			t.Fatal(err)
		}
		targets[c] = addr
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets["MD"] == "" || targets["NL"] == "" {
		t.Fatalf("targets by country = %v, want one in MD and one in NL", targets)
	}
}

// A country the GeoIP database cannot place is reported as ZZ, never as a
// country and never silently dropped from the totals.
func TestUnplacedSpaceIsStoredAsUnknown(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	env := newBuilder(t, pool, writeBview(t)) // no geo override: nothing is placed
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var code string
	var v4 int64
	if err := pool.QueryRow(ctx, `select country_code, ipv4_addresses
		from routing.provider_geo where provider_id = $1`, providerA).Scan(&code, &v4); err != nil {
		t.Fatal(err)
	}
	if code != "ZZ" || v4 != 65536+256 {
		t.Fatalf("unplaced distribution = %s/%d, want ZZ/%d", code, v4, 65536+256)
	}
}

// The artifact workers download carries the country/city labels too, added as
// new columns so an older worker reading the old ones is unaffected.
func TestArtifactCarriesGeography(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	env := newBuilder(t, pool, writeBview(t))
	env.b.cfg.GeoSource = placedGeoSource()
	if err := env.b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	ptr := readPointer(t, env.storeRoot)
	db, err := sql.Open("sqlite", filepath.Join(env.storeRoot,
		filepath.FromSlash(artifact.ObjectKeySQLite(ptr.Version))))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var country, city string
	if err := db.QueryRow(`select geo_country, geo_city from prefixes
		where prefix = '185.0.0.0/16'`).Scan(&country, &city); err != nil {
		t.Fatal(err)
	}
	if country != "MD" || city != "Chisinau" {
		t.Fatalf("prefix geography = %s/%s, want MD/Chisinau", country, city)
	}
	var targetsWithCountry int
	if err := db.QueryRow(
		`select count(*) from targets where geo_country is not null`).Scan(&targetsWithCountry); err != nil {
		t.Fatal(err)
	}
	if targetsWithCountry == 0 {
		t.Fatal("no target carries a country")
	}
	// The pre-existing contract still holds for workers that ignore geography.
	var one int
	if err := db.QueryRow(
		`select 1 from targets where address = '185.0.0.1'`).Scan(&one); err != nil {
		t.Fatalf("worker's target lookup broke: %v", err)
	}
}

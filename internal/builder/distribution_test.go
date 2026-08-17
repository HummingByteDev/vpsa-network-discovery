package builder

import (
	"net/netip"
	"testing"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/geo"
)

// Address-space accounting is the number VPS Advisor renders, so these tests
// pin down the two ways it can go wrong: counting the same address twice, and
// counting prefixes instead of addresses.

// fakeRanger stands in for a GeoIP database whose records are the prefixes it
// was given, so a test can place address space precisely — including a single
// announcement that spans two countries.
//
// It answers the way a real MMDB does: a partition of the queried prefix into
// **disjoint** pieces, each labelled by the most specific record covering it,
// with space no record covers simply absent.
type fakeRanger map[string]geo.Info

func (f fakeRanger) Ranges(p netip.Prefix) []geo.Range {
	return f.split(p.Masked())
}

func (f fakeRanger) split(p netip.Prefix) []geo.Range {
	var (
		best     = -1
		bestInfo geo.Info
		deeper   bool
	)
	for cidr, info := range f {
		rec := netip.MustParsePrefix(cidr)
		switch {
		case rec.Bits() <= p.Bits() && rec.Contains(p.Addr()):
			if rec.Bits() > best {
				best, bestInfo = rec.Bits(), info
			}
		case rec.Bits() > p.Bits() && p.Contains(rec.Addr()):
			deeper = true
		}
	}
	switch {
	case !deeper && best >= 0:
		return []geo.Range{{Prefix: p, Info: bestInfo}}
	case !deeper:
		return nil // no record covers this space: unattributed
	}
	lo := netip.PrefixFrom(p.Addr(), p.Bits()+1)
	hi := netip.PrefixFrom(setBit(p.Addr(), p.Bits()), p.Bits()+1)
	return append(f.split(lo), f.split(hi)...)
}

// setBit returns addr with bit index n (from the most significant bit) set.
func setBit(addr netip.Addr, n int) netip.Addr {
	b := addr.AsSlice()
	b[n/8] |= 1 << (7 - uint(n%8))
	out, _ := netip.AddrFromSlice(b)
	return out
}

func country(code, name, continent string) geo.Info {
	return geo.Info{Country: code, CountryName: name, ContinentCode: continent,
		ContinentName: "Europe", OK: true}
}

func rows(provider string, prefixes ...string) []*prefixRow {
	out := make([]*prefixRow, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, &prefixRow{prefix: netip.MustParsePrefix(p), provider: provider})
	}
	return out
}

func TestUnitsCountAddressSpaceNotPrefixes(t *testing.T) {
	cases := []struct {
		prefix string
		want   uint64
	}{
		{"192.0.2.0/24", 256},
		{"10.0.0.0/20", 4096},
		{"10.0.0.0/16", 65536},
		{"10.0.0.0/8", 16777216},
		{"2001:db8::/32", 1 << 32}, // /64s, not addresses
		{"2001:db8::/48", 65536},
		{"2001:db8::/64", 1},
		{"2001:db8::/96", 0}, // longer than a /64: no countable /64s
	}
	for _, c := range cases {
		if got := units(netip.MustParsePrefix(c.prefix)); got != c.want {
			t.Errorf("units(%s) = %d, want %d", c.prefix, got, c.want)
		}
	}
}

// A /20 is sixteen /24s: a country holding one /20 must outweigh a country
// holding one /24, which counting prefixes would get backwards.
func TestSharesUseAddressCountsNotPrefixCounts(t *testing.T) {
	r := fakeRanger{
		"185.0.0.0/20": country("MD", "Moldova", "EU"),
		"185.1.0.0/24": country("NL", "Netherlands", "EU"),
	}
	stats := distribute(rows("p", "185.0.0.0/20", "185.1.0.0/24"), r)["p"]
	if stats["MD"].IPv4Addresses != 4096 || stats["NL"].IPv4Addresses != 256 {
		t.Fatalf("addresses MD=%d NL=%d, want 4096/256",
			stats["MD"].IPv4Addresses, stats["NL"].IPv4Addresses)
	}
	pct := shares(stats)
	if wantMD := 4096.0 / 4352 * 100; !closeTo(pct["MD"], wantMD) {
		t.Fatalf("MD share = %v, want %v", pct["MD"], wantMD)
	}
	if !closeTo(pct["MD"]+pct["NL"], 100) {
		t.Fatalf("shares sum to %v, want 100", pct["MD"]+pct["NL"])
	}
}

// The same prefix seen through several peers/paths reaches the builder as one
// row per origin; the same prefix announced by two ASNs of one provider is
// still one piece of address space.
func TestDuplicateAndMultiOriginPrefixesCountOnce(t *testing.T) {
	r := fakeRanger{"185.0.0.0/16": country("MD", "Moldova", "EU")}
	dup := rows("p", "185.0.0.0/16", "185.0.0.0/16")
	dup[1].origin = 64501 // same prefix, second ASN of the same provider
	stats := distribute(dup, r)["p"]
	if got := stats["MD"].IPv4Addresses; got != 65536 {
		t.Fatalf("addresses = %d, want 65536 (counted once)", got)
	}
}

// Nested announcements are the classic double-count: /16 + /17 + /24 is 65 536
// addresses, not 65 536 + 32 768 + 256.
func TestNestedPrefixesAreNotDoubleCounted(t *testing.T) {
	r := fakeRanger{"185.0.0.0/16": country("MD", "Moldova", "EU")}
	stats := distribute(rows("p", "185.0.0.0/16", "185.0.0.0/17", "185.0.5.0/24"), r)["p"]
	if got := stats["MD"].IPv4Addresses; got != 65536 {
		t.Fatalf("addresses = %d, want 65536", got)
	}
	// All three announcements still count as reaching into the country.
	if got := stats["MD"].PrefixCountV4; got != 3 {
		t.Fatalf("prefix count = %d, want 3", got)
	}
}

// A more-specific in a different country takes its space *from* the covering
// announcement rather than adding to the total.
func TestMoreSpecificMovesSpaceBetweenCountries(t *testing.T) {
	r := fakeRanger{
		"185.0.0.0/16": country("MD", "Moldova", "EU"),
		"185.0.7.0/24": country("NL", "Netherlands", "EU"),
	}
	stats := distribute(rows("p", "185.0.0.0/16", "185.0.7.0/24"), r)["p"]
	if stats["MD"].IPv4Addresses != 65536-256 || stats["NL"].IPv4Addresses != 256 {
		t.Fatalf("addresses MD=%d NL=%d, want %d/256",
			stats["MD"].IPv4Addresses, stats["NL"].IPv4Addresses, 65536-256)
	}
	var total uint64
	for _, s := range stats {
		total += s.IPv4Addresses
	}
	if total != 65536 {
		t.Fatalf("total addresses = %d, want 65536", total)
	}
}

// One announcement, two GeoIP records: the split follows the database's own
// boundaries instead of labelling the whole prefix from its first address.
func TestPrefixSpanningTwoCountriesSplitsAtRecordBoundaries(t *testing.T) {
	r := fakeRanger{
		"185.0.0.0/17":   country("MD", "Moldova", "EU"),
		"185.0.128.0/17": country("RO", "Romania", "EU"),
	}
	stats := distribute(rows("p", "185.0.0.0/16"), r)["p"]
	if stats["MD"].IPv4Addresses != 32768 || stats["RO"].IPv4Addresses != 32768 {
		t.Fatalf("addresses MD=%d RO=%d, want 32768 each",
			stats["MD"].IPv4Addresses, stats["RO"].IPv4Addresses)
	}
}

// Space the database does not place is reported as unknown — never folded into
// a real country, never dropped from the total.
func TestUnattributedSpaceIsReportedAsUnknown(t *testing.T) {
	r := fakeRanger{"185.0.0.0/17": country("MD", "Moldova", "EU")}
	stats := distribute(rows("p", "185.0.0.0/16"), r)["p"]
	if stats[unknownCountry] == nil || stats[unknownCountry].IPv4Addresses != 32768 {
		t.Fatalf("unknown space = %+v, want 32768 addresses", stats[unknownCountry])
	}
	if stats[unknownCountry].Name != "Unknown" {
		t.Fatalf("unknown country name = %q, want Unknown", stats[unknownCountry].Name)
	}

	// No GeoIP database at all: everything is unknown, nothing is invented.
	none := distribute(rows("p", "185.0.0.0/16"), nil)["p"]
	if len(none) != 1 || none[unknownCountry].IPv4Addresses != 65536 {
		t.Fatalf("without GeoIP: %+v, want all 65536 addresses unknown", none)
	}
}

func TestIPv6CountedIn64s(t *testing.T) {
	r := fakeRanger{"2001:db8::/32": country("MD", "Moldova", "EU")}
	stats := distribute(rows("p", "2001:db8::/32", "2001:db8::/48"), r)["p"]
	if got := stats["MD"].IPv6Net64s; got != 1<<32 {
		t.Fatalf("v6 /64s = %d, want %d (nested /48 already inside)", got, uint64(1)<<32)
	}
	if stats["MD"].IPv4Addresses != 0 {
		t.Fatal("IPv6 space leaked into the IPv4 count")
	}
	if got := shares(stats)["MD"]; got != 0 {
		t.Fatalf("IPv4 share with no IPv4 space = %v, want 0", got)
	}
}

// Providers are accounted separately, and one prefix announced by two
// different providers (MOAS) counts for both — the platform does not
// adjudicate ownership here.
func TestProvidersAreAccountedSeparately(t *testing.T) {
	r := fakeRanger{"185.1.0.0/24": country("MD", "Moldova", "EU")}
	all := append(rows("a", "185.1.0.0/24"), rows("b", "185.1.0.0/24")...)
	got := distribute(all, r)
	if got["a"]["MD"].IPv4Addresses != 256 || got["b"]["MD"].IPv4Addresses != 256 {
		t.Fatalf("MOAS accounting a=%+v b=%+v, want 256 each", got["a"]["MD"], got["b"]["MD"])
	}
}

func TestCountryMetadataIsCarried(t *testing.T) {
	r := fakeRanger{"185.0.0.0/24": country("MD", "Moldova", "EU")}
	s := distribute(rows("p", "185.0.0.0/24"), r)["p"]["MD"]
	if s.Name != "Moldova" || s.ContinentCode != "EU" || s.ContinentName != "Europe" {
		t.Fatalf("country metadata = %+v", s)
	}
}

func closeTo(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

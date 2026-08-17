package builder

import (
	"math"
	"net/netip"
	"sort"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/routing/geo"
)

// Geographic distribution of a provider's announced address space.
//
// An ASN is not a place. A provider announces prefixes, those prefixes hold
// address space, and that space is located — sometimes in a dozen countries.
// This file turns the deduplicated prefix set of a snapshot into "how much
// IPv4 space does this provider have in each country", which is the number
// VPS Advisor renders and the basis for spreading probe targets across a
// provider's footprint.
//
// Two properties matter more than anything else here:
//
//  1. **No address is counted twice.** BGP announcements nest: a provider that
//     announces 1.2.0.0/16 and 1.2.3.0/24 has announced 65 536 addresses, not
//     65 792. Every prefix is therefore counted only for the space no
//     more-specific announcement of the same provider covers.
//
//  2. **Address space, not prefix count.** A /20 is sixteen times a /24. Shares
//     are computed from address counts, never from how many prefixes or how
//     many BGP observations a country has.
//
// Nothing here expands a prefix into addresses: counts come from prefix
// lengths, and country attribution walks GeoIP *records* (see geo.Ranges).

// unknownCountry is the ISO 3166-1 code reserved for "unknown". Address space
// the GeoIP database does not attribute lands here and is reported as such —
// it is never folded into a real country, and never silently dropped.
const unknownCountry = "ZZ"

// CountryStat is one provider's presence in one country.
type CountryStat struct {
	Code          string // ISO 3166-1 alpha-2, or unknownCountry
	Name          string
	ContinentCode string
	ContinentName string
	// IPv4Addresses counts addresses; IPv6Net64s counts /64 networks, because
	// IPv6 address counts overflow every integer type and mean nothing as a
	// share. IPv6 announcements longer than /64 contribute no counted /64s.
	IPv4Addresses uint64
	IPv6Net64s    uint64
	PrefixCountV4 int
	PrefixCountV6 int
}

// ranger is the GeoIP capability this file needs; *geo.Enricher implements it.
// A nil ranger attributes everything to unknownCountry, which is what a
// deployment without a GeoIP database honestly knows.
type ranger interface {
	Ranges(netip.Prefix) []geo.Range
}

// units returns the accounting weight of p: IPv4 addresses, or IPv6 /64
// networks. An IPv6 prefix longer than /64 weighs nothing — it sits inside a
// single /64 that carries no meaningful share of the provider's space.
func units(p netip.Prefix) uint64 {
	bits, size := p.Bits(), p.Addr().BitLen()
	if size == 128 {
		if bits > 64 {
			return 0
		}
		size = 64
	}
	if bits <= 0 {
		return math.MaxUint64
	}
	return uint64(1) << uint(size-bits)
}

// countryUnits attributes every unit of p to a country, splitting p at the
// GeoIP database's record boundaries. Units no record covers are attributed to
// unknownCountry, so the returned map always sums to units(p).
func countryUnits(p netip.Prefix, r ranger, names map[string]CountryStat) map[string]uint64 {
	total := units(p)
	out := map[string]uint64{}
	var covered uint64
	if r != nil {
		for _, rng := range r.Ranges(p) {
			code := unknownCountry
			if rng.Info.OK {
				code = rng.Info.Country
				if _, seen := names[code]; !seen {
					names[code] = CountryStat{
						Code: code, Name: rng.Info.CountryName,
						ContinentCode: rng.Info.ContinentCode,
						ContinentName: rng.Info.ContinentName,
					}
				}
			}
			u := units(rng.Prefix)
			if covered+u > total { // defensive: never attribute past the prefix
				u = total - covered
			}
			out[code] += u
			covered += u
			if covered >= total {
				break
			}
		}
	}
	if covered < total {
		out[unknownCountry] += total - covered
	}
	return out
}

// exclusiveUnits computes, for a set of prefixes of one provider and one
// address family, the per-country unit counts of the space each prefix covers
// *exclusively* — its own space minus the space its direct more-specifics
// already account for.
//
// Announced prefixes form a forest: any two are either disjoint or nested, so
// a single ordered sweep with a stack finds each prefix's direct children.
// Because the GeoIP split is a function of the address, a child's country map
// is exactly the part of its parent's map it covers, and the subtraction is
// exact.
func exclusiveUnits(prefixes []netip.Prefix, r ranger, names map[string]CountryStat) (own, exclusive []map[string]uint64) {
	sorted := append([]netip.Prefix(nil), prefixes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Addr() != sorted[j].Addr() {
			return sorted[i].Addr().Less(sorted[j].Addr())
		}
		return sorted[i].Bits() < sorted[j].Bits() // container before contained
	})

	own = make([]map[string]uint64, len(sorted))
	exclusive = make([]map[string]uint64, len(sorted))
	for i, p := range sorted {
		own[i] = countryUnits(p, r, names)
		exclusive[i] = map[string]uint64{}
		for code, u := range own[i] {
			exclusive[i][code] = u
		}
	}

	var stack []int
	for i, p := range sorted {
		for len(stack) > 0 {
			top := sorted[stack[len(stack)-1]]
			if top.Contains(p.Addr()) && top.Bits() <= p.Bits() {
				break
			}
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			for code, u := range own[i] {
				if have := exclusive[parent][code]; have > u {
					exclusive[parent][code] = have - u
				} else {
					delete(exclusive[parent], code)
				}
			}
		}
		stack = append(stack, i)
	}
	// Callers index by the sorted order, so hand it back alongside the maps.
	copy(prefixes, sorted)
	return own, exclusive
}

// distribute turns a snapshot's prefix rows into each provider's country
// distribution. Prefixes announced by several ASNs of the *same* provider are
// counted once; the same prefix announced by two different providers (MOAS)
// counts for both, because the platform does not adjudicate ownership here.
func distribute(rows []*prefixRow, r ranger) map[string]map[string]*CountryStat {
	type key struct {
		provider string
		v6       bool
	}
	byProvider := map[key]map[netip.Prefix]bool{}
	for _, row := range rows {
		if row.provider == "" {
			continue
		}
		k := key{row.provider, row.prefix.Addr().Is6()}
		if byProvider[k] == nil {
			byProvider[k] = map[netip.Prefix]bool{}
		}
		byProvider[k][row.prefix.Masked()] = true
	}

	names := map[string]CountryStat{}
	out := map[string]map[string]*CountryStat{}
	for k, set := range byProvider {
		prefixes := make([]netip.Prefix, 0, len(set))
		for p := range set {
			prefixes = append(prefixes, p)
		}
		own, exclusive := exclusiveUnits(prefixes, r, names)

		if out[k.provider] == nil {
			out[k.provider] = map[string]*CountryStat{}
		}
		stats := out[k.provider]
		stat := func(code string) *CountryStat {
			if s, ok := stats[code]; ok {
				return s
			}
			s := &CountryStat{Code: code, Name: "Unknown"}
			if meta, ok := names[code]; ok {
				s.Name, s.ContinentCode, s.ContinentName =
					meta.Name, meta.ContinentCode, meta.ContinentName
			}
			stats[code] = s
			return s
		}
		for i := range prefixes {
			// Address space: exclusive only, so nesting never double-counts.
			for code, u := range exclusive[i] {
				if u == 0 {
					continue
				}
				if k.v6 {
					stat(code).IPv6Net64s += u
				} else {
					stat(code).IPv4Addresses += u
				}
			}
			// Prefix counts: every country the announcement reaches into,
			// which is what "prefixes contributing to this country" means.
			for code := range own[i] {
				if k.v6 {
					stat(code).PrefixCountV6++
				} else {
					stat(code).PrefixCountV4++
				}
			}
		}
	}
	return out
}

// shares returns the percentage of a provider's IPv4 space held by each
// country. Computed from address counts after deduplication, never from prefix
// counts.
func shares(stats map[string]*CountryStat) map[string]float64 {
	var total uint64
	for _, s := range stats {
		total += s.IPv4Addresses
	}
	out := make(map[string]float64, len(stats))
	if total == 0 {
		return out
	}
	for code, s := range stats {
		out[code] = float64(s.IPv4Addresses) / float64(total) * 100
	}
	return out
}

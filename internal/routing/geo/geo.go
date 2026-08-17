// Package geo enriches prefixes with GeoLite2 City data. The database file is
// always a locally supplied copy (deployer's own MaxMind key — never
// redistributed, see architecture risk R8); enrichment is optional and the
// builder degrades gracefully without it.
package geo

import (
	"net"
	"net/netip"

	"github.com/oschwald/geoip2-golang"
	"github.com/oschwald/maxminddb-golang"
)

type Info struct {
	Country       string // ISO 3166-1 alpha-2; empty when unattributed
	CountryName   string
	ContinentCode string
	ContinentName string
	City          string
	Lat           float64
	Lon           float64
	OK            bool
}

// cityRecord is the subset of a GeoLite2-City record the platform reads. It is
// decoded directly through maxminddb (rather than geoip2) because the
// range-splitting traversal below needs the same decoder.
type cityRecord struct {
	Continent struct {
		Code  string            `maxminddb:"code"`
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"continent"`
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude  float64 `maxminddb:"latitude"`
		Longitude float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

func (r cityRecord) info() Info {
	return Info{
		Country:       r.Country.ISOCode,
		CountryName:   r.Country.Names["en"],
		ContinentCode: r.Continent.Code,
		ContinentName: r.Continent.Names["en"],
		City:          r.City.Names["en"],
		Lat:           r.Location.Latitude,
		Lon:           r.Location.Longitude,
		// Country, not registered_country: the question is where the address
		// space *is*, not who registered it. No attribution stays no
		// attribution — never a guessed country.
		OK: r.Country.ISOCode != "",
	}
}

type Enricher struct {
	db *maxminddb.Reader
}

func Open(cityMMDB string) (*Enricher, error) {
	db, err := maxminddb.Open(cityMMDB)
	if err != nil {
		return nil, err
	}
	return &Enricher{db: db}, nil
}

func (e *Enricher) Close() error { return e.db.Close() }

// Lookup resolves geo data for a prefix via its first address: the single
// country/city label carried on the prefix row and shown per network. Address
// *space* accounting does not use this — it uses Ranges, which respects the
// database's own record boundaries.
func (e *Enricher) Lookup(p netip.Prefix) Info {
	var rec cityRecord
	if err := e.db.Lookup(net.IP(p.Addr().AsSlice()), &rec); err != nil {
		return Info{}
	}
	return rec.info()
}

// Range is a contiguous slice of a queried prefix that the GeoIP database
// attributes to one record.
type Range struct {
	Prefix netip.Prefix
	Info   Info
}

// Ranges splits p at the GeoIP database's own record boundaries and returns
// the pieces, clamped to p.
//
// This is the deterministic rule for prefixes that span several geographic
// records: attribute each *sub-range* to the record that covers it, rather
// than labelling the whole prefix from its first address. Space no record
// covers is simply absent from the result — the caller accounts for it as
// unattributed, never as a country.
//
// The traversal walks database records, never individual addresses, so a /8
// costs no more than the number of records inside it.
func (e *Enricher) Ranges(p netip.Prefix) []Range {
	p = p.Masked()
	ipnet := &net.IPNet{
		IP:   net.IP(p.Addr().AsSlice()),
		Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen()),
	}
	var out []Range
	nets := e.db.NetworksWithin(ipnet)
	for nets.Next() {
		var rec cityRecord
		sub, err := nets.Network(&rec)
		if err != nil {
			continue
		}
		got, ok := fromIPNet(sub)
		if !ok {
			continue
		}
		// A record wider than the query is reported as the containing network;
		// only the queried part of it belongs to this prefix.
		if got.Bits() < p.Bits() {
			got = p
		}
		out = append(out, Range{Prefix: got, Info: rec.info()})
	}
	return out
}

// fromIPNet converts a database network to a netip.Prefix, unmapping the
// IPv4-in-IPv6 form an IPv6 database reports IPv4 records in.
func fromIPNet(n *net.IPNet) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	bits, _ := n.Mask.Size()
	if addr.Is4In6() {
		addr = addr.Unmap()
		bits -= 96
	}
	if bits < 0 || bits > addr.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, bits), true
}

// ASNResolver maps an IP to its origin ASN via GeoLite2-ASN; the coordinator
// uses it to record which network a worker probes from (self-ASN exclusion
// and diversity need it).
type ASNResolver struct {
	db *geoip2.Reader
}

func OpenASN(asnMMDB string) (*ASNResolver, error) {
	db, err := geoip2.Open(asnMMDB)
	if err != nil {
		return nil, err
	}
	return &ASNResolver{db: db}, nil
}

func (r *ASNResolver) Close() error { return r.db.Close() }

func (r *ASNResolver) Lookup(addr netip.Addr) (int64, bool) {
	rec, err := r.db.ASN(net.IP(addr.AsSlice()))
	if err != nil || rec == nil || rec.AutonomousSystemNumber == 0 {
		return 0, false
	}
	return int64(rec.AutonomousSystemNumber), true
}

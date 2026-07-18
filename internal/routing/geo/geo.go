// Package geo enriches prefixes with GeoLite2 City data. The database file is
// always a locally supplied copy (deployer's own MaxMind key — never
// redistributed, see architecture risk R8); enrichment is optional and the
// builder degrades gracefully without it.
package geo

import (
	"net"
	"net/netip"

	"github.com/oschwald/geoip2-golang"
)

type Info struct {
	Country string
	City    string
	Lat     float64
	Lon     float64
	OK      bool
}

type Enricher struct {
	db *geoip2.Reader
}

func Open(cityMMDB string) (*Enricher, error) {
	db, err := geoip2.Open(cityMMDB)
	if err != nil {
		return nil, err
	}
	return &Enricher{db: db}, nil
}

func (e *Enricher) Close() error { return e.db.Close() }

// Lookup resolves geo data for a prefix via its first address. Provider
// prefixes are geographically coherent at the granularity we store; per-IP
// spread inside one announced prefix is noise for our purposes.
func (e *Enricher) Lookup(p netip.Prefix) Info {
	addr := p.Addr()
	rec, err := e.db.City(net.IP(addr.AsSlice()))
	if err != nil || rec == nil {
		return Info{}
	}
	info := Info{
		Country: rec.Country.IsoCode,
		Lat:     rec.Location.Latitude,
		Lon:     rec.Location.Longitude,
		OK:      rec.Country.IsoCode != "",
	}
	if name, ok := rec.City.Names["en"]; ok {
		info.City = name
	}
	return info
}

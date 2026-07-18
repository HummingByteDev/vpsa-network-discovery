// Package bogon classifies prefixes that must never be probed or stored:
// private, reserved, documentation, and otherwise non-global address space.
// A route announcing bogon space in the DFZ is garbage or a leak either way.
package bogon

import "net/netip"

var bogons = func() []netip.Prefix {
	specs := []string{
		// IPv4
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
		"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4",
		// IPv6: everything outside 2000::/3 is handled structurally below;
		// these are non-global carve-outs inside 2000::/3.
		"2001:db8::/32", "2001:2::/48", "3fff::/20",
	}
	out := make([]netip.Prefix, 0, len(specs))
	for _, s := range specs {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

var v6Global = netip.MustParsePrefix("2000::/3")

// IsBogon reports whether p lies inside (or equals) non-global space.
func IsBogon(p netip.Prefix) bool {
	if p.Addr().Is6() && !v6Global.Overlaps(p) {
		return true
	}
	for _, b := range bogons {
		if b.Overlaps(p) {
			return true
		}
	}
	return false
}

// TooLong reports prefixes longer than what belongs in the global table
// (v4 >/24, v6 >/48). These are flagged, not dropped: rare but occasionally
// legitimate (anycast, DDoS mitigation carve-outs).
func TooLong(p netip.Prefix) bool {
	if p.Addr().Is4() {
		return p.Bits() > 24
	}
	return p.Bits() > 48
}

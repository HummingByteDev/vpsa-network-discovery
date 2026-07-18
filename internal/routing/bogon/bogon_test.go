package bogon

import (
	"net/netip"
	"testing"
)

func TestIsBogon(t *testing.T) {
	cases := []struct {
		prefix string
		want   bool
	}{
		{"10.0.0.0/8", true},
		{"10.5.0.0/16", true},
		{"192.168.1.0/24", true},
		{"203.0.113.0/24", true}, // documentation
		{"100.64.0.0/10", true},  // CGNAT
		{"224.0.0.0/8", true},    // multicast
		{"8.8.8.0/24", false},
		{"185.0.0.0/16", false},
		{"1.0.0.0/7", true}, // covers 0.0.0.0/8 → overlap counts
		{"2001:db8::/32", true},
		{"fc00::/7", true},  // ULA: outside 2000::/3
		{"fe80::/10", true}, // link-local
		{"2001:41d0::/32", false},
		{"2a00::/12", false},
	}
	for _, c := range cases {
		if got := IsBogon(netip.MustParsePrefix(c.prefix)); got != c.want {
			t.Errorf("IsBogon(%s) = %v, want %v", c.prefix, got, c.want)
		}
	}
}

func TestTooLong(t *testing.T) {
	cases := []struct {
		prefix string
		want   bool
	}{
		{"185.0.0.0/24", false},
		{"185.0.0.0/25", true},
		{"185.0.0.4/32", true},
		{"2001:41d0::/48", false},
		{"2001:41d0::/49", true},
	}
	for _, c := range cases {
		if got := TooLong(netip.MustParsePrefix(c.prefix)); got != c.want {
			t.Errorf("TooLong(%s) = %v, want %v", c.prefix, got, c.want)
		}
	}
}

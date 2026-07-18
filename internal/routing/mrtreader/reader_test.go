package mrtreader

import (
	"compress/gzip"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"github.com/osrg/gobgp/v3/pkg/packet/mrt"
)

// buildBview serializes a synthetic TABLE_DUMP_V2 dump with known contents.
func buildBview(t *testing.T, gzipped bool) string {
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

	peers := []*mrt.Peer{
		mrt.NewPeer("10.0.0.1", "10.0.0.1", 65001, true),
		mrt.NewPeer("10.0.0.2", "10.0.0.2", 65002, true),
	}
	add(mrt.PEER_INDEX_TABLE, mrt.NewPeerIndexTable("10.0.0.0", "test-view", peers))

	entry := func(peer uint16, path ...uint32) *mrt.RibEntry {
		attrs := []bgp.PathAttributeInterface{
			bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{
				bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, path),
			}),
		}
		return mrt.NewRibEntry(peer, 1752800000, 0, attrs, false)
	}

	// R1: monitored origin 64500, seen by two peers.
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(1, bgp.NewIPAddrPrefix(16, "185.0.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64500), entry(1, 65002, 174, 64500)}))
	// R2: unmonitored origin — must not be emitted.
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(2, bgp.NewIPAddrPrefix(24, "8.8.8.0"),
		[]*mrt.RibEntry{entry(0, 65001, 15169)}))
	// R3: MOAS — monitored 64500 and unmonitored 64999 both originate.
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(3, bgp.NewIPAddrPrefix(24, "185.1.0.0"),
		[]*mrt.RibEntry{entry(0, 65001, 64500), entry(1, 65002, 64999)}))
	// R4: IPv6 monitored origin.
	add(mrt.RIB_IPV6_UNICAST, mrt.NewRib(4, bgp.NewIPv6AddrPrefix(32, "2001:41d0::"),
		[]*mrt.RibEntry{entry(0, 65001, 3356, 64500)}))
	// R5: trailing AS_SET containing a monitored ASN (aggregated route).
	setAttrs := []bgp.PathAttributeInterface{
		bgp.NewPathAttributeAsPath([]bgp.AsPathParamInterface{
			bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SEQ, []uint32{65001, 4200000000}),
			bgp.NewAs4PathParam(bgp.BGP_ASPATH_ATTR_TYPE_SET, []uint32{64500, 64502}),
		}),
	}
	add(mrt.RIB_IPV4_UNICAST, mrt.NewRib(5, bgp.NewIPAddrPrefix(22, "185.2.0.0"),
		[]*mrt.RibEntry{mrt.NewRibEntry(0, 1752800000, 0, setAttrs, false)}))

	name := "test-bview"
	if gzipped {
		name += ".gz"
	}
	path := filepath.Join(t.TempDir(), name)
	if gzipped {
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
	} else if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, path string) (map[string]Record, *Stats) {
	t.Helper()
	monitored := map[uint32]bool{64500: true}
	got := map[string]Record{}
	stats, err := Stream(path, monitored, func(r Record) error {
		got[r.Prefix.String()] = r
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, stats
}

func TestStreamExtractsMonitoredPrefixes(t *testing.T) {
	for _, gzipped := range []bool{false, true} {
		path := buildBview(t, gzipped)
		got, stats := collect(t, path)

		if stats.Records != 5 {
			t.Errorf("records: got %d, want 5", stats.Records)
		}
		if stats.MatchedRecords != 4 {
			t.Errorf("matched: got %d, want 4", stats.MatchedRecords)
		}
		if len(got) != 4 {
			t.Fatalf("emitted prefixes: got %d (%v), want 4", len(got), got)
		}
		if _, bad := got["8.8.8.0/24"]; bad {
			t.Error("unmonitored origin was emitted")
		}

		r1 := got["185.0.0.0/16"]
		if len(r1.Origins) != 1 || r1.Origins[0] != 64500 {
			t.Errorf("R1 origins: %v", r1.Origins)
		}
		if r1.PeerCounts[64500] != 2 {
			t.Errorf("R1 peer count: got %d, want 2", r1.PeerCounts[64500])
		}

		r3 := got["185.1.0.0/24"]
		if len(r3.Origins) != 2 {
			t.Errorf("R3 (MOAS) origins: %v, want [64500 64999]", r3.Origins)
		}

		r4, ok := got["2001:41d0::/32"]
		if !ok || !r4.Prefix.Addr().Is6() {
			t.Errorf("IPv6 prefix missing: %v", got)
		}

		r5 := got["185.2.0.0/22"]
		if len(r5.Origins) != 2 {
			t.Errorf("R5 (AS_SET) origins: %v, want both set members", r5.Origins)
		}
	}
}

func TestStreamPrefixIsMasked(t *testing.T) {
	path := buildBview(t, false)
	got, _ := collect(t, path)
	for s, r := range got {
		p := netip.MustParsePrefix(s)
		if p != p.Masked() {
			t.Errorf("prefix %s not masked", r.Prefix)
		}
	}
}

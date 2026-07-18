// Package mrtreader streams RIPE RIS MRT TABLE_DUMP_V2 dumps (bview files)
// and yields, for each RIB prefix whose origin set intersects the monitored
// ASN set, the prefix with its full origin information.
//
// Only this package (via the builder) ever touches MRT data; everything else
// consumes its output. One TABLE_DUMP_V2 RIB record carries every peer's path
// for a single prefix, so multi-origin (MOAS) detection is local to a record
// and the reader needs no cross-record state.
package mrtreader

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"

	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"github.com/osrg/gobgp/v3/pkg/packet/mrt"
)

// Record is one monitored prefix occurrence in the dump.
type Record struct {
	Prefix netip.Prefix
	// Origins holds every origin ASN seen for this prefix across all peers,
	// monitored or not (len > 1 ⇒ MOAS).
	Origins []uint32
	// PeerCounts maps origin ASN → number of peer entries originating there;
	// a visibility signal for later target selection.
	PeerCounts map[uint32]int
}

// Stats summarizes a completed pass.
type Stats struct {
	Records        int // RIB records seen
	MatchedRecords int // records with ≥1 monitored origin
	Skipped        int // undecodable or unsupported records
	FirstTimestamp uint32
}

// Stream decompresses (if gzipped) and parses the dump at path, invoking emit
// for every RIB record with at least one origin in monitored.
func Stream(path string, monitored map[uint32]bool, emit func(Record) error) (*Stats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = bufio.NewReaderSize(f, 1<<20)
	if br, ok := r.(*bufio.Reader); ok {
		if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
			gz, err := gzip.NewReader(br)
			if err != nil {
				return nil, fmt.Errorf("gzip: %w", err)
			}
			defer gz.Close()
			r = gz
		}
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 64<<20) // peer index tables can be large
	scanner.Split(mrt.SplitMrt)

	stats := &Stats{}
	for scanner.Scan() {
		buf := scanner.Bytes()
		hdr := &mrt.MRTHeader{}
		if err := hdr.DecodeFromBytes(buf[:mrt.MRT_COMMON_HEADER_LEN]); err != nil {
			stats.Skipped++
			continue
		}
		if stats.FirstTimestamp == 0 {
			stats.FirstTimestamp = hdr.Timestamp
		}
		if mrt.MRTType(hdr.Type) != mrt.TABLE_DUMPv2 {
			stats.Skipped++
			continue
		}
		switch mrt.MRTSubTypeTableDumpv2(hdr.SubType) {
		case mrt.RIB_IPV4_UNICAST, mrt.RIB_IPV6_UNICAST,
			mrt.RIB_IPV4_UNICAST_ADDPATH, mrt.RIB_IPV6_UNICAST_ADDPATH:
		default:
			continue // peer index table, multicast, generic: not needed
		}
		msg, err := mrt.ParseMRTBody(hdr, buf[mrt.MRT_COMMON_HEADER_LEN:])
		if err != nil {
			stats.Skipped++
			continue
		}
		rib, ok := msg.Body.(*mrt.Rib)
		if !ok {
			stats.Skipped++
			continue
		}
		stats.Records++

		rec := Record{PeerCounts: map[uint32]int{}}
		matched := false
		for _, entry := range rib.Entries {
			for _, origin := range originsOf(entry.PathAttributes) {
				if !slices.Contains(rec.Origins, origin) {
					rec.Origins = append(rec.Origins, origin)
				}
				rec.PeerCounts[origin]++
				if monitored[origin] {
					matched = true
				}
			}
		}
		if !matched {
			continue
		}
		stats.MatchedRecords++

		pfx, err := netip.ParsePrefix(rib.Prefix.String())
		if err != nil {
			stats.Skipped++
			continue
		}
		slices.Sort(rec.Origins)
		rec.Prefix = pfx.Masked()
		if err := emit(rec); err != nil {
			return stats, err
		}
	}
	if err := scanner.Err(); err != nil {
		return stats, fmt.Errorf("scan mrt: %w", err)
	}
	return stats, nil
}

// originsOf extracts the origin ASN(s) from a path's AS_PATH attribute: the
// last element of a trailing AS_SEQUENCE, or every member of a trailing
// AS_SET (aggregated route — genuinely multi-origin).
func originsOf(attrs []bgp.PathAttributeInterface) []uint32 {
	for _, attr := range attrs {
		asPath, ok := attr.(*bgp.PathAttributeAsPath)
		if !ok {
			continue
		}
		if len(asPath.Value) == 0 {
			return nil
		}
		last := asPath.Value[len(asPath.Value)-1]
		switch seg := last.(type) {
		case *bgp.As4PathParam:
			if len(seg.AS) == 0 {
				return nil
			}
			if seg.Type == bgp.BGP_ASPATH_ATTR_TYPE_SET {
				return seg.AS
			}
			return seg.AS[len(seg.AS)-1:]
		case *bgp.AsPathParam:
			if len(seg.AS) == 0 {
				return nil
			}
			out := make([]uint32, 0, 1)
			if seg.Type == bgp.BGP_ASPATH_ATTR_TYPE_SET {
				for _, a := range seg.AS {
					out = append(out, uint32(a))
				}
				return out
			}
			return append(out, uint32(seg.AS[len(seg.AS)-1]))
		}
		return nil
	}
	return nil
}

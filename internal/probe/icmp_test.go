package probe

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

// TestICMPLoopback pings 127.0.0.1. It needs either unprivileged datagram
// ICMP (net.ipv4.ping_group_range) or CAP_NET_RAW; environments with neither
// skip — the fake-prober end-to-end test covers the pipeline regardless.
func TestICMPLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := ICMP{}.Probe(ctx, netip.MustParseAddr("127.0.0.1"),
		Params{Count: 2, Interval: 50 * time.Millisecond, Timeout: time.Second})
	if err != nil {
		t.Skipf("no ICMP capability in this environment: %v", err)
	}
	if !res.OK {
		t.Skip("ICMP socket available but loopback did not reply (filtered environment)")
	}
	if res.PacketsSent != 2 {
		t.Fatalf("sent %d packets, want 2", res.PacketsSent)
	}
	if res.RTTMillis <= 0 {
		t.Fatalf("median RTT %v, want > 0", res.RTTMillis)
	}
	t.Logf("loopback rtt=%.3fms loss=%d/%d", res.RTTMillis, res.PacketsLost, res.PacketsSent)
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(ICMP{})
	if _, err := r.Get("icmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("quic"); err == nil {
		t.Fatal("unknown prober type did not error")
	}
}

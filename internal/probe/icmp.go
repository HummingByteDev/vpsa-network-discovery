package probe

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// ICMP sends echo requests and measures reply RTTs. It prefers unprivileged
// datagram ICMP sockets (needs net.ipv4.ping_group_range to include the
// process group) and falls back to raw sockets (needs CAP_NET_RAW — granted
// to the worker container).
type ICMP struct{}

func (ICMP) Type() string { return "icmp" }

func (ICMP) Probe(ctx context.Context, target netip.Addr, params Params) (Result, error) {
	p := params.withDefaults()
	conn, proto, err := openICMP(target)
	if err != nil {
		return Result{}, fmt.Errorf("icmp socket: %w", err)
	}
	defer conn.Close()

	id := os.Getpid() & 0xffff
	var rtts []float64
	sent := 0
	for seq := 0; seq < p.Count; seq++ {
		if ctx.Err() != nil {
			break
		}
		echo := &icmp.Message{
			Type: echoType(target), Code: 0,
			Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("cnip-probe")},
		}
		wire, err := echo.Marshal(nil)
		if err != nil {
			return Result{}, err
		}
		start := time.Now()
		if _, err := conn.WriteTo(wire, addrFor(conn, target)); err != nil {
			sent++ // counts as a lost packet, not an execution error
			continue
		}
		sent++
		if rtt, ok := awaitReply(conn, proto, id, seq, start, p.Timeout); ok {
			rtts = append(rtts, rtt)
		}
		if seq < p.Count-1 {
			select {
			case <-ctx.Done():
			case <-time.After(p.Interval):
			}
		}
	}

	res := Result{
		OK:          len(rtts) > 0,
		PacketsSent: sent,
		PacketsLost: sent - len(rtts),
		Metrics:     map[string]any{},
	}
	if len(rtts) > 0 {
		sort.Float64s(rtts)
		res.RTTMillis = rtts[len(rtts)/2]
		res.Metrics["rtt_min_ms"] = rtts[0]
		res.Metrics["rtt_max_ms"] = rtts[len(rtts)-1]
	}
	return res, nil
}

// openICMP tries datagram ICMP first, then raw.
func openICMP(target netip.Addr) (*icmp.PacketConn, int, error) {
	if target.Is4() {
		if c, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
			return c, 1, nil
		}
		c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		return c, 1, err
	}
	if c, err := icmp.ListenPacket("udp6", "::"); err == nil {
		return c, 58, nil
	}
	c, err := icmp.ListenPacket("ip6:ipv6-icmp", "::")
	return c, 58, err
}

func echoType(target netip.Addr) icmp.Type {
	if target.Is4() {
		return ipv4.ICMPTypeEcho
	}
	return ipv6.ICMPTypeEchoRequest
}

// addrFor builds the destination address in the form the socket type expects.
func addrFor(conn *icmp.PacketConn, target netip.Addr) net.Addr {
	ip := net.IP(target.AsSlice())
	if _, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return &net.UDPAddr{IP: ip}
	}
	return &net.IPAddr{IP: ip}
}

// awaitReply reads until the matching echo reply, timeout, or a foreign
// packet budget is exhausted (other flows' replies share raw sockets).
func awaitReply(conn *icmp.PacketConn, proto, id, seq int, start time.Time, timeout time.Duration) (float64, bool) {
	deadline := start.Add(timeout)
	buf := make([]byte, 1500)
	for budget := 0; budget < 64; budget++ {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return 0, false
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, false // timeout
		}
		rtt := float64(time.Since(start).Microseconds()) / 1000
		msg, err := icmp.ParseMessage(proto, buf[:n])
		if err != nil {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok || echo.Seq != seq {
			continue
		}
		// Datagram sockets rewrite the ID; only match it on raw sockets.
		if _, isRaw := conn.LocalAddr().(*net.IPAddr); isRaw && echo.ID != id {
			continue
		}
		return rtt, true
	}
	return 0, false
}

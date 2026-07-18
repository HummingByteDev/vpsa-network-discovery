// Package probe is the protocol-agnostic measurement engine
// (docs/architecture/01-system-architecture.md §4.4): probers implement one
// protocol each behind a common interface, so new protocols never require
// changes to scheduling, upload, or aggregation. ICMP is the first
// implementation; TCP connect and HTTP(S) are future probers.
package probe

import (
	"context"
	"fmt"
	"net/netip"
	"time"
)

// Params tunes a single probe execution; zero values take defaults.
type Params struct {
	Count    int           // packets per execution (default 4)
	Interval time.Duration // gap between packets (default 200ms)
	Timeout  time.Duration // per-packet reply timeout (default 2s)
}

func (p Params) withDefaults() Params {
	if p.Count <= 0 {
		p.Count = 4
	}
	if p.Interval <= 0 {
		p.Interval = 200 * time.Millisecond
	}
	if p.Timeout <= 0 {
		p.Timeout = 2 * time.Second
	}
	return p
}

// Result is one probe execution's outcome. OK means the target answered at
// least once; metrics carry protocol-specific detail.
type Result struct {
	OK          bool
	RTTMillis   float64 // median RTT of received replies; 0 when none
	PacketsSent int
	PacketsLost int
	Metrics     map[string]any
}

type Prober interface {
	// Type names the probe protocol as stored in assignments/observations.
	Type() string
	Probe(ctx context.Context, target netip.Addr, params Params) (Result, error)
}

// Registry maps probe_type → Prober; the executor consults it per assignment.
type Registry map[string]Prober

func NewRegistry(probers ...Prober) Registry {
	r := Registry{}
	for _, p := range probers {
		r[p.Type()] = p
	}
	return r
}

func (r Registry) Get(probeType string) (Prober, error) {
	p, ok := r[probeType]
	if !ok {
		return nil, fmt.Errorf("no prober for type %q", probeType)
	}
	return p, nil
}

// Command loadtest simulates a worker fleet against a coordinator: real
// registration, signed heartbeats, leases, and signed observation uploads —
// everything except actual ICMP. It reports request latencies and error
// counts so capacity claims are measured, not guessed.
//
// Usage (dev):
//
//	loadtest -url http://localhost:8080 -token dev-worker-token \
//	         -workers 500 -duration 3m
//
// The coordinator must have a dev enrollment token set (each simulated
// worker auto-enrolls with it), or pre-created tokens must be piped in.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/HummingByteDev/vpsa-network-discovery/internal/observation"
	"github.com/HummingByteDev/vpsa-network-discovery/internal/worker"
)

type stats struct {
	mu        sync.Mutex
	latencies map[string][]time.Duration
	errors    map[string]*atomic.Int64
	ok        map[string]*atomic.Int64
}

func newStats() *stats {
	s := &stats{latencies: map[string][]time.Duration{},
		errors: map[string]*atomic.Int64{}, ok: map[string]*atomic.Int64{}}
	for _, k := range []string{"register", "heartbeat", "lease", "upload"} {
		s.errors[k] = &atomic.Int64{}
		s.ok[k] = &atomic.Int64{}
	}
	return s
}

func (s *stats) record(op string, d time.Duration, err error) {
	if err != nil {
		s.errors[op].Add(1)
		return
	}
	s.ok[op].Add(1)
	s.mu.Lock()
	s.latencies[op] = append(s.latencies[op], d)
	s.mu.Unlock()
}

func pct(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	i := int(float64(len(ds)-1) * p)
	return ds[i]
}

func main() {
	url := flag.String("url", "http://localhost:8080", "coordinator URL")
	token := flag.String("token", "dev-worker-token", "dev enrollment token")
	n := flag.Int("workers", 500, "simulated workers")
	dur := flag.Duration("duration", 3*time.Minute, "steady-state duration")
	hb := flag.Duration("heartbeat", 30*time.Second, "heartbeat interval")
	lease := flag.Duration("lease", 60*time.Second, "lease interval")
	capacity := flag.Int("capacity", 16, "lease capacity per worker")
	flag.Parse()

	st := newStats()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var registered atomic.Int64

	start := time.Now()
	rampEach := 20 * time.Millisecond // 500 workers over ~10s
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			time.Sleep(time.Duration(idx) * rampEach)
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return
			}
			c := worker.NewClient(*url, key)
			t0 := time.Now()
			_, err = c.Register(ctx, *token, fmt.Sprintf("load-%04d", idx), "loadtest")
			st.record("register", time.Since(t0), err)
			if err != nil {
				return
			}
			registered.Add(1)

			var held []worker.Assignment
			hbT := time.NewTicker(*hb + time.Duration(mrand.N(int64(*hb)/4)))
			leaseT := time.NewTicker(*lease + time.Duration(mrand.N(int64(*lease)/4)))
			upT := time.NewTicker(30 * time.Second)
			defer hbT.Stop()
			defer leaseT.Stop()
			defer upT.Stop()

			doLease := func() {
				t := time.Now()
				as, err := c.LeaseAssignments(ctx, *capacity)
				st.record("lease", time.Since(t), err)
				if err == nil {
					held = as
				}
			}
			doLease()
			for {
				select {
				case <-ctx.Done():
					return
				case <-hbT.C:
					t := time.Now()
					_, err := c.Heartbeat(ctx, "loadtest")
					st.record("heartbeat", time.Since(t), err)
				case <-leaseT.C:
					doLease()
				case <-upT.C:
					if len(held) == 0 {
						continue
					}
					obs := make([]observation.Observation, 0, len(held))
					now := time.Now().UTC()
					for _, a := range held {
						rtt := 5 + mrand.Float64()*40
						o := observation.Observation{
							AssignmentID: a.ID, Target: a.Target, ProbeType: "icmp",
							MeasuredAt: now, OK: true, RTTMillis: &rtt,
							PacketsSent: 3, PacketsLost: 0,
						}
						if err := observation.Sign(&o, key); err == nil {
							obs = append(obs, o)
						}
					}
					t := time.Now()
					err := c.UploadObservations(ctx, uuid.NewString(), obs)
					st.record("upload", time.Since(t), err)
				}
			}
		}(i)
	}

	// Let the fleet run, then stop.
	time.Sleep(time.Duration(*n)*rampEach + *dur)
	cancel()
	wg.Wait()

	fmt.Printf("\n=== loadtest: %d workers, %s steady state (total %s) ===\n",
		*n, *dur, time.Since(start).Round(time.Second))
	fmt.Printf("registered: %d/%d\n\n", registered.Load(), *n)
	fmt.Printf("%-10s %8s %8s %10s %10s %10s\n", "op", "ok", "errors", "p50", "p95", "max")
	for _, op := range []string{"register", "heartbeat", "lease", "upload"} {
		st.mu.Lock()
		ds := st.latencies[op]
		st.mu.Unlock()
		fmt.Printf("%-10s %8d %8d %10s %10s %10s\n", op,
			st.ok[op].Load(), st.errors[op].Load(),
			pct(ds, 0.50).Round(time.Millisecond),
			pct(ds, 0.95).Round(time.Millisecond),
			pct(ds, 1.0).Round(time.Millisecond))
	}
	if st.errors["register"].Load() > 0 || st.errors["upload"].Load() > 0 {
		os.Exit(1)
	}
}

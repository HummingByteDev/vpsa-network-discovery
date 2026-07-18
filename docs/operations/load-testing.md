# Load Testing

`cmd/loadtest` simulates a worker fleet doing everything real workers do —
registration, signed heartbeats, leases, signed observation uploads — minus
the ICMP itself:

```sh
go build -o bin/ ./cmd/loadtest
./bin/loadtest -url http://localhost:8080 -token dev-worker-token \
               -workers 500 -duration 3m
```

It needs a coordinator with a dev enrollment token (each simulated worker
auto-enrolls) — never point it at production; run it against a staging stack
or the dev compose. Output: per-op ok/error counts and p50/p95/max
latencies; non-zero exit if registrations or uploads failed.

## Reference numbers

Single machine (4-core laptop-class, coordinator + postgres + full dev stack
sharing it), 500 workers, 30 s heartbeats, 60 s leases, 30 s uploads —
see docs/demos/phase11.md for the captured run. Use these as a regression
baseline: the v1 production target (several hundred workers on a 4 vCPU VM)
holds with an order-of-magnitude headroom on request latencies.

## What to watch during a run

- `vapn_http_request_duration_seconds` p95 per route (Grafana) — the lease
  route is the heaviest (row locking); it should stay well under 250 ms.
- postgres connections (pool default) and CPU.
- `vapn_observations_ingested_total` rate matches the expected
  `workers × held assignments / 30s`.
- After the run: simulated workers linger as `active` with stale heartbeats
  in the registry (dev database); clean up with truncate or ignore.

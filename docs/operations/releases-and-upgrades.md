# Releases, Upgrades & Rollback

How a version becomes a release, how the platform moves onto it, and how to go
back. Workers upgrade through their own channel — the coordinator tolerates
version skew, and `min_worker_version` in the snapshot manifest is the
compatibility floor it enforces.

## Versioning

Semantic versioning on git tags (`v1.2.3`), one version for the whole platform:
all images and CLIs share it, so there is no per-component version matrix.

- **Patch** — fixes; no schema or contract changes.
- **Minor** — features; schema changes must be backward-compatible (the previous
  minor must run against the new schema — see [Upgrade](#upgrade)).
- **Major** — breaking contract changes (worker protocol, advisor contract).
  Workers older than a snapshot manifest's `min_worker_version` refuse the
  snapshot; raise that floor only in majors, with a deprecation window.

## What a tag produces

Pushing a `v*` tag runs [`.github/workflows/release.yml`](../../.github/workflows/release.yml),
which publishes:

- **Multi-arch (amd64/arm64) images to GHCR**, tagged `vX.Y.Z` and `latest`:
  `ghcr.io/hummingbytedev/vapn-{coordinator,aggregator,builder,migrate,worker,mockadvisor}`
- **Release assets**: `vapn-linux-{amd64,arm64}`,
  `vapnctl-linux-{amd64,arm64}`, `install.sh`, `SHA256SUMS`, and generated
  release notes.

> Never delete or re-point a published tag — rollback depends on old tags
> remaining pullable.

## Cutting a release

1. `main` is green in CI; the demo docs are current, and
   [`CHANGELOG.md`](../../CHANGELOG.md) has an entry for the release —
   including its compatibility table, which is what tells an operator whether
   an upgrade needs anything beyond `docker compose up -d`.
2. `git tag v1.2.3 && git push origin v1.2.3` — the workflow does the rest.
3. Upgrade the platform (below) and watch one consensus window.
4. Workers pick the new image up via `vapn update` or their auto-update timer
   (health-gated with client-side auto-rollback). The fleet's version mix is
   visible in `vapnctl status` and in fleet telemetry.

### Channels

`latest` is the stable channel workers follow by default. For a canary period,
tag `vX.Y.Z` **without** moving `latest`, run your anchor workers with
`VAPN_WORKER_IMAGE=ghcr.io/hummingbytedev/vapn-worker:vX.Y.Z`, then re-tag
`latest` once satisfied.

---

## Upgrade

```sh
cd /opt/vapn/deploy/prod
git pull                                             # compose/config changes, if any
sed -i 's/^VAPN_VERSION=.*/VAPN_VERSION=v1.3.0/' .env
docker compose pull
docker compose pull builder                          # profiled: pull skips it otherwise
docker compose up -d                                 # migrate runs first; services follow
docker compose ps                                    # all healthy?
vapnctl status                                       # fleet unaffected?
```

Zero-downtime notes: workers buffer observations through the seconds of
coordinator restart, so nothing is lost. Migrations run under an advisory lock —
a second `up -d` is always safe.

**Do not omit the second `pull`.** The builder lives in the `build` compose
profile, and profiled services are excluded from bare `pull` and `up -d` —
naming the service is what enables its profile. Miss it and the builder keeps
running the old image indefinitely while every other component moves forward,
which is a difficult failure to spot: snapshots keep publishing, just from
stale code. Confirm after upgrading with:

```sh
docker images --format '{{.Repository}}:{{.Tag}}  {{.CreatedSince}}' | grep vapn-builder
```

(`docker compose images builder` reports nothing useful here — it lists images
belonging to *containers*, and the builder has none between runs.)

### Order of operations for risky releases

1. Announce it in the operator channel; note the current `VAPN_VERSION`.
2. Take an extra backup: `sudo systemctl start vapn-backup.service`.
3. Upgrade; watch the Grafana fleet dashboard for one full consensus window
   (ingest rate, 5xx ratio, and outbox depth all steady).
4. Keep the previous image tag pullable — never delete release tags.

## Rollback: the software

```sh
sed -i 's/^VAPN_VERSION=.*/VAPN_VERSION=v1.2.0/' .env
docker compose up -d
```

Migrations are forward-only by policy; releases that add schema do so
backward-compatibly (new tables and columns, never breaking old readers), so the
previous minor version always runs against the newer schema. A release that
cannot honour that must say so loudly in its notes and ship explicit `down`
guidance.

## Rollback: the routing snapshot

Completely independent of software versions.

```sh
vapnctl snapshots list
vapnctl snapshots rollback 20260808T0800Z-1723118400000
```

Workers converge on their next heartbeat (the downgrade is logged with a warning
worker-side). Retention keeps the newest `VAPN_RETAIN_SNAPSHOTS` superseded
snapshots rollback-eligible (default 5); older ones are pruned and `snapshots
list` marks them `pruned` — rollback to those is refused.

## Worker-side updates

Worker operators run health-gated updates with automatic rollback; the platform
drains anything below the minimum version. Both mechanisms are documented for
operators in [Operating a worker → Updating](../worker/operations.md#updating).

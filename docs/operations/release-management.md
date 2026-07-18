# Release Management

## Versioning

Semantic versioning on git tags (`v1.2.3`), one version for the whole
platform (all images + CLIs share it — no per-component version matrices).

- **Patch**: fixes, no schema or contract changes.
- **Minor**: features; schema changes must be backward-compatible (the
  previous minor must run against the new schema — see upgrades.md).
- **Major**: breaking contract changes (worker protocol, advisor contract).
  Workers older than a snapshot manifest's `min_worker_version` refuse the
  snapshot; raise that floor only in majors, with a deprecation window.

## What a tag produces (.github/workflows/release.yml)

- Multi-arch (amd64/arm64) images to GHCR:
  `ghcr.io/hummingbytedev/vapn-{coordinator,aggregator,builder,migrate,worker,mockadvisor}:vX.Y.Z` and `:latest`.
- Release assets: `vapn-linux-{amd64,arm64}`, `vapnctl-linux-{amd64,arm64}`,
  `install.sh`, `SHA256SUMS`, generated release notes.

Never delete or re-point a published tag — rollback depends on old tags
remaining pullable.

## Cutting a release

1. `main` green in CI; demo docs current.
2. `git tag v1.2.3 && git push origin v1.2.3` — the workflow does the rest.
3. Upgrade the platform (upgrades.md), watch one consensus window.
4. Workers pick the new image up via `vapn update` / the auto-update timer
   (health-gated, auto-rollback client-side). The fleet's version mix is
   visible in `vapnctl status` and fleet telemetry.

## Channels

`latest` is the stable channel workers follow by default. For a canary
period, tag `vX.Y.Z` without moving `latest`, run anchor workers with
`VAPN_WORKER_IMAGE=...:vX.Y.Z`, then re-tag `latest` when satisfied.

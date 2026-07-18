# Phase 3 Demo — Snapshot Distribution

What exists: after the database publish, the builder now exports the
worker-facing SQLite artifact, signs its manifest with Ed25519, uploads both to
the artifact store, moves the `current` pointer, and prunes routing data +
store objects of old superseded snapshots. Rollback re-points workers at any
retained version. The store is untrusted by design — integrity comes entirely
from the signed manifest, which workers verify (Phase 4).

Production store: **Backblaze B2** via its S3-compatible API
(`VAPN_ARTIFACT_S3_ENDPOINT=s3.<region>.backblazeb2.com`, B2 application key
ID/secret as access/secret). Local dev uses minio — same protocol, same code.

## 1. Generate the signing keypair (once)

```sh
make build
./bin/keygen
# VAPN_SNAPSHOT_SIGNING_KEY=…   ← builder secret
# VAPN_SNAPSHOT_PUBLIC_KEY=…    ← pinned by workers
```

## 2. Run a distributing build

```sh
VAPN_DB_DSN=postgres://vapn:vapn-dev@localhost:5433/vapn \
VAPN_ADVISOR_URL=http://localhost:8081 \
VAPN_ADVISOR_TOKEN=dev-advisor-token \
VAPN_GEOIP_CITY_MMDB=data/geo-data/GeoLite2-City.mmdb \
VAPN_ARTIFACT_S3_ENDPOINT=localhost:9000 \
VAPN_ARTIFACT_S3_ACCESS_KEY=vapn \
VAPN_ARTIFACT_S3_SECRET_KEY=vapn-dev-secret \
VAPN_ARTIFACT_S3_USE_SSL=false \
VAPN_SNAPSHOT_SIGNING_KEY=<from keygen> \
./bin/builder
```

The log gains `artifact published` (sha256, size, target count) between the
load and publish stages; publication order guarantees a half-finished upload
is never visible: object + manifest upload → **readback verification**
(re-download, verify signature + hash) → DB publish → pointer move → prune.

Knobs: `VAPN_RETAIN_SNAPSHOTS` (default 5 superseded versions kept),
`VAPN_MIN_WORKER_VERSION` (stamped into the manifest, enforced by workers),
`VAPN_ARTIFACT_DIR` (filesystem store instead of S3 — tests, single-host).

## 3. Inspect the store

```sh
docker run --rm --network vapn-dev_default -e MC_HOST_local=http://vapn:vapn-dev-secret@minio:9000 \
  minio/mc ls -r local/vapn-artifacts/snapshots/
# snapshots/current.json
# snapshots/<version>/manifest.json
# snapshots/<version>/routing.sqlite
```

`current.json` names the version in force; the manifest carries the artifact's
sha256/size/signature and counts. Open the SQLite artifact: tables `meta`
(version, min_worker_version), `prefixes` (prefix, origin_asn, provider_id,
geo_country), `targets` (address, provider_id, prefix).

## 4. Rollback

Rollback = flip the published snapshot in the database and re-point
`current.json` at a retained version (`Publisher.RollbackTo`; admin CLI wraps
this in Phase 10). Rolling back to a pruned version is refused.

## 5. Tests

`go test ./internal/artifact/ ./internal/builder/` covers: sign/verify
round-trip, unsigned/tampered/wrong-key manifests rejected, file tamper
detection, artifact contents (prefix/target/meta), pointer movement over four
consecutive builds, retention pruning (rows + objects gone, summary row kept),
rollback, and refusal to roll back to a pruned snapshot.

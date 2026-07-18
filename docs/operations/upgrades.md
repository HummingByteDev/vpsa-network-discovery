# Platform Upgrades & Rollback

Platform services (coordinator, aggregator, builder, migrate) ship as one
versioned image set; workers upgrade independently through their own channel
(the coordinator tolerates version skew — `min_worker_version` in the
snapshot manifest is the compatibility floor it enforces).

## Upgrade

```sh
cd /opt/vapn/deploy/prod
git pull                                  # compose/config changes, if any
sed -i 's/^VAPN_VERSION=.*/VAPN_VERSION=v1.3.0/' .env
docker compose pull
docker compose up -d                      # migrate runs first; services follow
docker compose ps                         # all healthy?
vapnctl status                            # fleet unaffected?
```

Zero-downtime notes: workers buffer through the seconds of coordinator
restart; nothing is lost. Migrations run under an advisory lock — a second
`up -d` is always safe.

## Rollback (platform)

```sh
sed -i 's/^VAPN_VERSION=.*/VAPN_VERSION=v1.2.0/' .env
docker compose up -d
```

Migrations are forward-only by policy; releases that add schema do so
backward-compatibly (new tables/columns, never breaking old readers) so the
previous minor version always runs against the newer schema. A release that
cannot honor that must say so loudly in its notes and ships a
`down`-guidance section.

## Rollback (routing snapshot)

Independent of software versions:

```sh
vapnctl snapshots list
vapnctl snapshots rollback <version>      # refuses pruned versions
```

Workers converge on their next heartbeat (downgrade logged with a warning
worker-side). Retention keeps 5 snapshots rollback-eligible by default
(`VAPN_RETAIN_SNAPSHOTS`).

## Order of operations for risky releases

1. Announce in the operator channel; note the current `VAPN_VERSION`.
2. Take an extra backup: `sudo systemctl start vapn-backup.service`.
3. Upgrade; watch the Grafana fleet dashboard for one full consensus window
   (ingest rate, 5xx ratio, outbox depth all steady).
4. Keep the previous image tag pullable — never delete release tags.

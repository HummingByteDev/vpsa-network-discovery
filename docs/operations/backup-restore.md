# Backup, Restore, Disaster Recovery

## What needs backing up

| Data | Where | Backup |
|---|---|---|
| PostgreSQL (all schemas) | `pgdata` volume | **nightly pg_dump** (the backup) |
| `.env` incl. signing key | `/opt/vapn/deploy/prod/.env` | copy to your secret store on every change |
| Routing artifacts | object store | provider-side redundancy; rebuildable from RIS |
| GeoLite2 mmdb | geoip dir | re-downloadable (MaxMind) |
| Caddy TLS material | `caddy_data` volume | re-issuable automatically |

Only the database and `.env` are irreplaceable. Worker keys live on the
workers.

## Backups

`deploy/prod/scripts/backup.sh` (installed as `vapn-backup.timer`, nightly
03:15 UTC): compressed custom-format `pg_dump`, readback-verified with
`pg_restore --list`, local retention 14 dumps, optional offsite copy via
`VAPN_BACKUP_S3_URI`.

**Configure the offsite copy** — a backup on the same disk as the database is a
convenience, not a backup.

> ⚠️ **`VAPN_BACKUP_S3_URI` in `.env` is ignored by the timer.** The script
> reads it from its process environment and `vapn-backup.service` has no
> `EnvironmentFile=`, so the nightly run never sees it. Set it on the unit:
>
> ```sh
> sudo systemctl edit vapn-backup.service
> ```
> ```ini
> [Service]
> Environment=VAPN_BACKUP_S3_URI=s3://your-bucket/vapn-backups
> ```
>
> Then verify a scheduled run actually copies offsite — the script prints
> `offsite copy ok: …` on success. The `aws` or `mc` CLI must be configured for
> root, the user the timer runs as.

Manual run: `sudo systemctl start vapn-backup.service` or
`VAPN_BACKUP_S3_URI=s3://… ./scripts/backup.sh /path/out` (invoked by hand, the
environment works normally).

## Restore

```sh
scripts/restore.sh /var/backups/vapn/vapn-<stamp>.dump
```

Stops coordinator/aggregator, `pg_restore --clean` replaces contents,
restarts services. Afterwards `vapnctl status` should show the fleet; workers
reconnect automatically (their identities are in the registry backup — no
re-enrollment).

**Drill this quarterly** against a scratch postgres container:

```sh
docker run -d --name vapn-drill -e POSTGRES_USER=vapn -e POSTGRES_PASSWORD=x \
  -e POSTGRES_DB=vapn postgres:16-alpine
docker exec -i vapn-drill pg_restore -U vapn -d vapn --no-owner < backup.dump
docker exec vapn-drill psql -U vapn -d vapn -c "select count(*) from registry.worker"
docker rm -f vapn-drill
```

## Disaster recovery: full VM loss

RTO ~30–60 min; RPO = last nightly dump (up to 24 h of measurements —
acceptable: history rebuilds, verdicts recompute from new measurements
within minutes of the fleet reconnecting).

1. Provision a new VM, install Docker, clone the repo to `/opt/vapn`.
2. Restore `.env` from the secret store (this carries the signing key and
   admin/advisor credentials).
3. Point DNS at the new VM (workers retry with backoff for days — the fleet
   will follow when the record propagates; low TTL helps).
4. `docker compose up -d` → `scripts/restore.sh <latest offsite dump>`.
5. `sudo systemctl enable --now vapn-builder.timer vapn-backup.timer`;
   trigger a build.
6. Verify: `vapnctl status` (workers re-heartbeating, snapshot fresh),
   outbox draining, VPS Advisor receiving statuses.

Losses in this scenario: measurements since the last dump, in-flight nonces
(harmless), lease state (rebuilt within one lease TTL).

### If the signing key was lost with the VM

Snapshot publishing halts (workers reject unsigned/foreign manifests — by
design). Generate a new keypair, publish the new public key through the
worker release channel (`vapn` CLI config update / new worker env), and
rebuild. This is the expensive scenario the secret-store copy of `.env`
exists to prevent.

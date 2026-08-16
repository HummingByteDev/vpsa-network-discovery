# Runbooks

One section per alert (anchors referenced from `alerts.yml`), plus scenarios
without a dedicated alert. Universal first steps: `vapnctl status`,
`docker compose ps`, `docker compose logs --since 15m <service>`.

## coordinator-down

Workers cannot lease, upload, or heartbeat; they buffer observations
(bounded queue, ~4096) and retry — short outages lose nothing.

1. `docker compose ps coordinator` — restarting? `docker compose logs coordinator`.
2. Common causes: postgres down (see its logs; the coordinator's `/readyz`
   names the failing dependency), bad config after an edit, disk full.
3. `docker compose up -d coordinator` after fixing the cause. Recovery is
   automatic fleet-wide within one heartbeat interval; no worker action needed.

## aggregator-down

Measurements keep flowing (coordinator ingests independently); only
consensus and publication stall. Same diagnosis pattern as above. On
recovery the aggregator processes settled windows it missed — data is not
lost, published verdicts just lag.

## snapshot-build-failure

`vapn_snapshot_age_seconds > 18h` — at least two consecutive builds failed.

1. `systemctl status vapn-builder.service` and
   `journalctl -u vapn-builder.service --since -1d`.
2. **Exit 2 — sanity gate**: the new snapshot deviated >50% from the
   published one. This is a *feature* refusing plausible garbage (RIS
   outage, truncated download, advisor catalog glitch). Inspect with
   `vapnctl snapshots list`; if the swing is legitimate (e.g. many providers
   onboarded), re-run once with `VAPN_SANITY_FORCE=true` in the builder env.
3. **Exit 1**: read the error. Download failures (RIS mirror down → set
   `VAPN_RIS_BVIEW_URL` to another collector, e.g. rrc01), S3 failures
   (check credentials/endpoint), advisor unreachable (sync errors are hard
   failures by design — the catalog is the source of truth).
4. No urgency panic: workers keep probing the *current* snapshot
   indefinitely; staleness costs freshness of targets, not uptime.
5. If a bad snapshot was *published* (targets collapsed, wrong providers):
   `vapnctl snapshots rollback <previous-version>` — workers converge on
   their next heartbeat.

## approved-worker-stuck-pending

An operator approved a worker on VPS Advisor, the website shows it approved,
and the worker keeps logging `awaiting approval`.

**What this always means:** the coordinator never received the decision.
Approval is a *pull* — the website records a decision and the coordinator
fetches it about every two minutes ([integration §4.3](../integration/django-integration.md#43-admin-decisions)).
The worker is not caching anything: it asks on every heartbeat and reports
whatever the coordinator's database says. So the break is between the website
and the coordinator, never between the coordinator and the worker.

1. `vapnctl status` — read the **Advisor decisions** line. `FAILING` names the
   error directly; that error is the whole diagnosis.
2. `docker compose logs --since 15m coordinator | grep -i advisor`. The
   coordinator logs the base URL it is calling with every failure, plus
   `workers are waiting on approvals that cannot be fetched` while any worker
   is pending.
3. Match the error:
   - **404, or `redirects to …`** → `VAPN_ADVISOR_URL` is wrong. It must be the
     **bare site address with no path** — the client appends
     `/api/v1/monitoring/...` itself. A trailing `/api` produces
     `/api/api/v1/...`; a `www.` host that redirects to the apex (or the other
     way) cannot be followed at all, because a redirect drops the
     `Authorization` header. Set it to the address the site answers on
     directly.
   - **401** → `VAPN_ADVISOR_TOKEN` is not a credential the website accepts.
     Re-issue it from the website's platform-credential screen.
   - **connection refused / timeout** → network path or DNS from the
     coordinator host. Check from inside the container:
     `docker compose exec coordinator wget -qO- $VAPN_ADVISOR_URL/api/v1/monitoring/providers`
4. Fix the value in `.env`, then `docker compose up -d coordinator`. The first
   pass after start asks for the **whole** decision feed, so every approval
   made while it was broken applies at once — no re-approval, no reinstall.
5. Confirm: `vapnctl workers list` shows the worker `active`, and its logs show
   `worker approval detected` within one heartbeat interval (30 s default).

> The same misconfiguration also stops the builder — every build begins with a
> provider sync — so an empty artifact bucket alongside stuck approvals is one
> fault, not two. See [snapshot-build-failure](#snapshot-build-failure).

**Prometheus:** `vapn_advisor_sync_last_success_timestamp_seconds{feed="decisions"}`
going stale is the alertable signal; `vapn_advisor_sync_total{outcome="error"}`
counts the failures.

## outbox-backlog

Pushes to VPS Advisor failing; backoff caps at 5 min per row, nothing is
dropped.

1. `docker compose logs --since 30m aggregator | grep "publication failed"` —
   the error names the endpoint and status.
2. 401/403 → rotated/revoked service credential: update
   `VAPN_ADVISOR_TOKEN`, `docker compose up -d aggregator`.
3. 5xx/timeouts → website-side incident; the queue drains itself once it
   recovers. Only act if growth threatens disk (it won't for days).
4. Persistent 4xx → contract drift; compare payloads against
   docs/integration/django-integration.md §4.4 and escalate to the
   website team. 4xx rows retry forever — after resolution they drain
   automatically.

## fleet-loss

Active workers at zero or halved.

1. `vapnctl workers list` — states tell the story. All `suspended`? Check
   the audit log (`vapnctl audit --category admin`) for a bad bulk action or
   an advisor decision sync gone wrong.
2. Workers active-but-silent (stale heartbeats): most often an edge/TLS
   problem — verify `curl -s https://$DOMAIN/api/v1/workers/me` returns 401
   (up, refusing unsigned) not 5xx/timeout; check Caddy logs and cert expiry.
3. A coordinated worker-version failure (bad release): check
   `vapnctl status` software versions; roll the worker release back (workers
   auto-update to whatever the release channel offers — see
   releases-and-upgrades.md).
4. Genuine community churn: nothing operational to do; scheduler
   redistributes within one lease TTL and redundancy absorbs it (verified to
   25% simultaneous loss in tests).

## security-event-spike

Replays, bad signatures, or consensus disagreement climbing.

1. `vapnctl audit --category security` and
   `select event_type, worker_id, count(*) from registry.trust_event where created_at > now() - interval '1 hour' group by 1,2 order by 3 desc;`
2. Single worker → likely compromise or broken clock/state:
   `vapnctl workers quarantine <id> --reason "security spike"` (zero public
   weight, keeps measuring in shadow) or `suspend` (hard lockout). Demand
   `vapnctl workers rotate-key <id>` on recovery.
3. Many workers, one operator/subnet → coordinated: suspend the set; consider
   `vapnctl scheduler pause` while you assess (fleet idles within one lease
   interval; measurements resume seamlessly on `resume`).
4. Trust weighting already discounts liars automatically — the spike alert is
   about *investigating*, the scoring holds the line meanwhile.

## data-plane-stalled

Active workers, zero ingest.

1. Was the scheduler paused and forgotten? `vapnctl status` shows PAUSED.
2. Zero assignments? Targets may have collapsed with a bad snapshot —
   `vapnctl snapshots list`, roll back if so.
3. Uploads rejected wholesale (signature verification)? Coordinator logs will
   show it; a platform key-handling bug would look like this — check recent
   deploys and roll back the platform version (releases-and-upgrades.md).

## http-errors

5xx ratio >5%: almost always postgres (connection exhaustion, disk) or a bad
deploy. `docker compose logs coordinator | grep -i error`, check postgres,
roll back the release if it started with one.

## worker-flood (no alert; admin-observed)

Mass registrations (abuse of the enrollment path):

- Enrollment always requires a valid one-time token (created via website or
  admin), so a flood implies token leakage or a hostile operator account —
  suspend the operator's workers, let the website team disable the account.
- The nonce/replay tables self-prune; junk `pending` workers are inert (no
  work, no weight) and can be bulk-retired via `vapnctl workers list` + a
  shell loop.

## compromise-response

See [security.md](security.md#compromise-response) for
the full procedure (platform host, worker credential, and signing-key
scenarios).

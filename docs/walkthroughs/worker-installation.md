# Walkthrough: Worker Installation

What *actually* happens between running the installer and having a probing
worker. This traces [Stage 6](end-to-end.md#stage-6--workers-detect-verify-and-download-it)
of the end-to-end flow from the worker's side. For the operator-facing how-to,
see [Getting Started → Installation](../getting-started/installation.md).

```mermaid
sequenceDiagram
  participant U as Operator (browser)
  participant S as VPS Advisor
  participant I as install.sh / vapn CLI
  participant W as Worker container
  participant C as Coordinator

  U->>S: Create worker (My Workers)
  S-->>U: One-time enrollment token
  U->>I: run install.sh + paste token
  I->>I: check Docker, disk, clock, coordinator
  I->>W: start container with token + coordinator URL
  W->>W: generate Ed25519 keypair (private key stays local)
  W->>C: POST /register (token + public key + facts)
  C->>S: verify token hash against pending enrollment
  C-->>W: worker_id, state = pending
  Note over W,C: worker heartbeats; no work until approved
  U->>S: Admin approves worker
  S-->>C: decision synced (state = active)
  C-->>W: next heartbeat: active + snapshot version
  W->>W: download + verify snapshot, lease work, probe
```

## Step by step

1. **Operator creates the worker on VPS Advisor.** The website generates a
   random ≥32-byte **enrollment token**, shows it once, and stores only its
   sha256. A `monitoring_worker` row exists in state `pending`.

2. **The installer bootstraps the CLI.** `install.sh` verifies Docker, detects
   the architecture, downloads the `vapn` binary from GitHub releases, and hands
   off to `vapn install`. → [Installer source](../../deploy/worker/install.sh).

3. **System checks run** (`vapn doctor`): Docker reachable, disk space,
   coordinator reachable over HTTPS, clock synchronized. Each failure blocks
   with a specific fix.

4. **The worker generates its identity.** On first boot it creates an **Ed25519
   keypair**. The **private key never leaves** the `~/.vapn` volume — not in
   logs, not to the coordinator, never. This is what makes measurements
   attributable to *you* without the platform being able to impersonate you.

5. **The worker registers.** `POST /api/v1/workers/register` with the enrollment
   token, its public key, and facts (version, self-reported location). The
   coordinator checks the token hash against VPS Advisor's pending enrollments,
   creates the worker record, and returns a `worker_id` in state `pending`. The
   token is now spent.

6. **It waits, heartbeating.** A `pending` worker may heartbeat but gets no
   assignments and contributes zero weight. The operator sees "awaiting
   approval."

7. **An admin approves it** on VPS Advisor. That decision syncs to the
   coordinator (poll every ~2 min, or webhook), which flips the worker to
   `active`.

8. **The worker goes to work.** On its next heartbeat it's told it's active and
   which snapshot version is current. It downloads and cryptographically
   verifies the snapshot ([auth walkthrough](worker-authentication.md)), leases
   assignments, and starts probing ([measurement walkthrough](measurement-lifecycle.md)).

## Why enrollment is split this way

Enrollment deliberately spans two systems: the **operator identity and approval
live on VPS Advisor** (humans, accounts, anti-abuse), while the **worker
operates against the coordinator** (high-volume, machine-facing). The one-time
token is the bridge — it lets the coordinator trust "a real operator started
this" without the worker ever holding a website session. See the
[API plane split](../architecture/01-system-architecture.md#2-api-plane-split-clarification-of-the-brief).

## What's on disk afterward

Everything lives under `~/.vapn` (override with `VAPN_HOME`):

| Path | Contents |
|---|---|
| `config.env` | coordinator URL, enrollment token (once), settings |
| `state/` | identity (private key!), downloaded snapshot, upload queue |
| `docker-compose.yml` | the generated worker service definition |

Back it up with `vapn backup` (it contains your private key — guard it).
Related: [command reference](../worker/command-reference.md) ·
[worker lifecycle](../worker/lifecycle.md).

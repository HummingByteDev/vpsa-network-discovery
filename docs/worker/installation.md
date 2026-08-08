# Install a Worker

Goal: a healthy VAPN worker probing from your machine, in about five minutes.

A **worker** is a small Docker container that measures VPS providers' public
network health from your location and reports signed results back. Running one
costs a few MB of RAM and a trickle of bandwidth, opens no inbound ports, and
you can pause or remove it at any moment — see
[resource usage & privacy](resources-and-privacy.md).

## Before you begin

You need three things:

1. A **Linux** machine (amd64 or arm64) that can run a long-lived container — a
   cheap VPS, a home server, or a spare box. It does not need to be powerful.
2. **Docker** installed and running.
3. Two values from whoever runs the platform (usually printed on your VPS
   Advisor dashboard under **My Workers**):
   - an **enrollment token**
   - the **snapshot public key**

> **What's an enrollment token?** A one-time password proving that *you* — a
> real, logged-in operator — started this worker. The worker trades it for a
> permanent cryptographic identity on first boot, after which it is useless. It
> is shown only once; if you lose it, regenerate it from the same page.

> **What's the snapshot public key?** Your worker only probes addresses from a
> list the platform publishes, and it refuses any list that isn't signed by the
> platform's key. This value is that key. It is not a secret — it is how your
> worker checks that nobody has tampered with its target list.

Check your machine is ready:

```sh
uname -m          # x86_64 or aarch64
docker info       # must succeed as your user, without sudo
timedatectl       # "System clock synchronized: yes"
```

> The clock matters: every request your worker makes is signed with a
> timestamp, and the platform rejects anything more than two minutes out. Turn
> on time sync with `sudo timedatectl set-ntp true`.

If `docker info` fails with a permission error:

```sh
sudo usermod -aG docker "$USER"      # then log out and back in
```

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/HummingByteDev/vpsa-network-discovery/main/deploy/worker/install.sh | bash
```

> This downloads the small `vapn` command-line tool for your architecture and
> immediately runs `vapn install`, which does the real work.

**Prefer to read a script before running it?** That's encouraged — see
[other ways to install](#other-ways-to-install) below.

`vapn install` is interactive. It asks four questions:

| Prompt | What to enter |
|---|---|
| **Coordinator URL** | The platform address, e.g. `https://probes.vpsadvisor.com`. Press Enter to accept the default if it's already right |
| **Worker name** | A label for your own reference. Defaults to your machine's hostname |
| **Snapshot public key** | The public key you were given above |
| **Enrollment token** | Your one-time token. Only asked on a first install |

Then it runs eight system checks, writes its configuration to `~/.vapn`, starts
the container, and waits for the worker to report in:

```
✓ Docker CLI                 ✓ State directory writable
✓ Docker daemon              ✓ Snapshot public key
✓ Docker Compose             ✓ Coordinator reachable
✓ Disk space (≥2 GB free)    ✓ Clock synchronization

Starting worker
Waiting for the worker to come up...
✓ Registration successful

Worker ID:
9f30…

Status:
Awaiting approval
```

If a check fails, installation stops and the failing line tells you what's
wrong. Fix it and re-run `vapn install` — it is safe to run repeatedly.

## Verify it worked

```sh
vapn status
```

**Healthy result:** your worker ID, its software version, the coordinator it is
talking to, a recent heartbeat, and either `Awaiting approval` or `Healthy`.

```sh
vapn doctor
```

**Healthy result:** eight ticks. Run this any time something looks off.

If `vapn status` says *"no status report yet"*, the container is still starting.
Watch it with `vapn logs -f`.

## What "Awaiting approval" means

Your worker registered successfully and is now **pending**. A human
administrator reviews new workers before they contribute to public results —
a deliberate anti-abuse step.

**You don't have to do anything.** Once approved, the worker downloads the
routing snapshot and starts probing on its own. Approval status is visible on
your VPS Advisor dashboard. See [Worker lifecycle](lifecycle.md).

## You're done

The worker is self-managing. It renews its own credentials, downloads new
routing snapshots (verifying each one cryptographically), retries failed
uploads, and recovers from reboots on its own. You should rarely need to touch
it.

Day-to-day commands:

```sh
vapn status      # health, snapshot version, assignments, measurements
vapn logs -f     # live logs
vapn pause       # stop probing (identity and trust kept; resume any time)
vapn resume
vapn update      # update the worker image (health-gated, auto-rollback)
```

Full list: [Operating a worker](operations.md).

### Next steps

- **[Turn on automatic updates](operations.md#automatic-updates-recommended)** —
  recommended; the platform can require a minimum worker version.
- [What a worker does, what it costs you, and what it can see](resources-and-privacy.md)
- [Curious how your packets become a public verdict?](../walkthroughs/end-to-end.md)

---

## Other ways to install

The canonical source is **GitHub** — auditable, versioned, and published by
release tooling. Three ways to install, in increasing order of "show me exactly
what runs":

### 1. Piped installer (fastest)

```sh
curl -fsSL https://raw.githubusercontent.com/HummingByteDev/vpsa-network-discovery/main/deploy/worker/install.sh | bash
```

### 2. Download, read, then run (recommended)

```sh
curl -fsSL -o vapn-install.sh \
  https://raw.githubusercontent.com/HummingByteDev/vpsa-network-discovery/main/deploy/worker/install.sh
less vapn-install.sh      # read it — it's short and commented
bash vapn-install.sh
```

Piping a script into a shell means trusting whatever the URL serves. Reading it
first costs a minute and removes the trust assumption. The script only checks
Docker, downloads the `vapn` binary for your architecture, and runs
`vapn install`.

### 3. Clone and build (contributors, air-gapped installs)

```sh
git clone https://github.com/HummingByteDev/vpsa-network-discovery.git
cd vpsa-network-discovery
make build            # produces ./bin/vapn (needs Go)
./bin/vapn install
```

Use this to build from source, pin to a specific commit, or install somewhere
without direct GitHub access.

## What `vapn install` actually does

For readers who want the mechanics. This is
[Stage 6](../walkthroughs/end-to-end.md#stage-6--workers-detect-verify-and-download-it)
of the end-to-end flow, from the worker's side.

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
  I->>I: system checks (Docker, disk, key, coordinator, clock)
  I->>W: generate compose file, start container
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

1. **The operator creates the worker on VPS Advisor.** The website generates a
   random enrollment token, shows it once, and stores only its sha256 hash.

2. **`install.sh` bootstraps the CLI.** It confirms Docker is present and the
   daemon reachable, detects your architecture, downloads the `vapn` binary
   from GitHub releases into `/usr/local/bin` (or `~/.local/bin` if that isn't
   writable), and hands off to `vapn install`.

3. **System checks run.** Docker CLI, Docker daemon, Docker Compose, ≥2 GB free
   disk, a writable state directory, a well-formed snapshot public key, the
   coordinator reachable over HTTPS, and clock skew under 90 seconds. Any
   failure blocks the install with a specific message.

4. **The CLI writes the configuration and generates a compose file.** Settings
   go to `~/.vapn/config.env`; `~/.vapn/docker-compose.yml` is generated with
   the image inlined so every later Docker call is self-contained. **Edit
   `config.env`, never the compose file** — it is regenerated.

5. **The worker generates its identity.** On first boot it creates an **Ed25519
   keypair**. The **private key never leaves `~/.vapn/state`** — not in logs,
   not to the coordinator, never. This is what makes measurements attributable
   to you without the platform being able to impersonate you.

6. **The worker registers.** `POST /api/v1/workers/register` with the
   enrollment token, its public key, and its facts. The coordinator checks the
   token hash against VPS Advisor's pending enrollments, creates the worker
   record, and returns a `worker_id` in state `pending`. The token is now spent
   — the CLI deletes it from `config.env` on success.

7. **It waits, heartbeating.** A `pending` worker may heartbeat but gets no
   assignments and contributes zero weight.

8. **An admin approves it** on VPS Advisor. That decision syncs to the
   coordinator (polled about every two minutes), which flips the worker to
   `active`.

9. **The worker goes to work.** On its next heartbeat it is told it is active
   and which snapshot version is current. It downloads the snapshot, verifies
   the sha256 and the Ed25519 signature against your pinned public key, swaps
   it in atomically, leases assignments, and starts probing.

### The generated container

`~/.vapn/docker-compose.yml` looks like this:

```yaml
name: vapn-worker
services:
  worker:
    image: ghcr.io/hummingbytedev/vapn-worker:latest
    restart: unless-stopped          # survives reboots
    user: "1000:1000"                # runs as you, not root
    cap_add: [NET_RAW]               # the ONE capability it needs (ICMP)
    sysctls:
      net.ipv4.ping_group_range: "0 2147483647"
    env_file: config.env
    environment:
      VAPN_STATE_DIR: /state
    volumes:
      - ./state:/state               # identity + snapshot + upload queue
    logging:
      driver: json-file
      options: { max-size: "10m", max-file: "3" }
```

Key points:

- **`cap_add: [NET_RAW]` and nothing else.** Sending an ICMP echo ("ping")
  needs a raw socket, which needs that one Linux capability. The container is
  **not** privileged, does not use host networking, and cannot see your files.
- **It runs as your user**, so the bind-mounted state directory — with your
  private key in it — stays owned by you.
- **`restart: unless-stopped`** means the worker returns automatically after a
  reboot or Docker restart.

### What's on disk afterward

Everything lives under `~/.vapn` (override with `VAPN_HOME`):

| Path | Contents |
|---|---|
| `config.env` | Coordinator URL, worker name, snapshot public key, image |
| `state/worker.key` | **Your private key** |
| `state/worker.id` | The assigned worker ID |
| `state/routing.sqlite` | The verified routing snapshot |
| `state/status.json` | What `vapn status` reads |
| `docker-compose.yml` | Generated — do not edit |

Back it up with `vapn backup` (the archive contains your private key — guard
it). See [Operating a worker](operations.md#backup).

### Configuration surface

Only three settings matter, and `vapn install` collects all of them:

| Variable | Required | Purpose |
|---|---|---|
| `VAPN_COORDINATOR_URL` | yes | The platform endpoint the worker talks to |
| `VAPN_SNAPSHOT_PUBLIC_KEY` | yes | Verifies every routing snapshot before use |
| `VAPN_ENROLLMENT_TOKEN` | first boot only | One-time proof a real operator started it |
| `VAPN_WORKER_NAME` | no | A label for your own reference |
| `VAPN_WORKER_IMAGE` | no | Pin a specific worker image |
| `VAPN_HOME` | no | Override the `~/.vapn` location |

The complete list is in the
[configuration reference](../reference/configuration.md#worker).

---

**Something not working?** → [Troubleshooting](troubleshooting.md) ·
**Every command:** → [Operating a worker](operations.md) ·
**Why it behaves the way it does:** → [Worker lifecycle](lifecycle.md)

# Worker Installation

This guide explains worker installation in depth — the same flow as the
[Quick Start](quickstart.md), but with the reasoning behind each choice and the
options you can take. If you just want a worker running, the Quick Start is
enough; come back here when you want to know *what* you're installing and
*why*.

## Requirements

| Requirement | Why | Notes |
|---|---|---|
| **Linux**, amd64 or arm64 | The worker sends raw ICMP; it targets Linux hosts | A cheap VPS, a Raspberry Pi, or a spare box all work |
| **Docker** | The worker ships as a container so you don't manage Go, libraries, or system packages | `docker info` must succeed for your user |
| **An enrollment token** | Proves a real operator started this worker | From VPS Advisor → My Workers → Create worker |
| **Outbound HTTPS (443)** | The worker talks only to the coordinator and the artifact CDN | No inbound ports are opened |
| A synchronized **clock** | Signed requests carry a timestamp; large clock skew is rejected as a replay defense | `timedatectl set-ntp true` |

> **What is CAP_NET_RAW?** Sending an ICMP echo ("ping") packet requires a
> raw socket, which needs one Linux capability: `CAP_NET_RAW`. The worker
> container requests exactly that one capability and nothing else — it is not
> privileged, cannot see your files, and cannot touch the host network stack
> beyond sending pings. See [privacy](../worker/resources-and-privacy.md#privacy).

## Choose how you install

The worker's canonical source is **GitHub** — auditable, versioned, and signed
by release tooling. There are three ways to install, in increasing order of
"I want to see exactly what runs":

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
first costs a minute and removes the trust assumption. The script only detects
Docker, downloads the `vapn` binary for your architecture, and runs
`vapn install`.

### 3. Clone the repository (for contributors / air-gapped installs)

```sh
git clone https://github.com/HummingByteDev/vpsa-network-discovery.git
cd vpsa-network-discovery
make build            # produces ./bin/vapn (needs Go)
./bin/vapn install
```

Use this if you want to build from source, pin to a specific commit, or install
somewhere without direct GitHub access.

## What `vapn install` does, step by step

`install.sh` only bootstraps the CLI; the real work is in `vapn install`, which
is interactive and idempotent:

1. **System checks (`vapn doctor`).** Docker present and reachable, enough disk
   space, coordinator reachable over HTTPS, clock synchronized. Any failure
   stops installation with a specific fix.
2. **Collects two settings:** the **coordinator URL**
   (`VAPN_COORDINATOR_URL`, e.g. `https://probe-api.vpsadvisor.example`) and
   your **enrollment token** (`VAPN_ENROLLMENT_TOKEN`). These are written to
   `~/.vapn/config.env`.
3. **Generates your identity.** On first boot the worker creates an
   [Ed25519](../concepts/measurement-and-consensus.md#trust) keypair. The
   **private key never leaves the container volume** (`~/.vapn`). The public
   key is what you register.
4. **Registers** with the coordinator using the enrollment token + public key.
   The token is now spent; the worker has a permanent ID and is `pending`.
5. **Downloads the routing snapshot** — the signed list of legitimate probe
   targets — and verifies its signature against a pinned key before using it.
6. **Optionally downloads GeoLite2** if you supplied a MaxMind license key
   (see below). Geo features degrade gracefully without one.
7. **Starts the container** (`restart: unless-stopped`) and **verifies** it
   reports healthy.

Everything the worker needs lives under `~/.vapn` (override with `VAPN_HOME`):
identity, config, the downloaded snapshot, and the generated
`docker-compose.yml`.

## Optional: a MaxMind license key

VAPN uses [MaxMind GeoLite2](../concepts/geoip.md) to attribute measurements to
countries/regions. The *platform* already geolocates providers; a worker only
needs a key if you want your worker's own location verified from a local
database rather than inferred. It is entirely optional — leave it unset and the
worker still contributes fully. To supply one, get a free license key from
MaxMind and set `VAPN_MAXMIND_LICENSE_KEY` when prompted. The key is **yours**
and is never redistributed by the project (see
[risk R8](../architecture/08-risk-assessment.md)).

## After installation

- The worker is `pending` until an admin approves it — see
  [Quick Start → Awaiting approval](quickstart.md#what-awaiting-approval-means).
- Verify any time with `vapn status` and `vapn doctor`.
- Turn on [automatic updates](updating.md).
- Learn the [full command set](../worker/command-reference.md).

## Uninstalling

Leaving is one command and removes everything cleanly, including your identity:

```sh
vapn uninstall
```

See [Uninstalling](uninstalling.md) for what it removes and how to unregister
politely so the platform stops assigning you work.

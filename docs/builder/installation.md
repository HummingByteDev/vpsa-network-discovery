# Install the Builder

This guide takes you from a **freshly installed VPS** to a **running builder
that has published its first signed routing snapshot**. Follow it top to
bottom; each step says what it does, what to type, and how to check it worked.

You do **not** need to understand BGP, ASNs, RIPE, PostgreSQL, Docker, or
cryptography to complete it. In one sentence, here is what you are installing:

> The builder downloads a copy of the Internet's routing information, works out
> which addresses belong to each VPS provider being monitored, signs that list
> so nobody can tamper with it, and publishes it for community workers to use.

If you want to know *how* it does that, read [How the builder
works](README.md) — afterwards. Nothing in this guide requires it.

> **Who needs this guide?** Only **platform operators** — the people running
> VAPN itself. If you just want to contribute measurements from a spare
> machine, you want [Install a worker](../worker/installation.md) instead. That
> is a completely different, much shorter task.

## What you are about to build

The builder is not a server that runs continuously. It is a **scheduled job**:
it wakes up, does one build, publishes the result, and exits. But it needs the
rest of the platform to exist first — a database to write to, a place to put
the finished file, and the list of providers to monitor. So this guide brings
up the whole VAPN platform on one machine, then runs the builder on it.

```
your VPS
├── PostgreSQL        stores the routing data
├── coordinator       the service community workers talk to
├── aggregator        combines worker measurements into verdicts
├── Caddy             handles HTTPS for you
└── builder           ← the scheduled job this guide is about
```

Time required: about 45 minutes, most of it waiting for the first build.

---

## Before you begin

Collect these five things. You cannot finish without them, and gathering them
now saves you a stalled install later.

| You need | Why | Where to get it |
|---|---|---|
| **A Linux VPS** you can SSH into | Everything runs here | Ubuntu LTS, 4 vCPU / 8 GB RAM / 80 GB disk. Smaller works for a test, but a real build needs ~10 GB free disk and a few GB of RAM |
| **A domain name** pointing at that VPS | Workers connect to it over HTTPS; certificates are issued automatically | Any registrar. Create an `A` record (and `AAAA` if you have IPv6) for e.g. `probes.example.com` → your VPS's IP |
| **An S3-compatible storage bucket** + access keys | Where the finished snapshot file is published so workers can download it | Backblaze B2, Cloudflare R2, or AWS S3 all work. Create a private bucket and one key pair scoped to it |
| **A MaxMind account** (free) | Adds country/city information to each address so verdicts can be regional | [maxmind.com](https://www.maxmind.com) → sign up → create a licence key. Note the **account ID** and the **licence key** |
| **A VPS Advisor service token** | The builder asks VPS Advisor which providers to monitor. Without it there is nothing to build | From the VPS Advisor website team |

You do **not** need a RIPE account. The routing data the builder downloads is
public and free.

> **Don't have a VPS Advisor token yet?** You can still complete this guide
> using the `mockadvisor` stub that ships in this repository — it serves the
> same contract from fixtures. See
> [Testing without VPS Advisor](#testing-without-vps-advisor) at the end.

---

## Step 1 — Prepare the VPS

Connect to your machine:

```sh
ssh your-user@probes.example.com
```

> Replace `probes.example.com` with your domain (or the VPS's IP address if DNS
> hasn't propagated yet).

Install Docker, which is the only software the platform needs:

```sh
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"
```

> The first command installs Docker Engine and the Compose plugin. The second
> lets your user run Docker without `sudo`.

**Log out and back in** so the group change takes effect:

```sh
exit
ssh your-user@probes.example.com
```

Make sure the machine's clock is synchronised:

```sh
sudo timedatectl set-ntp true
```

> Workers sign every request with a timestamp, and the platform rejects
> requests whose clock is more than two minutes off. A drifting clock on this
> machine would lock out your entire worker fleet.

### Verify

```sh
docker --version
docker compose version
docker info >/dev/null && echo "Docker is working"
timedatectl | grep -i synchronized
```

**Healthy result:** a Docker version, a Compose version, `Docker is working`,
and `System clock synchronized: yes`. If `docker info` fails with a permission
error, you skipped the log-out/log-in.

---

## Step 2 — Download the project

```sh
sudo mkdir -p /opt/vapn && sudo chown "$USER" /opt/vapn
git clone https://github.com/HummingByteDev/vpsa-network-discovery /opt/vapn
cd /opt/vapn/deploy/prod
```

> This downloads the project into `/opt/vapn` and moves you into the production
> deployment directory, which holds the configuration files you will edit.

**Use `/opt/vapn` exactly.** The scheduled-job definitions you install in
[Step 8](#step-8--run-the-builder-automatically) expect that path.

### Verify

```sh
ls
```

**Healthy result:** you see `docker-compose.yml`, `.env.example`, `Caddyfile`,
`systemd/`, `scripts/`, and `monitoring/`. If `git` is missing, install it with
`sudo apt install -y git` and retry the clone.

---

## Step 3 — Generate the snapshot signing key

This is the most important step in the guide, so it gets its own explanation.

### Why this key exists

Workers download the address list the builder produces. That list tells them
what to send network probes to. If someone could substitute their own list,
they could point thousands of machines at a victim's network.

To make that impossible, the builder **signs** every snapshot it publishes with
a private key, and every worker **verifies** that signature before using the
snapshot. A list that isn't signed by your key is refused — even if the
attacker fully controls the storage bucket it came from.

You are about to create that key pair. It has two halves:

| Half | Variable | Who holds it | What it does |
|---|---|---|---|
| **Private key** | `VAPN_SNAPSHOT_SIGNING_KEY` | Only the builder, only on this VPS | Signs each snapshot. **Secret.** |
| **Public key** | `VAPN_SNAPSHOT_PUBLIC_KEY` | Every worker operator | Verifies the signature. Safe to publish anywhere. |

> ⚠️ **Protect the private key.**
>
> - **Never** commit it, paste it into a chat, or email it.
> - **Never** put it on a worker. Workers only ever need the *public* key.
> - Keep one copy in your team's password manager or secret store. It lives on
>   this VPS in a file called `.env`, which you will lock down in Step 4.
>
> **If you lose the private key:** you cannot publish new snapshots. You must
> generate a new key pair and get the new *public* key to every worker
> operator, because their workers will refuse snapshots signed by an unknown
> key. Workers keep running on their last good snapshot in the meantime, so
> this is disruptive, not fatal.
>
> **If the private key leaks:** an attacker who can also write to your storage
> bucket could feed workers a forged address list. Treat it as an incident:
> generate a new key pair, roll out the new public key, rotate the storage
> credentials, and review the bucket's access logs. The
> [security guide](../operations/security.md#compromise-response) has the full
> procedure.

### Generate it

```sh
cd /opt/vapn
docker build --build-arg COMPONENT=keygen -t vapn-keygen .
docker run --rm vapn-keygen
```

> The first command builds a tiny throwaway image containing the key generator;
> the second runs it. Nothing is stored — the key exists only in the output you
> are about to copy.

**You will see exactly two lines:**

```
VAPN_SNAPSHOT_SIGNING_KEY=XcOyiFle7/85M7gl5LKmLW/4pmphUfX/3Lcq/FuAF0I=
VAPN_SNAPSHOT_PUBLIC_KEY=hJJgj1Wx9sQDf0SCo2JtlYUcFt/BtFoyGrQTubh7uxM=
```

**Copy both lines somewhere safe right now.** The command does not save them
and running it again produces a *different* key pair.

- The **first** line goes into your configuration in the next step.
- The **second** line is what you give worker operators. They are prompted for
  it when they run `vapn install`, so publish it wherever you tell people how
  to join — a README, your website, the onboarding email.

Go back to the deployment directory:

```sh
cd /opt/vapn/deploy/prod
```

### Verify

Both lines end with `=` and are about 44 characters after the `=` sign. If you
got an error instead, the most likely cause is Docker running out of disk while
building — check with `df -h /`.

---

## Step 4 — Configure the builder

Create your configuration file from the template:

```sh
cp .env.example .env
chmod 600 .env
nano .env
```

> `.env` is where every setting and secret lives. `chmod 600` makes it readable
> only by you — important, because it now contains your signing key.

Work through the file group by group. Everything below is required unless
marked optional.

### Group 1 — Where your platform lives

| Setting | What it does | What to enter |
|---|---|---|
| `VAPN_DOMAIN` | The public address workers connect to. Caddy obtains an HTTPS certificate for it automatically | Your domain, e.g. `probes.example.com` |
| `VAPN_ADMIN_ALLOW_CIDR` | Which IP addresses may reach the administration API | Your own IP with `/32`, e.g. `203.0.113.7/32`. Find it with `curl -s ifconfig.me` **from your laptop**, not the VPS |

### Group 2 — Secrets

Generate the two passwords with `openssl rand -hex 32` (run it twice; use a
different value for each).

| Setting | Secret? | What it does | What to enter |
|---|---|---|---|
| `VAPN_DB_PASSWORD` | 🔒 | Password for the platform's own database | Output of `openssl rand -hex 32` |
| `VAPN_ADMIN_TOKEN` | 🔒 | Password for the administration API and the `vapnctl` tool | Output of `openssl rand -hex 32` |
| `VAPN_SNAPSHOT_SIGNING_KEY` | 🔒🔒 | Signs every snapshot (Step 3) | The **first** line from `keygen` — private key only |

### Group 3 — VPS Advisor

The builder asks VPS Advisor which providers to monitor. Without this, a build
has nothing to look for and fails.

| Setting | Secret? | What it does | What to enter |
|---|---|---|---|
| `VAPN_ADVISOR_URL` | | Base address of the VPS Advisor site | e.g. `https://www.vpsadvisor.example` — **no trailing path**, the platform appends `/api/v1/monitoring/...` itself |
| `VAPN_ADVISOR_TOKEN` | 🔒 | Proves the platform is allowed to read the provider list | The service token from the website team |

### Group 4 — Where snapshots are published

The finished snapshot is uploaded to an S3-compatible bucket. Workers download
it from there. The bucket needs no special trust — the signature from Step 3 is
what makes the file trustworthy — but keep it **private** and scope the keys to
that one bucket.

| Setting | Secret? | What it does | What to enter |
|---|---|---|---|
| `VAPN_ARTIFACT_S3_ENDPOINT` | | Your storage provider's address | Backblaze B2: `s3.us-west-004.backblazeb2.com` · Cloudflare R2: `<account-id>.r2.cloudflarestorage.com` · AWS: `s3.<region>.amazonaws.com`. Host only — no `https://` |
| `VAPN_ARTIFACT_S3_BUCKET` | | Bucket name | Default `vapn-artifacts`, or your bucket's name |
| `VAPN_ARTIFACT_S3_REGION` | | Region | AWS: your region (required). Cloudflare R2: `auto`. Backblaze B2: the region in your endpoint, e.g. `us-east-005`, or leave empty |
| `VAPN_ARTIFACT_S3_ACCESS_KEY` | 🔒 | Storage username | From your storage provider. **Backblaze B2: create an *application key*** — the account master key is rejected by the S3 API |
| `VAPN_ARTIFACT_S3_SECRET_KEY` | 🔒 | Storage password | From your storage provider, shown only once |

Create the bucket yourself before the first build — the builder uploads into it
and never creates it. Keep it **private**; workers fetch snapshots through the
coordinator, not directly from the bucket.

### Group 5 — Location data (MaxMind)

| Setting | Secret? | What it does | What to enter |
|---|---|---|---|
| `MAXMIND_ACCOUNT_ID` | | Identifies your MaxMind account | Your account ID |
| `MAXMIND_LICENSE_KEY` | 🔒 | Downloads the GeoLite2 databases | Your licence key |
| `VAPN_GEOIP_DIR` | | Where the downloaded databases are stored | Leave as `./geoip` |

MaxMind's licence does not allow redistributing these databases, which is why
you bring your own key rather than the project shipping them.

### Group 6 — Routing data source

| Setting | What it does | What to enter |
|---|---|---|
| `VAPN_RIS_BVIEW_URL` | Which public routing-data collector to download from | Leave the default, `https://data.ris.ripe.net/rrc00/latest-bview.gz`. Change it only if that collector is unavailable (see [troubleshooting](#the-routing-download-fails)) |

### Group 7 — Optional

| Setting | What it does | What to enter |
|---|---|---|
| `VAPN_VERSION` | Which release of VAPN to run | `latest`, or a specific tag such as `v1.2.0` once you are in production |
| `VAPN_GRAFANA_PASSWORD` | Password for the optional dashboards | Any password, if you plan to enable monitoring |

Save and close the file (in `nano`: `Ctrl+O`, `Enter`, `Ctrl+X`).

> **There are more settings than these.** Everything above is what an ordinary
> operator needs. Tuning knobs — how many probe targets per provider, how
> aggressive the safety check is, how many old snapshots to keep — have
> sensible defaults and are documented in the
> [configuration reference](../reference/configuration.md#builder).

### Verify

```sh
grep -c '^VAPN_\|^MAXMIND_' .env
grep -E '^(VAPN_DB_PASSWORD|VAPN_ADMIN_TOKEN|VAPN_SNAPSHOT_SIGNING_KEY|VAPN_ADVISOR_TOKEN|VAPN_ARTIFACT_S3_SECRET_KEY)=$' .env
```

> The first command counts the settings you have. The second lists any required
> secret you left **empty**.

**Healthy result:** the second command prints nothing at all. Every line it
prints is a secret you still need to fill in.

---

## Step 5 — Start the platform

Bring up the database, the coordinator, the aggregator, and the HTTPS edge:

```sh
cd /opt/vapn/deploy/prod
docker compose up -d
```

> This downloads the container images and starts the services. The first run
> takes a few minutes. Database migrations run automatically before the
> services start.

> ⚠️ **Until the first public release is tagged, expect a `denied` line for
> every image**, like `ghcr.io/hummingbytedev/vapn-coordinator:latest error
> from registry: denied`. This is normal and not a problem with your setup —
> the published images do not exist yet, and **you do not need a Docker or
> GitHub account**. Compose falls back to building each image from the source
> you just cloned, which is why the next line says `Building`. The first build
> takes several minutes; afterwards the images are cached locally.

Now download the location databases, which the builder needs:

```sh
docker compose --profile geoip up -d
```

> This starts a small updater that fetches the MaxMind GeoLite2 databases into
> `./geoip` and refreshes them every three days.

**Wait for the download to finish before continuing** — the first build fails
if the database file isn't there yet:

```sh
docker compose logs geoipupdate
ls -lh geoip/
```

### Verify

```sh
docker compose ps
curl -sI "https://$(grep '^VAPN_DOMAIN=' .env | cut -d= -f2)/" | head -1
ls geoip/GeoLite2-City.mmdb
```

**Healthy result:**

- `docker compose ps` shows `caddy`, `postgres`, `coordinator`, and
  `aggregator` running, with the coordinator and aggregator marked `healthy`.
  The `migrate` service showing `exited (0)` is correct — it is a one-shot job.
- The `curl` returns `HTTP/2 200`. If it fails, DNS may not have propagated
  yet, or port 443 is blocked. Certificate issuance can take a minute.
- `GeoLite2-City.mmdb` exists.

**If a service is restarting:** `docker compose logs <service>` names the
problem. A missing or invalid setting is reported as
`bad configuration ... configuration errors: VAPN_...`, listing every variable
at fault at once.

---

## Step 6 — Run the first build

Now the actual builder. Run it once, by hand, so you can watch it work:

```sh
docker compose run --rm builder
```

> This runs a single build and exits. The `--rm` cleans up afterwards. There is
> no long-running builder process — this is the whole thing.

**This takes 10–40 minutes on the first run.** The routing snapshot it
downloads is several gigabytes. Later runs reuse a cached copy if it is less
than six hours old.

You will see log lines for each stage:

```
provider sync complete       asns=…
downloading RIS bview        url=https://data.ris.ripe.net/rrc00/latest-bview.gz
bview downloaded             bytes=…
extraction complete          rib_records=… matched=… prefix_rows=…
geo enrichment complete      prefixes=… resolved=…
snapshot loaded              version=20260808T0800Z-… prefixes=… targets=…
artifact published           version=… sha256=… targets=…
snapshot published           version=… elapsed=…
```

### Verify

```sh
echo "exit code: $?"
```

Run this **immediately** after the build finishes. The exit code tells you
exactly what happened:

| Exit code | Meaning | What to do |
|---|---|---|
| **0** | Snapshot built, signed, and published | Nothing — you are done with this step |
| **2** | Snapshot **held for review**: the number of addresses changed far more than expected since the last build | Normal safety behaviour, but it cannot happen on a first build. See [the safety check held my build](#the-safety-check-held-my-build) |
| **1** | The build failed | Read the last log line — it names the stage that failed. See [Troubleshooting](#step-10--troubleshooting) |

---

## Step 7 — Verify the snapshot

Confirm the snapshot really was recorded, signed, and published.

### Check the database

```sh
docker compose exec postgres psql -U vapn -d vapn -c \
  "select version, status, prefix_count_v4, prefix_count_v6, published_at,
          artifact_signature is not null as signed
   from routing.snapshot order by id desc limit 5;"
```

> This asks the database for the most recent snapshots.

**Healthy result:** one row with `status = published`, a recent `published_at`,
prefix counts in the thousands (the exact number depends on how many providers
you monitor), and `signed = t`.

### Check the published file

```sh
docker compose exec postgres psql -U vapn -d vapn -c \
  "select count(*) as probe_targets from routing.probe_target;"
```

**Healthy result:** a non-zero count. These are the addresses workers will
probe.

### Check it from an operator's point of view

`vapnctl` is the platform administration tool.

> ⚠️ **Install it on your own machine, not on the VPS.** The administration API
> is restricted to the addresses you put in `VAPN_ADMIN_ALLOW_CIDR` back in
> Step 4 — which was *your* IP, not the VPS's. Running `vapnctl` on the VPS
> would be refused with a `403`. (If you would rather administer from the VPS,
> see [the alternative](#administering-from-the-vps-instead) below.)

**On your local machine**, download the tool for your architecture:

```sh
# Linux
curl -fsSL -o vapnctl "https://github.com/HummingByteDev/vpsa-network-discovery/releases/latest/download/vapnctl-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
chmod +x vapnctl && sudo mv vapnctl /usr/local/bin/
```

> Releases ship `vapnctl-linux-amd64` and `vapnctl-linux-arm64`. The `sed`
> translates `uname -m` output into the name used by the release. On macOS,
> build it instead with `go build -o vapnctl ./cmd/vapnctl` from a clone.

Point it at your platform. Copy the admin token off the VPS:

```sh
export VAPN_COORDINATOR_URL="https://probes.example.com"
export VAPN_ADMIN_TOKEN="$(ssh your-user@probes.example.com \
  "grep '^VAPN_ADMIN_TOKEN=' /opt/vapn/deploy/prod/.env | cut -d= -f2")"
```

> Substitute your own domain and SSH login. Add both lines to your shell
> profile so they persist — the token is a secret, so treat that file
> accordingly.

```sh
vapnctl snapshots list
vapnctl status
```

**Healthy result:** `snapshots list` shows one snapshot with status
`published`, sensible prefix counts, and `ROLLBACK? yes`. `status` shows the
snapshot version, its target count, and `Scheduler: running`.

**If you get a connection error or a `403`:** your current IP is not the one in
`VAPN_ADMIN_ALLOW_CIDR`. Home IP addresses change. Check your current address
with `curl -s ifconfig.me`, then update the value on the VPS and reload the
edge:

```sh
ssh your-user@probes.example.com
cd /opt/vapn/deploy/prod
nano .env                       # set VAPN_ADMIN_ALLOW_CIDR to your current IP/32
docker compose up -d caddy
```

#### Administering from the VPS instead

If you would rather keep everything on one machine, allow the VPS's own public
address as well. `VAPN_ADMIN_ALLOW_CIDR` accepts several space-separated CIDRs:

```sh
# on the VPS
curl -s ifconfig.me                              # the VPS's public IP
nano .env                                        # e.g. VAPN_ADMIN_ALLOW_CIDR=203.0.113.7/32 198.51.100.9/32
docker compose up -d caddy
```

Then install `vapnctl` on the VPS with the same command as above. Note this
only works if your host's network routes its own public address back to itself;
if `vapnctl status` still fails, administer from your laptop instead.

---

## Step 8 — Run the builder automatically

You have proved a build works. Now let the machine do it on a schedule.

```sh
sudo cp systemd/vapn-builder.service systemd/vapn-builder.timer /etc/systemd/system/
sudo cp systemd/vapn-backup.service systemd/vapn-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vapn-builder.timer vapn-backup.timer
```

> These install two scheduled jobs: the builder and a nightly database backup.
> `enable --now` starts them immediately and makes them survive reboots.

**The builder runs three times a day — at 00:30, 08:30, and 16:30 UTC**, with
up to 10 minutes of random jitter. That matches the cadence at which fresh
routing data is published (every 8 hours), 30 minutes behind it.

The backup runs nightly at 03:15 UTC. See
[Backup & restore](../operations/backup-restore.md) to send those backups
offsite — a backup on the same disk as the database is not a backup.

### Verify

```sh
systemctl list-timers 'vapn-*'
```

**Healthy result:** both timers listed with a `NEXT` time in the future and
`ACTIVATES` naming the matching service.

After the next scheduled run, confirm it succeeded:

```sh
systemctl status vapn-builder.service
journalctl -u vapn-builder.service --since -1d
```

**Healthy result:** `Active: inactive (dead)` with
`Main PID: … (code=exited, status=0/SUCCESS)`. For a one-shot job, "inactive"
between runs is correct.

---

## Step 9 — Keep it up to date

The builder is a container image, so updating it is part of updating the
platform. Once its image is updated, the next scheduled run picks it up
automatically.

> ⚠️ **The builder must be updated explicitly.** It sits in the `build` compose
> profile because it is a scheduled job rather than a running service, and both
> `docker compose pull` and `docker compose up -d` skip profiled services
> entirely. Neither command will ever touch the builder image, so a builder left
> to those two commands stays on the version you first installed — silently,
> and possibly for months. Always name it.

```sh
cd /opt/vapn
git pull
cd deploy/prod
sed -i 's/^VAPN_VERSION=.*/VAPN_VERSION=v1.3.0/' .env    # the release you want
docker compose pull                  # the running services
docker compose pull builder          # the builder, which the line above skipped
docker compose up -d
```

> `git pull` picks up changes to the compose and configuration files, the
> `sed` line selects the release, and the two `pull` commands fetch it.
> Naming `builder` explicitly is what enables its profile. Database migrations
> run automatically before the services restart.

**Before the first release is tagged** there is nothing to pull, so leave
`VAPN_VERSION=latest` alone and rebuild from source instead:

```sh
cd /opt/vapn && git pull
cd deploy/prod
docker compose up -d --build         # the running services
docker compose build builder         # the builder, again separately
docker compose run --rm builder
```

> `docker compose run` does **not** rebuild an image that already exists, so
> without the `build builder` line your next build silently runs the old code.

### Verify

```sh
docker compose ps        # on the VPS: all services healthy on the new version
vapnctl status           # from your admin machine: the fleet is unaffected
```

> From here on, `docker compose` commands run **on the VPS** and `vapnctl`
> commands run **wherever you installed it** in Step 7 — normally your own
> machine.

### Rolling back

Two independent things can be rolled back, and it matters which one you mean.

**Roll back the software** — set the previous version and restart:

```sh
sed -i 's/^VAPN_VERSION=.*/VAPN_VERSION=v1.2.0/' .env
docker compose up -d
```

**Roll back the routing snapshot** — if a build published a bad address list:

```sh
vapnctl snapshots list
vapnctl snapshots rollback 20260808T0800Z-1723118400000
```

> This re-publishes an earlier snapshot. Workers switch to it on their next
> heartbeat, within about 30 seconds. The action is recorded in the audit log.

By default the five most recent superseded snapshots stay available for
rollback; older ones are pruned and `snapshots list` marks them `pruned`.

Full procedures: [Releases & upgrades](../operations/releases-and-upgrades.md).

---

## Step 10 — Troubleshooting

Every failure below leaves your **previously published snapshot fully in
force**. Workers keep probing the addresses they already have, indefinitely.
A failed build is never an outage — take your time.

> **Where the builder's logs are.** The builder is a one-shot job, not a
> running service, so `docker compose logs builder` shows **nothing** — the
> container is gone by the time you ask. Read its output from whichever way it
> ran:
>
> ```sh
> # A build you started by hand: keep a copy as it runs.
> docker compose run --rm builder 2>&1 | tee /tmp/vapn-build.log
>
> # A build the timer started: it goes to the journal.
> journalctl -u vapn-builder.service --since -1d --no-pager
> ```
>
> The `grep`s below assume you have one of those two; `BUILD_LOG` in the
> examples means either `/tmp/vapn-build.log` or the `journalctl` output.
> Long-running services (`coordinator`, `aggregator`, `postgres`) do work with
> `docker compose logs`.

### Docker isn't available

**What it usually means:** the daemon isn't running, or your user can't reach
it.

```sh
docker info
```

**Healthy result:** a block of system information. `permission denied` means
you need `sudo usermod -aG docker "$USER"` and a fresh login. `Cannot connect
to the Docker daemon` means `sudo systemctl start docker`.

### The repository won't clone

**What it usually means:** `git` isn't installed, or the machine has no
outbound HTTPS.

```sh
git --version
curl -fsSI https://github.com | head -1
```

**Healthy result:** a git version and `HTTP/2 200`. Install git with
`sudo apt install -y git`; a failing `curl` means your firewall is blocking
outbound HTTPS, which the platform needs.

### Configuration validation fails

**What it usually means:** a required setting is empty or malformed. Services
refuse to start rather than run half-configured, and they list **every**
problem at once instead of stopping at the first.

```sh
grep -A2 'bad configuration' "$BUILD_LOG"
```

**Healthy result:** no output. Otherwise you will see
`configuration errors: VAPN_ADVISOR_TOKEN, VAPN_SANITY_MAX_DELTA_PCT (not an
integer: "fifty")` — fix each named variable in `.env` and re-run.

### A service says `(unhealthy)`

**What it usually means:** the service itself is fine and serving traffic — the
health *probe* is failing. Check the service's own logs before assuming it is
broken:

```sh
docker compose logs coordinator | tail -20
docker inspect --format '{{range .State.Health.Log}}{{.ExitCode}} {{.Output}}{{end}}' vapn-coordinator-1 | tail -3
```

**Healthy result:** the logs end with `http server listening` and the probe
exit code is `0`.

An exit code of `127` with `stat /coordinator: no such file or directory` means
you are running a compose file from before 2026-08-09, which pointed the probe
at the wrong path — every component's binary is installed at `/app`. Update and
recreate:

```sh
cd /opt/vapn && git pull
cd deploy/prod && docker compose up -d
```

> Nothing was actually wrong with the service, so nothing was lost. Only the
> reported status changes.

`provider sync failed` warnings in the log do **not** make a service unhealthy
— see [below](#the-builder-cannot-reach-vps-advisor-at-all).

### The database connection fails

**What it usually means:** PostgreSQL isn't up yet, or `VAPN_DB_PASSWORD`
changed without the database volume being recreated.

```sh
docker compose ps postgres
docker compose exec postgres pg_isready -U vapn -d vapn
```

**Healthy result:** the service is `healthy` and `pg_isready` prints
`accepting connections`. The builder waits up to 60 seconds for the database at
startup, so a slow start is handled for you.

### VPS Advisor authentication fails

**What it usually means:** a wrong token, or a `VAPN_ADVISOR_URL` with an extra
path on the end.

```sh
grep -i advisor "$BUILD_LOG"
```

**Healthy result:** `provider sync complete asns=N` with N greater than zero.
Every error names the URL it called, so compare that against the address the
site actually answers on:

- `401` — the token is wrong or expired; ask the website team for a fresh one.
- `404` — `VAPN_ADVISOR_URL` includes a path. It must be the bare site address,
  because the platform appends `/api/v1/monitoring/providers` itself.
- `… redirects to "https://…"` — the configured host redirects (typically
  `www.` to the apex, or the reverse). The platform refuses to follow it,
  because a cross-host redirect strips the `Authorization` header and would
  turn every call into an anonymous 401. Set `VAPN_ADVISOR_URL` to the address
  in the message.

**Nothing appearing in your artifact bucket is usually this same fault.** The
build begins with the provider sync and aborts there, so it never reaches the
upload — an empty bucket and a silent fleet are one problem, not two.

`ASN … claimed by two providers` is a data problem on the VPS Advisor side, not
yours. The builder refuses to guess which provider owns an address range;
report it to the website team.

### The builder cannot reach VPS Advisor at all

**What it usually means:** the website integration isn't live yet, so there is
no token to issue and the endpoint 404s.

**This is a hard stop, by design.** Provider sync is the *first* stage of the
build ([how the builder works](README.md)), and a failure there aborts the run
with exit code 1 before anything is downloaded or written:

```
provider sync: advisor GET https://www.vpsadvisor.example/api/v1/monitoring/providers?enabled=true: 404 Not Found
```

The builder will not fall back to a guessed provider list — the ASN-to-provider
mapping is what defines which networks the fleet is permitted to probe, and
probing networks nobody has consented to is exactly what the platform must
never do.

Until the website team issues a real token, you can still exercise the whole
pipeline against the fixture-backed stub that ships with the project. It serves
five real provider ASNs, so the build is genuine — only the provider list is
mocked:

```sh
cd /opt/vapn
docker build --build-arg COMPONENT=mockadvisor -t vapn-mockadvisor .
docker run -d --name mockadvisor --network vapn_default --restart unless-stopped \
  vapn-mockadvisor
```

> This builds the stub and attaches it to the same network as the rest of the
> stack, where the other services can reach it by the name `mockadvisor`.

Point the stack at it and run a build:

```sh
cd /opt/vapn/deploy/prod
sed -i 's|^VAPN_ADVISOR_URL=.*|VAPN_ADVISOR_URL=http://mockadvisor:8081|' .env
sed -i 's|^VAPN_ADVISOR_TOKEN=.*|VAPN_ADVISOR_TOKEN=dev-advisor-token|' .env
docker compose up -d
docker compose run --rm builder
```

**Healthy result:** `provider sync complete asns=5`, then the build proceeds
through extraction and publication exactly as it will in production.

> ⚠️ **Remember to undo this before going live.** Snapshots built against the
> stub describe five example providers, not your real ones. Restore the real
> `VAPN_ADVISOR_URL` and token, `docker rm -f mockadvisor`, and rebuild before
> enrolling any community workers.

### The routing download fails

**What it usually means:** the RIPE collector is temporarily unavailable, or
outbound HTTPS is blocked.

```sh
curl -fsSI https://data.ris.ripe.net/rrc00/latest-bview.gz | head -1
df -h /
```

**Healthy result:** `HTTP/2 200` and at least 10 GB free disk. If the collector
is down, point at a different one — they are interchangeable:

```sh
sed -i 's|^VAPN_RIS_BVIEW_URL=.*|VAPN_RIS_BVIEW_URL=https://data.ris.ripe.net/rrc01/latest-bview.gz|' .env
docker compose run --rm builder
```

A partial download never corrupts anything: the file is written to a temporary
name and only moved into place once complete.

If the error instead reads `open /work/.bview-…: permission denied`, the
builder cannot write to its own work volume. This affected images built before
2026-08-09; rebuild and discard the empty volume:

```sh
docker compose down
docker volume rm vapn_builder_work
docker compose build builder
docker compose run --rm builder
```

> The volume only holds the downloadable bview cache, so removing it loses
> nothing — the next run re-downloads it.

### The location database is missing

**What it usually means:** the MaxMind updater hasn't run, or your credentials
are wrong. The build fails at `geo enrichment`.

```sh
ls -lh geoip/
docker compose logs geoipupdate | tail -20
```

**Healthy result:** `GeoLite2-City.mmdb` and `GeoLite2-ASN.mmdb` exist and are
tens of megabytes. If the updater reports an authentication error, re-check
`MAXMIND_ACCOUNT_ID` and `MAXMIND_LICENSE_KEY`, then
`docker compose --profile geoip up -d --force-recreate`.

### The safety check held my build

**What it usually means:** the new snapshot's address count differs from the
last published one by more than 50%. The builder refuses to publish it and
exits with code **2**, leaving the previous snapshot in force. This is the
protection against a routing leak or a truncated download quietly poisoning
your fleet's target list.

```sh
vapnctl snapshots list
```

**Healthy result:** the previous snapshot is still `published`; the new one
sits at `building`.

**What to do next:** work out whether the swing is *real*. A large batch of
providers being onboarded or removed genuinely changes the count. A RIPE outage
or an advisor glitch does not. Once you are satisfied it is legitimate, run one
build with the check bypassed:

```sh
VAPN_SANITY_FORCE=true docker compose run --rm builder
```

Do not put `VAPN_SANITY_FORCE=true` in `.env` — that disables the protection
permanently.

### The storage bucket is empty

**What it usually means:** almost never the storage settings. A build uploads
only at the very end, so anything that stops it earlier leaves the bucket
untouched — and the builder is a one-shot, so if it never ran there is nothing
to find. Work through the stages in order; each one logs a line, and the
**last line you see tells you where it stopped**:

```sh
docker compose run --rm builder 2>&1 | tee /tmp/vapn-build.log
grep -E 'artifact publication enabled|provider sync complete|extraction complete|snapshot loaded|artifact published|snapshot published|level.:.(ERROR|WARN)' /tmp/vapn-build.log
```

| Last line you see | Where it stopped | What to do |
|---|---|---|
| *(no output at all)* | The build never ran | The builder is behind a Compose profile, so `docker compose up -d` does **not** start it. Run it as above, then [enable the timer](#step-8--run-the-builder-automatically) |
| `bad configuration` | Startup | [Configuration validation fails](#configuration-validation-fails) |
| *no* `artifact publication enabled` | Startup | No store configured, or `VAPN_SNAPSHOT_SIGNING_KEY` unusable. The build still runs, and still uploads nothing — check `VAPN_ARTIFACT_S3_ENDPOINT` is set for the **builder** |
| `provider sync` error | Stage 1 | [VPS Advisor authentication fails](#vps-advisor-authentication-fails). This is the most common cause of an empty bucket |
| `mrt extraction` error | Stage 2 | [The routing download fails](#the-routing-download-fails) |
| `geo enrichment` error | Stage 3 | [The location database is missing](#the-location-database-is-missing) |
| `snapshot held for review` | Sanity gate | [The safety check held my build](#the-safety-check-held-my-build) — deliberate, nothing is published |
| `upload artifact` / `readback` error | Upload | [Publishing to storage fails](#publishing-to-storage-fails) |
| `snapshot published` | Finished | It worked; the bucket has `snapshots/…` and `current.json` |

**Healthy full run** ends with `artifact published …` followed by
`snapshot published version=… elapsed=…`.

### Publishing to storage fails

**What it usually means:** wrong endpoint, wrong credentials, or a bucket the
keys can't write to. The builder verifies that it can read back exactly what it
uploaded before marking a snapshot published, so a half-uploaded snapshot never
becomes live.

```sh
grep -iE 'artifact|upload|readback' "$BUILD_LOG"
```

**Healthy result:** `artifact published version=… sha256=… targets=…`. Errors
naming `upload artifact` or `readback verification` point at
`VAPN_ARTIFACT_S3_*` — check the endpoint has no `https://` prefix, that the
bucket exists, and that the key pair has write access to it.

### Workers reject the snapshot's signature

**What it usually means:** the public key a worker was given doesn't match the
private key this builder signs with.

On the builder, the public key is printed at the start of every run:

```sh
grep 'artifact publication enabled' "$BUILD_LOG"
```

**Healthy result:** `public_key=hJJgj1Wx9sQ…`. Compare it to the value the
worker operator entered — `vapn install` prompts for it and stores it in
`~/.vapn/config.env` as `VAPN_SNAPSHOT_PUBLIC_KEY`. If they differ, the worker
has the wrong key: give them the correct one and have them re-run
`vapn install`.

### The scheduled build isn't running

**What it usually means:** the timer wasn't enabled, or the units were copied
from a checkout that isn't at `/opt/vapn`.

```sh
systemctl list-timers 'vapn-*'
systemctl status vapn-builder.timer
```

**Healthy result:** the timer is `active (waiting)` with a future `NEXT` time.
If it is missing, repeat [Step 8](#step-8--run-the-builder-automatically). If
the service fails instantly with a path error, check that
`WorkingDirectory=/opt/vapn/deploy/prod` in
`/etc/systemd/system/vapn-builder.service` matches where you actually cloned
the project.

### Nothing above matches

Read the last error line in the build log — it names the stage that failed:

```sh
docker compose logs --tail 50 builder
```

Then see the platform [runbooks](../operations/runbooks.md#snapshot-build-failure),
which cover the same failures from an on-call perspective.

---

## What happens from here

- **The builder runs three times a day** and publishes a fresh snapshot each
  time. Provider address ranges change slowly, so a missed build costs you
  freshness, not availability.
- **Workers pick it up automatically.** Each worker is told the current version
  on its heartbeat, downloads the file, verifies the signature and checksum
  against the public key you gave them, and swaps it in. They never accept an
  older version than the one they hold.
- **You monitor one number.** Snapshot age. If the newest published snapshot is
  more than ~18 hours old, builds are failing or the timer isn't firing. The
  optional monitoring stack alerts on exactly that — see
  [Monitoring](../operations/monitoring.md).
- **Nothing about this is urgent.** Every failure mode leaves the last good
  snapshot in force.

### Testing without VPS Advisor

If the website integration isn't ready, point your production stack at the
fixture-backed stub — see
[the builder cannot reach VPS Advisor at all](#the-builder-cannot-reach-vps-advisor-at-all)
above for the procedure. That keeps the real stack, the real RIS data, and the
real publication path; only the provider list is mocked.

There is also a full offline development stack (`make dev-up`), but it expects
a pre-populated `data/` directory of routing and location files that is **not**
part of the repository, so it is not a shortcut on a fresh server. Details:
[Development](../development/README.md).

### Where to go next

| If you want to… | Read |
|---|---|
| Understand what the builder actually does | [How the builder works](README.md) |
| Enrol your first community workers | [Deployment → First workers](../operations/deployment.md#first-workers) |
| See every setting, default, and accepted value | [Configuration reference](../reference/configuration.md) |
| Set up alerting and dashboards | [Monitoring](../operations/monitoring.md) |
| Handle an incident | [Runbooks](../operations/runbooks.md) |
| Protect the host and the secrets | [Security](../operations/security.md) |

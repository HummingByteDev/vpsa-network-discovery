# Quick Start — Run a Worker

Goal: a healthy VAPN worker, probing from your machine, in about five minutes.
This page is the happy path. For the reasoning behind each step, see
[Installation](installation.md); if something goes wrong, see
[Troubleshooting](troubleshooting.md).

## Before you begin

You need three things:

1. A **Linux** machine (amd64/arm64) you can run a long-lived container on.
2. **Docker** installed and running — verify with `docker info`.
3. An **enrollment token** from VPS Advisor: log in → **My Workers** →
   **Create worker** → copy the token it shows you *once*.

> **What's an enrollment token?** A one-time password that proves *you* (a
> real, logged-in operator) started this worker. The worker trades it for a
> permanent cryptographic identity on first boot, then it's useless. It is
> shown only once — if you lose it, regenerate it from the same page.

## Install

The installer lives in the project's GitHub releases. Run:

```sh
curl -fsSL https://raw.githubusercontent.com/HummingByteDev/vpsa-network-discovery/main/deploy/worker/install.sh | bash
```

> **Why the long GitHub URL?** GitHub is the canonical, auditable source for
> the installer — no vanity redirect to trust. Prefer to read before you run?
> That's encouraged — see
> [Installation → Install from GitHub](installation.md#choose-how-you-install),
> which covers downloading and inspecting the script (or cloning the repo)
> first.

The installer:

1. Confirms Docker is present and the daemon is reachable.
2. Downloads the small `vapn` command-line tool for your architecture.
3. Hands over to `vapn install`, which asks for your **coordinator URL** and
   **enrollment token**, runs system checks, starts the worker, and verifies
   it comes up:

```
✓ Docker detected          ✓ Coordinator reachable
✓ Disk space               ✓ Clock synchronization
✓ Registration successful
Worker ID: 9f30…
Status: Awaiting approval
```

## What "Awaiting approval" means

Your worker registered successfully and is now **pending**. A human
administrator reviews new workers before they contribute to public results —
this is a deliberate anti-abuse step (see [Trust model](../concepts/measurement-and-consensus.md#trust)).
You don't have to do anything: once approved, the worker starts probing on its
own. Approval status is visible on your VPS Advisor dashboard.

## Check on it

```sh
vapn status
```

You'll see health, the routing snapshot version it downloaded, how many
assignments it holds, and how many measurements it has submitted. A few useful
commands day-to-day:

```sh
vapn logs -f     # live logs
vapn pause       # stop probing (identity kept; resume any time)
vapn resume
vapn doctor      # re-run the system checks if something looks off
```

The full list is the [Worker command reference](../worker/command-reference.md).

## You're done

That's it — the worker is self-managing. It renews its own credentials,
downloads new routing snapshots (verifying each one cryptographically), retries
failed uploads, and recovers from reboots on its own. You should rarely need to
touch it.

### Next steps

- Understand exactly [what the worker does and what it costs you](../worker/resources-and-privacy.md).
- Turn on [automatic updates](updating.md).
- Curious how your packets become a public verdict? Follow the
  [end-to-end walkthrough](../walkthroughs/end-to-end.md).

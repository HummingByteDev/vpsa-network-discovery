# Running a VAPN Worker

Thank you for contributing measurement capacity to the VPS Advisor Probe
Network. A worker is a small Docker container that pings VPS providers'
public networks from your vantage point and reports signed measurements.
It uses a few MB of RAM, negligible CPU, and a trickle of bandwidth.

**You stay in control**: pause, resume, or remove the worker at any time.
Providers being measured can opt out at any time too — the target list your
worker uses comes exclusively from providers listed on VPS Advisor.

## Install

Requirements: Linux (amd64/arm64), Docker, and an **enrollment token** from
your VPS Advisor dashboard (My Workers → Create worker).

```sh
curl -fsSL https://install.vpsadvisor.com | bash
```

The installer detects Docker, downloads the `vapn` CLI, runs the system
checks, asks for your token, starts the worker, and verifies it comes up:

```
✓ Docker detected          ✓ Coordinator reachable
✓ Disk space               ✓ Clock synchronization
✓ Registration successful
Worker ID: 9f30…
Status: Awaiting approval
```

Approval is manual (an admin reviews new workers); your worker begins
probing automatically once approved — nothing more to do.

## Day to day

```sh
vapn status        # health, snapshot, assignments, measurements submitted
vapn logs -f       # live logs
vapn pause         # stop probing (your assignments shift to other workers)
vapn resume
vapn update        # pull latest image; health-gated with automatic rollback
vapn doctor        # re-run the system checks
vapn backup        # archive identity + config (contains your private key!)
```

Updates are automatic if you enable the timer (the installer offers it, or
see `deploy/worker/vapn-update.*`): daily check, randomized hour, and if an
updated worker fails to report healthy within two minutes it is rolled back
to the previous image automatically.

Everything lives in `~/.vapn`; the worker is self-managing — it renews
credentials, downloads new routing snapshots (cryptographically verified
against a pinned key), retries failed uploads, and recovers from reboots
(`restart: unless-stopped`) and network interruptions on its own. You
should rarely need to SSH in.

## Leaving

```sh
vapn uninstall
```

Offers to unregister your worker with the network (recommended) and to keep
a backup, then removes containers, images, and `~/.vapn`. No orphaned
resources, no hard feelings — thanks for the packets.

## Privacy & conduct

- The worker only probes IP addresses from the signed routing snapshot —
  never targets of its own choosing; ICMP echo only, a few packets per
  minute per target.
- It reports measurements, its version, and liveness. It does not read
  anything else on your machine.
- Your worker's key never leaves your machine; the platform can verify your
  measurements but cannot impersonate you.
- Misbehaving workers (bad clocks, tampered binaries) lose trust weight and
  are quarantined server-side — if that happens to you, `vapn doctor` and
  `vapn logs` usually explain why.

## Troubleshooting

| Symptom | Try |
|---|---|
| `Awaiting approval` for long | Approval is human; check your VPS Advisor dashboard |
| `Unreachable (last report …)` in status | `vapn logs`, then `vapn doctor` |
| Clock check fails | Enable NTP (`timedatectl set-ntp true`) |
| Docker permission errors | Add your user to the `docker` group, re-login |
| Behind strict egress firewall | Allow HTTPS (443) to the coordinator domain |

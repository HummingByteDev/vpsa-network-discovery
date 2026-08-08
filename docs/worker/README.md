# Community Workers

A **worker** is a small program you run that measures VPS providers' public
network health from your location and reports signed results back to the
network. Thousands of workers, run by the community from many places, are what
make VAPN's verdicts trustworthy — no single one is trusted on its own.

Thank you for considering running one. This section is everything you need,
from "what is it" to "how do I leave."

> **Just want it running?** → [Install a worker](installation.md) — about five
> minutes.

## What a worker is, in one paragraph

It's a Docker container that, once approved, receives **assignments** from the
platform (never chooses its own targets), sends a few lightweight
[ICMP "ping"](../concepts/measurement-and-consensus.md#what-a-single-measurement-is)
packets to the assigned provider addresses, times the replies, cryptographically
signs the results with a key that never leaves your machine, and uploads them.
It uses a few MB of RAM, negligible CPU, and a trickle of bandwidth. **You stay
in control**: pause, resume, or remove it whenever you like.

## This section

| Guide | What it covers |
|---|---|
| [Installation](installation.md) | Requirements, install, verify — plus what actually happens under the hood |
| [Operating a worker](operations.md) | Every `vapn` command, updating, pausing, backups, leaving |
| [Worker lifecycle](lifecycle.md) | Every state a worker moves through and why |
| [Resource usage & privacy](resources-and-privacy.md) | Exactly what it costs you and what it does (and doesn't) see |
| [Troubleshooting & FAQ](troubleshooting.md) | When something's off, and the questions everyone asks |

## Why run one?

- **Help buyers make better decisions.** Your vantage point adds a real,
  independent data point about provider health from your part of the world.
- **It's nearly free to run.** A spare VPS or home server has plenty of
  headroom; see [resource usage](resources-and-privacy.md#resource-usage).
- **It's safe and private by design.** Your worker only probes addresses from a
  signed, platform-provided list, reports nothing about your machine beyond its
  own liveness and version, and your private key never leaves your host. See
  [privacy](resources-and-privacy.md#privacy).

## The short version of day-to-day

```sh
vapn status        # health, snapshot, assignments, measurements submitted
vapn logs -f       # live logs
vapn pause         # stop probing (identity + trust kept; resume any time)
vapn resume
vapn update        # health-gated update with automatic rollback
vapn doctor        # re-run the system checks
vapn uninstall     # remove everything (offers a clean unregister)
```

Full details: [Operating a worker](operations.md).

## How your worker earns its place

New workers are **manually approved** (anti-abuse) and then build **trust** over
time by agreeing with the consensus of other workers. Trust determines how much
your measurements count. Misbehaving workers (bad clocks, tampered binaries)
lose trust and are quarantined automatically. None of this needs your attention
in the normal case — but if you're curious how it works, read
[the trust concept](../concepts/measurement-and-consensus.md#trust) and the
[lifecycle](lifecycle.md).

Everything lives in `~/.vapn`; the worker is self-managing — it renews
credentials, downloads and verifies new routing snapshots, retries failed
uploads, and recovers from reboots on its own. You should rarely need to SSH in.
Thanks for the packets.

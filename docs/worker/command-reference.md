# Worker Command Reference

Every `vapn` command, what it does, and when to use it. `vapn` is the small CLI
the installer places on your host; it wraps Docker so you never have to think
about containers directly.

Run `vapn` with no arguments (or `vapn help`) to print this list. The worker's
home directory is `~/.vapn` (override with `VAPN_HOME`).

## Quick index

| Command | One-liner |
|---|---|
| [`install`](#install) | Set up and start a worker (interactive) |
| [`status`](#status) | Worker health at a glance |
| [`doctor`](#doctor) | Run the system checks |
| [`logs`](#logs) | Show worker logs (`-f` to follow) |
| [`pause`](#pause) | Stop probing, keep identity |
| [`resume`](#resume) | Start probing again |
| [`update`](#update) | Update the worker image (health-gated, auto-rollback) |
| [`backup`](#backup) | Archive identity + config |
| [`unregister`](#unregister) | Permanently retire this worker with the network |
| [`uninstall`](#uninstall) | Remove everything |
| [`version`](#version) | Print the CLI version |

---

## `install`

```sh
vapn install
```

Interactive setup: runs the [system checks](#doctor), prompts for the
**coordinator URL** and **enrollment token**, writes `~/.vapn/config.env`,
generates the worker's keypair, registers with the coordinator, downloads and
verifies the routing snapshot, starts the container, and verifies it comes up.
Idempotent — safe to re-run to reconfigure. Usually invoked for you by
`install.sh`. → [Installation](installation.md).

## `status`

```sh
vapn status
```

A snapshot of worker health: running/paused state, current routing snapshot
version, number of held assignments, measurements submitted, last heartbeat, and
any pending control actions (e.g. `upgrade_required`). Your first stop when
checking in. → interpreting it: [lifecycle](lifecycle.md).

## `doctor`

```sh
vapn doctor
```

Re-runs the environment checks: Docker present and reachable, disk space,
coordinator reachable over HTTPS, clock synchronized. Each failure prints a
specific remedy. Run it whenever something looks off. →
[Troubleshooting](../getting-started/troubleshooting.md).

## `logs`

```sh
vapn logs          # recent logs
vapn logs -f       # follow live
```

Streams the worker container's logs. Safe to share when reporting a problem —
**the private key never appears in logs**. Look here for the last error line
when diagnosing.

## `pause`

```sh
vapn pause
```

Stops probing but **keeps your identity and accumulated trust**. Your
assignments redistribute to other workers within about a minute. Use this for
maintenance windows or when you need your bandwidth. Prefer this over uninstall
if you'll be back — resuming keeps your trust; reinstalling starts trust over.

## `resume`

```sh
vapn resume
```

Resumes probing after a `pause`. The worker re-leases assignments on its next
cycle.

## `update`

```sh
vapn update           # manual, health-gated update
vapn update --auto    # what the systemd timer runs (unattended)
```

Pulls the latest worker image, restarts, and waits for health. **If the new
version isn't healthy within two minutes, it automatically rolls back** to the
previous image — you can't break a working worker by updating. See
[Updating](../getting-started/updating.md) and the
[software-updates walkthrough](../walkthroughs/software-updates.md).

## `backup`

```sh
vapn backup
```

Archives your identity and configuration to a `tar.gz`. **The archive contains
your private key — treat it like a password.** Use it to move a worker to
another host or to restore the same identity (and its trust) instead of
enrolling fresh.

## `unregister`

```sh
vapn unregister
```

Tells the coordinator this worker is retiring: it's marked `retired`, keys
revoked, no more assignments — history retained for audit. The polite, immediate
way to leave. `uninstall` offers to do this for you.

## `uninstall`

```sh
vapn uninstall
```

Removes everything: containers, the worker image, and `~/.vapn`. Interactively
offers to [`unregister`](#unregister) first (recommended) and to keep a
[`backup`](#backup). → [Uninstalling](../getting-started/uninstalling.md).

## `version`

```sh
vapn version
```

Prints the `vapn` CLI version. (To see the running *worker image* version, use
[`status`](#status).)

---

## Common recipes

```sh
# First install, then confirm it's healthy and awaiting approval
vapn install && vapn status

# Something's wrong — diagnose
vapn doctor ; vapn logs -f

# Going away for a bit (keep identity/trust)
vapn pause          # …later… vapn resume

# Move this worker to a new machine
vapn backup         # copy the tar.gz over, restore into ~/.vapn there

# Leave for good, cleanly
vapn uninstall      # accept the unregister prompt
```

Related: [lifecycle](lifecycle.md) ·
[resource usage & privacy](resources-and-privacy.md) ·
[FAQ](../getting-started/faq.md).

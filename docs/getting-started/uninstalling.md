# Uninstalling a Worker

You can leave the network at any time, and leaving is clean — no orphaned
containers, images, or files. There is no lock-in and no penalty.

## The one command

```sh
vapn uninstall
```

It walks you through two choices, then removes everything:

1. **Unregister with the network? (recommended)** This tells the coordinator
   your worker is retiring so it stops assigning you work and marks your worker
   `retired` (keys revoked, history retained for audit). Declining just stops
   the local container; the platform will notice the silence and flag the
   worker `unreachable` on its own, but unregistering is the polite, immediate
   way out.
2. **Keep a backup?** Offers to archive your identity and config first (see
   below) in case you want to come back.

Then it removes the containers, the worker image, and the entire `~/.vapn`
directory.

## Just pausing, not leaving

If you only want to stop probing for a while — a maintenance window, a busy
period on your link — don't uninstall. Pause instead:

```sh
vapn pause     # stops probing; keeps your identity and trust
vapn resume    # back to work
```

While paused, your assignments redistribute to other workers within a minute,
and your accumulated [trust](../concepts/measurement-and-consensus.md#trust) is
preserved. Uninstalling and re-installing, by contrast, creates a *new* worker
identity that starts trust-building from scratch.

## Backups (and what's in them)

```sh
vapn backup
```

This archives your identity and configuration to a `tar.gz`. **It contains your
private key** — treat it like a password. A backup lets you restore the *same*
worker identity (and its trust history) on the same or another machine instead
of enrolling fresh.

## Manual cleanup (if `vapn` is already gone)

If you deleted the CLI before uninstalling, remove the pieces by hand:

```sh
docker compose -f ~/.vapn/docker-compose.yml down --rmi all   # stop + remove image
rm -rf ~/.vapn                                                 # remove state
sudo rm -f /usr/local/bin/vapn                                 # remove the CLI
```

Your worker will then go silent and the platform will flag it `unreachable`;
an admin can retire it. To retire it immediately and cleanly, prefer
`vapn unregister` *before* manual cleanup.

Thanks for the packets.

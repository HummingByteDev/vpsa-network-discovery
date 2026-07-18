# Updating a Worker

Workers are versioned container images. Keeping current matters: the platform
can require a minimum worker version (old workers get drained until they
upgrade), and updates carry fixes and new probe types. Updating is safe by
design — it is **health-gated with automatic rollback**.

## Update on demand

```sh
vapn update
```

This pulls the latest worker image, restarts the container, and waits for it to
report healthy. **If the new version fails to become healthy within two
minutes, it is automatically rolled back to the previous image.** You cannot
break a working worker by updating it.

## Automatic updates (recommended)

The installer offers to enable a systemd timer that checks for updates daily at
a randomized hour (so the whole fleet doesn't update in lockstep). To enable it
later, install the units shipped in the repository:

```
deploy/worker/vapn-update.service
deploy/worker/vapn-update.timer
```

```sh
sudo cp deploy/worker/vapn-update.* /etc/systemd/system/
sudo systemctl enable --now vapn-update.timer
```

The timer runs `vapn update --auto`, which is the same health-gated,
auto-rollback update — just unattended.

> **Prefer to control timing yourself?** Skip the timer and run `vapn update`
> from your own cron or configuration-management tooling. The command is
> idempotent and safe to run when already up to date.

## How the platform signals "please upgrade"

Every heartbeat reports your worker's version. If your version is below the
platform's **minimum supported version**, the coordinator responds with an
`upgrade_required` control action: the worker is *drained* (its assignments
move to other workers) and refused new leases until it upgrades. This is how
old, potentially buggy workers are retired gracefully without cutting anyone
off abruptly. You'll see it in `vapn status` and `vapn logs`.

## Verifying an update

```sh
vapn version    # CLI version
vapn status     # shows running worker image/version + health
vapn logs -f    # watch it come back up
```

If an update ever leaves you in a bad state (it shouldn't, thanks to
auto-rollback), see [Troubleshooting](troubleshooting.md).

## Updating the CLI itself

`vapn update` updates the *worker image*. To update the `vapn` CLI binary,
re-run the installer (it overwrites the binary in place) or `git pull &&
make build` if you installed from source.

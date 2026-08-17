# CLI Reference

Two command-line tools ship with VAPN:

- **`vapn`** — the **worker operator** CLI (community-facing). Wraps Docker so
  operators never touch containers directly. Full narrative:
  [operating a worker](../worker/operations.md).
- **`vapnctl`** — the **platform administration** CLI. Drives the
  [platform admin API](../api/README.md#c-platform-admin-api).

Other binaries in `cmd/` are services or tools, not interactive CLIs:

| Binary | What it is |
|---|---|
| `coordinator` · `aggregator` | Long-running services |
| `builder` | One-shot snapshot build ([guide](../builder/installation.md)) |
| `worker` | The agent inside the worker container (`vapn` drives it) |
| `migrate` | Applies the SQL migrations, then exits |
| `keygen` | Prints a snapshot signing keypair ([how to run it](configuration.md#snapshot-signing-keys)) |
| `loadtest` | Synthetic fleet load generator ([usage](../operations/monitoring.md#load-testing)) |
| `mockadvisor` | VPS Advisor stub for dev/CI |

How each is configured: [configuration](configuration.md).

---

## `vapn` (worker operator)

```
vapn <command>
```

| Command | Description |
|---|---|
| `install` | Set up and start a worker (interactive) |
| `reconfigure` | Change settings on an installed worker, keeping its identity |
| `status` | Worker health at a glance |
| `doctor` | Run the system checks |
| `logs [-f]` | Worker logs (`-f` to follow) |
| `pause` | Stop probing (keeps identity; resume any time) |
| `resume` | Start probing again |
| `update [--auto]` | Update to the latest worker image (health-gated, auto-rollback) |
| `backup` | Archive worker identity/config to a `tar.gz` |
| `unregister` | Permanently retire this worker with the coordinator |
| `uninstall` | Remove everything (offers unregister + backup) |
| `version` | Print the CLI version |

Home directory: `~/.vapn` (override with `VAPN_HOME`). Per-command detail and
recipes: [operating a worker](../worker/operations.md).

### `vapn reconfigure`

Walks the same questions as `install` — coordinator URL, worker name, snapshot
public key, worker image — with the current value offered as the default.
**Press Enter to keep a value.** Then it re-runs the system checks, rewrites
`~/.vapn/config.env` and the generated compose file, recreates the container,
and waits for the worker to report healthy.

- **Identity is preserved.** The worker's private key, worker ID and trust
  history are never touched; a worker that has already registered is not asked
  for an enrollment token again (the token is one-time, and re-enrolling would
  start its trust history from zero).
- **Secrets are shown masked** (`TbP5t********la/rw=`) so you can tell which
  value is stored without it being printed in full, and kept on an empty answer
  so you never have to re-enter one to change something else.
- **Safe to run repeatedly.** The config file is rewritten from scratch in a
  fixed order — no appended duplicates, no regenerated credentials, no change
  to persistent state. Settings you added to `config.env` by hand are carried
  across.
- **Nothing is written if the checks fail**, so a failed reconfiguration leaves
  the running worker exactly as it was.

There is no `reinstall` command: `reconfigure` covers configuration and
container recreation, [`update`](../worker/operations.md) covers the image
(health-gated, with rollback), and `uninstall` covers removal. A separate
"reinstall" would only risk the one thing that cannot be recreated — the
worker's identity.

---

## `vapnctl` (platform administration)

```
vapnctl [--url URL] [--token TOKEN] <command> [args]
```

Reads `VAPN_COORDINATOR_URL` and `VAPN_ADMIN_TOKEN` from the environment if the
flags are omitted. The token is the platform [admin token](../api/README.md#c-platform-admin-api);
keep this CLI's access network-restricted.

| Command | Description |
|---|---|
| `status` | Fleet overview, including per-feed VPS Advisor sync health |
| `workers list` | List workers |
| `workers show <id>` | Worker detail (state, trust, leases, events) |
| `workers create --name N` | Create a worker; prints a one-time enrollment token |
| `workers approve <id> --reason R` | Transition to `active` |
| `workers suspend <id> --reason R` | Transition to `suspended` |
| `workers quarantine <id> --reason R` | Transition to `quarantined` (shadow mode) |
| `workers retire <id> --reason R` | Transition to `retired` (terminal) |
| `workers rotate-key <id>` | Demand a key rotation at the worker's next heartbeat |
| `snapshots list` | List routing snapshots (version, status, counts) |
| `snapshots rollback <version>` | Re-publish a previous snapshot |
| `scheduler pause` | Global assignment kill switch — stop issuing work |
| `scheduler resume` | Resume issuing assignments |
| `audit [--category C] [--since RFC3339] [--limit N]` | Query the append-only audit log |

### Common admin recipes

```sh
# Enroll a worker before the website enrollment UI exists (rollout step 3)
vapnctl workers create --name helsinki-1        # prints one-time token

# Approve a pending worker
vapnctl workers approve 9f30… --reason "verified operator"

# Investigate a worker
vapnctl workers show 9f30…                       # state, trust, leases, events

# A worker approved on the website is still reporting "awaiting approval"
vapnctl status                                    # read the "Advisor decisions" line

# Emergency: stop all probing fleet-wide
vapnctl scheduler pause                           # …later… vapnctl scheduler resume

# Roll back a bad snapshot
vapnctl snapshots list
vapnctl snapshots rollback 20260717T0800Z-1723032000000     # audited

# Review recent security events
vapnctl audit --category security --since 2026-07-18T00:00:00Z --limit 100
```

These map to the [platform admin API](../api/README.md#c-platform-admin-api) and
mirror what the VPS Advisor admin dashboard drives via the
[decisions sync](../integration/django-integration.md#43-admin-decisions). Every
state-changing action is written to the [audit log](database-schema.md#schema-audit).

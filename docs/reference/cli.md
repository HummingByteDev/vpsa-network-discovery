# CLI Reference

Two command-line tools ship with VAPN:

- **`vapn`** — the **worker operator** CLI (community-facing). Wraps Docker so
  operators never touch containers directly. Full narrative:
  [worker command reference](../worker/command-reference.md).
- **`vapnctl`** — the **platform administration** CLI. Drives the
  [platform admin API](../api/README.md#c-platform-admin-api).

Other binaries in `cmd/` are services or tools, not interactive CLIs:
`coordinator`, `aggregator`, `builder`, `worker`, `migrate`, `mockadvisor`,
`keygen`, `loadtest` — see [configuration](configuration.md) for how they're
run.

---

## `vapn` (worker operator)

```
vapn <command>
```

| Command | Description |
|---|---|
| `install` | Set up and start a worker (interactive) |
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
recipes: [worker command reference](../worker/command-reference.md).

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
| `status` | Fleet overview |
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

# Emergency: stop all probing fleet-wide
vapnctl scheduler pause                           # …later… vapnctl scheduler resume

# Roll back a bad snapshot
vapnctl snapshots list
vapnctl snapshots rollback 20260717T0800Z-1       # audited

# Review recent security events
vapnctl audit --category security --since 2026-07-18T00:00:00Z --limit 100
```

These map to the [platform admin API](../api/README.md#c-platform-admin-api) and
mirror what the VPS Advisor admin dashboard drives via the
[decisions sync](../integration/django-integration.md#43-admin-decisions). Every
state-changing action is written to the [audit log](database-schema.md#schema-audit).

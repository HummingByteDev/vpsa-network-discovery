# Troubleshooting (Workers)

Most worker problems are one of a handful of things: Docker permissions, a
clock that drifted, a firewall blocking egress, or simply waiting on manual
approval. This page is symptom-first. For platform-side operator troubleshooting
see [Operations → Runbooks](../operations/runbooks.md).

> **Two commands solve most issues.** `vapn doctor` re-runs the system checks
> and tells you exactly what's wrong; `vapn logs` shows what the worker is
> actually doing. Start there.

## Symptom → fix

| Symptom | Likely cause | Fix |
|---|---|---|
| Stuck at **`Awaiting approval`** | Approval is a manual, human step | Check your VPS Advisor dashboard; nothing to do worker-side |
| **`Unreachable (last report …)`** in status | Worker crashed, host offline, or egress blocked | `vapn logs`, then `vapn doctor` |
| **Clock check fails** | No NTP; signed requests are time-bound | `sudo timedatectl set-ntp true` |
| **Docker permission errors** | Your user isn't in the `docker` group | `sudo usermod -aG docker $USER`, then log out/in |
| **`Coordinator unreachable`** | Strict egress firewall | Allow outbound HTTPS (443) to the coordinator domain |
| **Registration fails / token rejected** | Token spent, expired, or mistyped | Regenerate the token on VPS Advisor and re-run `vapn install` |
| **Snapshot verification failed** | Corrupt download or wrong pinned key | `vapn logs`; the worker retries automatically — persistent failure means report it |
| **No assignments after approval** | Scheduler still balancing, or you're the only worker for those targets | Wait a heartbeat cycle (~30 s); check `vapn status` |
| **Trust stuck low** | New workers start near the floor by design | [Tenure ramps over ~2 weeks](../concepts/measurement-and-consensus.md#trust); check for `bad_signature`/`replay` events with `vapn logs` |

## Diagnosing step by step

1. **Is the container running?**
   ```sh
   vapn status
   ```
   If it shows the worker as down or restarting, go to logs.

2. **What do the logs say?**
   ```sh
   vapn logs -f
   ```
   Look for the last error line. Common ones map directly to the table above
   (`clock skew`, `dial tcp … i/o timeout`, `403 suspended`, `401 signature`).

3. **Do the system checks pass?**
   ```sh
   vapn doctor
   ```
   Each failed check prints a one-line remedy.

4. **Is it a network problem?** From the host:
   ```sh
   curl -fsSI https://<your-coordinator-domain>/healthz
   ```
   A hang or refusal means egress filtering; a 200 means the path is clear and
   the problem is worker-side.

## Specific situations

### "My worker was quarantined"

The platform quarantines workers whose measurements consistently disagree with
consensus, whose clock is wrong, or that submit invalid signatures. Quarantined
workers keep measuring in **shadow mode** (weight 0) and can earn trust back.
`vapn logs` and the trust events in `vapn status` explain why. Fix the
underlying issue (usually the clock or an outdated image) and trust recovers
over subsequent windows. See [Trust calculation](../walkthroughs/trust-calculation.md).

### "It was working, now it's silent"

Usually a host reboot or Docker restart. The worker is set to
`restart: unless-stopped`, so it should recover on its own; if it doesn't:

```sh
vapn resume        # if you had paused it
vapn logs -f       # find the crash
vapn doctor        # confirm environment still healthy
```

### "Updates keep rolling back"

`vapn update` rolls back if the new image doesn't become healthy in two
minutes. That means the *new* version fails in your environment — capture logs
during the attempt (`vapn logs -f` in another terminal) and report them. Your
worker stays on the working previous version in the meantime.

## Still stuck?

- Re-read the relevant guide: [Installation](installation.md) ·
  [Updating](updating.md) · [FAQ](faq.md).
- Understand the lifecycle so the state names make sense:
  [Worker lifecycle](../worker/lifecycle.md).
- Open an issue on the project's GitHub with `vapn doctor` output and the last
  ~50 lines of `vapn logs` (they contain no secrets — the private key never
  appears in logs).

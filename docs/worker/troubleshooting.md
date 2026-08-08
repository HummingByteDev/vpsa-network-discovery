# Worker Troubleshooting & FAQ

Most worker problems are one of a handful of things: Docker permissions, a
clock that drifted, a firewall blocking egress, a mismatched snapshot public
key, or simply waiting on manual approval.

> **Two commands solve most issues.** `vapn doctor` re-runs the system checks
> and tells you exactly what's wrong; `vapn logs` shows what the worker is
> actually doing. Start there.

Platform-side operator troubleshooting is in
[Operations → Runbooks](../operations/runbooks.md).

## Symptom → fix

| Symptom | Likely cause | Fix |
|---|---|---|
| Stuck at **`Awaiting approval`** | Approval is a manual, human step | Check your VPS Advisor dashboard; nothing to do worker-side |
| **`Unreachable (last report …)`** in status | Worker crashed, host offline, or egress blocked | `vapn logs`, then `vapn doctor` |
| **`no status report yet`** | Container still starting, or it failed to boot | `vapn logs -f` — a config error is printed on the first lines |
| **Clock check fails** | No NTP; signed requests are time-bound | `sudo timedatectl set-ntp true` |
| **Docker permission errors** | Your user isn't in the `docker` group | `sudo usermod -aG docker $USER`, then log out/in |
| **`Coordinator reachable` fails** | Strict egress firewall, or a typo in the URL | Allow outbound HTTPS (443) to the coordinator domain |
| **`Snapshot public key` check fails** | The value is missing, truncated, or not base64 | Re-run `vapn install` and paste the key exactly as given |
| **Registration fails / token rejected** | Token spent, expired, or mistyped | Regenerate the token on VPS Advisor and re-run `vapn install` |
| **Snapshot verification failed** | Wrong pinned key, or a corrupt download | Confirm your public key matches the platform's (below); the worker retries corrupt downloads automatically |
| **No assignments after approval** | Scheduler still balancing, or you're the only worker for those targets | Wait a heartbeat cycle (~30 s); check `vapn status` |
| **Trust stuck low** | New workers start near the floor by design | [Tenure ramps over ~2 weeks](../concepts/measurement-and-consensus.md#trust); check for `bad_signature`/`replay` events in `vapn logs` |

## Diagnosing step by step

1. **Is the container running and reporting?**
   ```sh
   vapn status
   ```
   If it shows the worker as unreachable or has no report at all, go to logs.

2. **What do the logs say?**
   ```sh
   vapn logs -f
   ```
   Look for the last error line. Common ones map directly to the table above
   (`clock skew`, `dial tcp … i/o timeout`, `403 suspended`, `401 signature`).
   A `bad configuration` line names every missing setting at once.

3. **Do the system checks pass?**
   ```sh
   vapn doctor
   ```
   Each failed check prints a specific reason.

4. **Is it a network problem?** From the host:
   ```sh
   curl -fsSI https://<your-coordinator-domain>/api/v1/workers/me
   ```
   A `401` is the **healthy** result — it means the coordinator is up, TLS is
   fine, and it is correctly refusing an unsigned request. A hang or connection
   refusal means egress filtering.

## Specific situations

### "Snapshot verification keeps failing"

Your worker refuses any routing snapshot not signed by the key it was given.
Persistent failure almost always means it holds the *wrong* key.

```sh
grep VAPN_SNAPSHOT_PUBLIC_KEY ~/.vapn/config.env
```

Compare that value character for character with the public key published by
whoever runs the platform. If they differ, re-run `vapn install` and paste the
correct one. If they match and verification still fails, report it — that would
mean something is tampering with the artifact in transit.

### "My worker was quarantined"

The platform quarantines workers whose measurements consistently disagree with
consensus, whose clock is wrong, or that submit invalid signatures. Quarantined
workers keep measuring in **shadow mode** (weight 0) and can earn trust back.

`vapn logs` explains why. Fix the underlying issue (usually the clock or an
outdated image) and trust recovers over subsequent windows. See
[Trust calculation](../walkthroughs/trust-calculation.md).

### "It was working, now it's silent"

Usually a host reboot or Docker restart. The worker is set to
`restart: unless-stopped`, so it should recover on its own; if it doesn't:

```sh
vapn resume        # if you had paused it
vapn logs -f       # find the crash
vapn doctor        # confirm the environment is still healthy
```

### "Updates keep rolling back"

`vapn update` rolls back if the new image doesn't become healthy in two
minutes. That means the *new* version fails in your environment — capture logs
during the attempt (`vapn logs -f` in another terminal) and report them. Your
worker stays on the working previous version in the meantime.

---

## Frequently asked questions

### About the project

**What is VAPN, in one sentence?**
A distributed system where community-run workers measure the public network
health of VPS providers from many locations, and a platform combines those
measurements into trusted, consensus-based verdicts published on VPS Advisor.

**Is this the VPS Advisor website?**
No. VPS Advisor is a separate, already-live review platform. VAPN is the
measurement backend behind its *Provider Network Health* feature. See
[Documentation Home → Project background](../README.md#project-background).

**Does VAPN scan the whole Internet?**
No. It only ever measures providers that exist on VPS Advisor, using only the
network routes those providers publicly announce. VPS Advisor is the sole
source of truth for which providers to monitor. See
[Core Concepts](../concepts/README.md).

**Is it open source?**
Yes — the platform, worker, and all documentation are in one repository. See
[Development](../development/README.md) to contribute.

### Running a worker

**What do I need to run a worker?**
A Linux machine, Docker, an enrollment token, and the platform's snapshot
public key. → [Installation](installation.md).

**How much CPU / RAM / bandwidth does it use?**
Very little — a few MB of RAM, negligible CPU, and a trickle of bandwidth. Full
numbers in [Resource usage](resources-and-privacy.md#resource-usage).

**Why is my worker "Awaiting approval"?**
New workers are approved by a human as an anti-abuse measure. Once approved it
starts probing automatically; you don't need to do anything. See
[Installation](installation.md#what-awaiting-approval-means).

**Can my worker see or measure *my* servers?**
No. Workers only probe addresses that appear in the signed routing snapshot,
which is derived exclusively from monitored providers' publicly announced
routes. Workers never choose their own targets. See
[Privacy](resources-and-privacy.md#privacy).

**Will running a worker expose anything about my machine?**
The worker reports its measurements, its software version, and liveness. It
does not read your files or other network traffic, and your private key never
leaves your machine. See [Privacy](resources-and-privacy.md#privacy).

**Can I pause participation?**
Yes: `vapn pause` stops probing and keeps your identity and trust; `vapn resume`
resumes. Pausing is better than uninstalling if you'll come back — uninstalling
creates a fresh identity next time.

**How do I completely remove it?**
`vapn uninstall` removes containers, images, and all state, and offers to
unregister you cleanly. → [Leaving](operations.md#leaving).

**What happens if my machine reboots?**
The worker restarts automatically (`restart: unless-stopped`) and resumes on
its own — it re-reads its identity, re-downloads any new snapshot, and
continues.

### Trust and measurements

**What is "trust" and why is mine low?**
Trust is a 0–1 score reflecting how reliable your worker has proven to be. New
workers start near the floor and ramp up over about two weeks (this caps the
value of spinning up many fake workers). Bad clocks and invalid signatures
lower it. → [Trust](../concepts/measurement-and-consensus.md#trust).

**Why doesn't one worker's measurement become the public result?**
Because any single worker could be wrong, misconfigured, or malicious. Public
verdicts come only from **consensus** across many workers, weighted by trust.
→ [Consensus](../concepts/measurement-and-consensus.md#consensus-from-many-views-to-one-verdict).

**My worker says a provider is down but the site says "healthy" — why?**
Your worker is one vantage point. If most trusted workers still reach the
provider, consensus is "healthy" and a regional issue near *you* may be the
cause. If enough workers agree, the verdict changes. That disagreement is the
system working as designed.

**What does `insufficient_data` mean on a provider?**
Not enough distinct, trusted workers measured that provider (or region) in the
window to call it confidently. It is deliberately **not** shown as an outage —
absence of data is not evidence of a problem.

### Security & privacy

**Could a malicious worker poison the results?**
That's an explicit design assumption. Mitigations: redundancy (many workers per
target across different operators/networks), trust weighting, dissent scoring,
signed-and-timestamped measurements, and shadow-mode quarantine. See the
[Security & trust model](../architecture/05-security-trust-model.md).

**Is my traffic to the coordinator encrypted?**
Yes — all worker↔coordinator traffic is HTTPS, and every request is
additionally signed with your worker's key so the platform can prove it came
from you and hasn't been altered or replayed.

**Where can I look up a term I don't understand?**
The [Glossary](../reference/glossary.md) defines every networking and
project-specific term in plain language.

---

## Still stuck?

- Re-read the relevant guide: [Installation](installation.md) ·
  [Operating a worker](operations.md).
- Understand the lifecycle so the state names make sense:
  [Worker lifecycle](lifecycle.md).
- Open an issue on the project's GitHub with `vapn doctor` output and the last
  ~50 lines of `vapn logs` (they contain no secrets — the private key never
  appears in logs).

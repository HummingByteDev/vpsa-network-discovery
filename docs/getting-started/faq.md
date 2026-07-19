# Frequently Asked Questions

General and worker-focused questions. Operator/platform questions are answered
throughout [Operations](../operations/README.md); integration questions in the
[Integration guide](../integration/README.md).

## About the project

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

## Running a worker

**What do I need to run a worker?**
A Linux machine, Docker, and an enrollment token from VPS Advisor. That's all.
→ [Quick Start](quickstart.md).

**How much CPU / RAM / bandwidth does it use?**
Very little — a few MB of RAM, negligible CPU, and a trickle of bandwidth. Full
numbers in [Resource usage](../worker/resources-and-privacy.md#resource-usage).

**Why is my worker "Awaiting approval"?**
New workers are approved by a human as an anti-abuse measure. Once approved it
starts probing automatically; you don't need to do anything. See
[Quick Start](quickstart.md#what-awaiting-approval-means).

**Can my worker see or measure *my* servers?**
No. Workers only probe addresses that appear in the signed routing snapshot,
which is derived exclusively from monitored providers' publicly announced
routes. Workers never choose their own targets. See
[Privacy](../worker/resources-and-privacy.md#privacy).

**Will running a worker expose anything about my machine?**
The worker reports its measurements, its software version, and liveness. It
does not read your files or other network traffic, and your private key never
leaves your machine. See [Privacy](../worker/resources-and-privacy.md#privacy).

**Can I pause participation?**
Yes: `vapn pause` stops probing and keeps your identity and trust; `vapn resume`
resumes. Pausing is better than uninstalling if you'll come back — uninstalling
creates a fresh identity next time.

**How do I completely remove it?**
`vapn uninstall` removes containers, images, and all state, and offers to
unregister you cleanly. → [Uninstalling](uninstalling.md).

**What happens if my machine reboots?**
The worker restarts automatically (`restart: unless-stopped`) and resumes on its
own — it re-reads its identity, re-downloads any new snapshot, and continues.

## Trust and measurements

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

## Security & privacy

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

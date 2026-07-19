# Getting Started

The fastest path from "I've heard of VAPN" to "I'm contributing measurements."
These guides are deliberately light on theory — when you want the *why*, follow
the links into [Core Concepts](../concepts/README.md).

There are two ways to "get started," depending on who you are:

- **Contribute a worker.** You have a Linux machine with a bit of spare
  bandwidth and want to help measure provider health. This is the common case
  and the focus of this section. → [Quick Start](quickstart.md)
- **Run the whole platform.** You are deploying VAPN itself (coordinator,
  aggregator, builder, database). That is an operator task with its own guide.
  → [Deployment guide](../operations/deployment.md)

## Contributor path

| Step | Guide | Time |
|---|---|---|
| 1 | [Quick Start](quickstart.md) — get a worker running | ~5 min |
| 2 | [Installation](installation.md) — the details behind the quick start | ~10 min |
| 3 | [What it costs & how privacy works](../worker/resources-and-privacy.md) | reading |
| 4 | [Updating](updating.md) — keep the worker current | ~2 min |
| 5 | [Uninstalling](uninstalling.md) — leave cleanly whenever you like | ~2 min |

Stuck? → [Troubleshooting](troubleshooting.md) · [FAQ](faq.md)

## What you'll need

- A **Linux** machine (amd64 or arm64) — a cheap VPS, a home server, or a
  spare box. It does **not** need to be powerful; a worker uses a few MB of
  RAM and a trickle of bandwidth (see [resource usage](../worker/resources-and-privacy.md)).
- **[Docker](https://docs.docker.com/engine/install/)** installed and running.
- An **enrollment token** from your VPS Advisor dashboard
  (My Workers → Create worker). This is a one-time secret that ties the worker
  to your account.

That's the whole shopping list. Everything else — keys, routing data, GeoIP,
assignments — the worker sets up automatically.

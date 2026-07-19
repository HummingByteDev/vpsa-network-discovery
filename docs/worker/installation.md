# Worker Installation (Details)

The friendly step-by-step lives in
[Getting Started → Installation](../getting-started/installation.md) and
[Quick Start](../getting-started/quickstart.md). This page adds the container-
level details for people who want to know exactly what runs, or who install by
hand rather than through `vapn install`.

## The three install methods (recap)

Canonical source is **GitHub**. In increasing order of "show me exactly what
runs":

1. **Piped:** `curl -fsSL <install.sh raw URL> | bash`
2. **Download, read, run:** save the script, `less` it, then `bash` it.
3. **Clone + build:** `git clone … && make build && ./bin/vapn install`.

Full commands and reasoning:
[Getting Started → Choose how you install](../getting-started/installation.md#choose-how-you-install).

## What the container actually looks like

`vapn install` generates a `~/.vapn/docker-compose.yml` roughly like this:

```yaml
services:
  worker:
    image: ghcr.io/hummingbytedev/vapn-worker:latest
    cap_add: [NET_RAW]                 # the ONE capability it needs (ICMP)
    environment:
      VAPN_ENROLLMENT_TOKEN: "…"       # spent on first boot
      VAPN_COORDINATOR_URL: "https://probe-api.vpsadvisor.example"
      # VAPN_MAXMIND_LICENSE_KEY: "…"  # optional; your own key
    volumes: [worker-state:/state]     # identity + snapshot + upload queue
    restart: unless-stopped            # survives reboots
volumes: { worker-state: }
```

Key points:

- **`cap_add: [NET_RAW]` and nothing else.** Sending an ICMP echo needs a raw
  socket, which needs `CAP_NET_RAW`. The container is **not** privileged, does
  not use host networking, and cannot see your files. See
  [privacy](resources-and-privacy.md#privacy).
- **One persistent volume** holds the worker's identity (its private key), the
  downloaded routing snapshot, and the local upload queue. Back it up with
  `vapn backup`.
- **`restart: unless-stopped`** means the worker returns automatically after a
  reboot or Docker restart.

## Configuration surface

A worker needs only two settings; everything else is automatic:

| Variable | Required | Purpose |
|---|---|---|
| `VAPN_COORDINATOR_URL` | yes | The platform endpoint the worker talks to |
| `VAPN_ENROLLMENT_TOKEN` | yes (first boot) | One-time proof a real operator started it |
| `VAPN_MAXMIND_LICENSE_KEY` | no | Your own key for a local GeoIP DB (degrades gracefully without) |
| `VAPN_HOME` | no | Override the `~/.vapn` location |

The complete list is in the
[configuration reference](../reference/configuration.md).

## Requirements checklist

| Requirement | Check |
|---|---|
| Linux amd64/arm64 | `uname -m` |
| Docker running for your user | `docker info` |
| Outbound HTTPS (443) to the coordinator | `curl -fsSI https://<coordinator>/healthz` |
| Synchronized clock | `timedatectl` shows NTP active |
| An enrollment token | VPS Advisor → My Workers → Create worker |

Any of these failing is caught by `vapn doctor` with a specific fix. See
[Troubleshooting](../getting-started/troubleshooting.md).

## After install

- Worker is `pending` until [manually approved](lifecycle.md#pending).
- `vapn status` / `vapn doctor` to verify.
- Turn on [automatic updates](../getting-started/updating.md).
- Learn the [commands](command-reference.md) and the
  [lifecycle](lifecycle.md).

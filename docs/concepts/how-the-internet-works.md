# How the Internet Works (for Developers)

**Problem this solves:** to measure whether a provider's *network* is
reachable, you first have to understand what "an address on the network" even
is, and how a packet you send finds its way there. This page builds that
foundation. If you've written an HTTP client but never thought about what
happens below the socket, this is for you.

> **Analogy we'll use throughout:** the Internet is a global postal system.
> Addresses, postcodes, sorting hubs, and delivery routes all have direct
> equivalents. We'll build the analogy up piece by piece.

## IP addresses: the postal addresses of the Internet

Every device that talks on the Internet has at least one **IP address** — a
numeric label that uniquely identifies it, like a postal address identifies a
building.

There are two versions in use:

- **IPv4** — four numbers 0–255 separated by dots: `203.0.113.7`. That's 32
  bits, so about 4.3 billion possible addresses. We ran out of these years ago,
  which is why they're rationed and traded.
- **IPv6** — eight groups of hexadecimal: `2001:db8::7`. That's 128 bits — an
  effectively unlimited supply.

VAPN handles both. When this documentation writes an address, assume the same
ideas apply to v4 and v6 unless stated otherwise.

```
203.0.113.7
└──┬──┘ └┬┘
 network  host   (roughly: which building block, which door)
```

That split — "which block of the network" vs "which specific host in it" — is
the single most important idea for understanding routing. Let's make it precise.

## Address blocks and CIDR notation

Addresses aren't handed out one at a time; they're allocated in **contiguous
blocks**. A block is written in **CIDR** notation (Classless Inter-Domain
Routing), which looks like:

```
203.0.113.0 / 24
└────┬────┘   └┬┘
 address       prefix length (bits fixed)
```

The number after the slash is the **prefix length**: how many leading bits are
fixed ("the network part"). The rest are free ("the host part").

- `/24` fixes the first 24 of 32 bits, leaving 8 bits → **256** addresses
  (`203.0.113.0` … `203.0.113.255`).
- `/16` fixes 16 bits, leaving 16 → **65,536** addresses.
- `/32` fixes all 32 bits → exactly **one** address.

**Smaller number after the slash = bigger block.** Each step down doubles the
size. A `/23` is two `/24`s; a `/22` is four.

> **Postal analogy:** `203.0.113.0/24` is "every address on Maple Street" —
> a whole street of 256 doors. `203.0.113.7/32` is "7 Maple Street" — one
> door. Routing works at the *street* level, not the door level, which is why
> blocks matter.

In VAPN's vocabulary, an announced address block is called a **prefix**. When
you read "a provider's prefixes," think "the streets that provider owns." A
provider might own `203.0.113.0/24`, `198.51.100.0/22`, and a handful of IPv6
blocks — collectively, its *public network*.

### Why blocks instead of individual addresses?

Because routers would drown otherwise. There are billions of addresses but only
around a million *blocks* announced globally. Routers keep a table of blocks,
not addresses — "to reach anything in `203.0.113.0/24`, go this way." Grouping
addresses into blocks is what keeps the global routing table a manageable size.
This is the same reason VAPN reasons about prefixes, not individual IPs.

## How a packet finds its way

Say your worker sends a probe to `203.0.113.7`. How does that packet cross the
planet to the right building?

1. Your machine sees the destination isn't on its local network, so it hands the
   packet to its **default gateway** (your router).
2. Your router doesn't know where `203.0.113.7` is either, so it forwards the
   packet toward *its* provider (your ISP).
3. At each hop, a router consults its **routing table**: a big list of "for
   this block of addresses, send toward that neighbor." It picks the entry that
   most specifically matches the destination and forwards the packet one hop
   closer.
4. This repeats — router to router, network to network — until the packet
   reaches a router *inside* the destination's network, which delivers it to
   the actual host.

```mermaid
flowchart LR
  W[Your worker] --> R1[Your router]
  R1 --> ISP[Your ISP]
  ISP --> T1[Transit network]
  T1 --> T2[Another network]
  T2 --> P["Provider's network<br/>(owns 203.0.113.0/24)"]
  P --> H[Host 203.0.113.7]
```

> **Postal analogy:** no single post office knows the location of every address
> on Earth. Each sorting hub only knows "mail for this region goes to that next
> hub." A letter hops hub to hub, each step narrowing it down, until it reaches
> the local office that knows the actual street.

The crucial insight: **no router has a map of the whole Internet.** Each one
just knows the *next hop* for each block of addresses. The global journey
emerges from many local decisions.

Which raises the question this whole system depends on: **how does a router
learn "for block X, send toward network Y"?** That's the job of the routing
protocol between networks — **BGP** — and the identity of those networks —
**ASNs** — which is exactly what the [next page](asn-and-bgp.md) covers.

## Latency and reachability: what VAPN actually measures

Two properties of that journey are what VAPN cares about:

- **Reachability** — does a packet get there and back *at all*? If it does, the
  chain of routes from your vantage point to the provider is intact.
- **Latency** — how long does the round trip take? This is the **RTT**
  (round-trip time), measured in milliseconds. It reflects physical distance and
  network congestion along the path.

A worker measures both by sending an **ICMP echo request** (a "ping") to a
target address and timing the reply. Reachability = did a reply come back;
latency = how long it took. Simple in principle — but a *single* ping from a
*single* place is a weak signal, which is the problem
[consensus](measurement-and-consensus.md) exists to solve.

## Key terms from this page

| Term | Meaning |
|---|---|
| **IP address** | Numeric label identifying a device (`203.0.113.7`) |
| **IPv4 / IPv6** | The 32-bit and 128-bit address schemes |
| **CIDR** | Notation for an address block: `address/prefix-length` |
| **Prefix** | An announced address block; a provider's "streets" |
| **Prefix length** | Bits fixed as the network part; smaller = bigger block |
| **Routing table** | A router's list of "for this block, next hop is…" |
| **RTT** | Round-trip time — the latency of a probe |
| **Reachability** | Whether a packet gets there and back at all |

Next: [ASN & BGP](asn-and-bgp.md) — who owns these blocks, and how the whole
Internet learns where they are.

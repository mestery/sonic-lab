# tgen — Topology Generator

`tgen` generates a complete SONiC virtual lab from command-line flags. It
produces a `docker-compose.yml`, a `topology.yml` (consumed by webmesh),
and supporting shell scripts — ready to run with `./start.sh`.

## Usage

```
tgen [flags]
```

### Topology Modes

| Flag | Description |
|------|-------------|
| `-s <int>` | Number of spine switches (spine-leaf mode) |
| `-l <int>` | Number of leaf switches (spine-leaf mode) |
| `-m` | Generate full mesh topology |
| `-r` | Generate ring topology |
| `-n <int>` | Number of nodes (for `-m` or `-r`) |
| `-links <int>` | Parallel links per switch pair (default 1; enables ECMP) |
| `-h <int>` | Number of hosts attached to each leaf (or ring/mesh node) |
| `-p <int>` | Number of ports per host (default 1; each port maps to a different switch) |
| `-host-spines` | Also attach hosts to spines (spine-leaf mode) |
| `-no-host-addr` | Don't generate the default host addressing (`addressing.txt`) |
| `-srv6` | Enable SRv6 overlay (locators, SIDs, seg6 kernel routes) |
| `-o <dir>` | Output directory (default `.`, must be a direct child of repo root) |

### Examples

```bash
# Classic spine-leaf: 1 spine, 4 leaves (each leaf has 1 uplink)
go run . -s 1 -l 4 -o ../mylab/

# Dual-spine: 2 spines, 6 leaves (each leaf has 2 uplinks)
go run . -s 2 -l 6 -o ../mylab/

# Full mesh: 5 nodes, every node connected to every other
go run . -m -n 5 -o ../mesh-lab/

# Ring: 8 nodes in a circular loop
go run . -r -n 8 -o ../ring-lab/

# Spine-leaf with ECMP: 2 spines, 4 leaves, 4 parallel links per pair
go run . -s 2 -l 4 -links 4 -o ../ecmp/

# Spine-leaf with SRv6 overlay
go run . -s 2 -l 4 -srv6 -o ../srv6-lab/

# Spine-leaf with hosts: 2 spines, 2 leaves, 1 dual-stack host per leaf
go run . -s 2 -l 2 -h 1 -o ../lab-with-hosts/

# 2 hosts per leaf, 2 ports per host (each port on a different switch)
go run . -s 2 -l 2 -h 2 -p 2 -o ../lab-with-hosts/
```

### Hosts

The `-h` flag adds simulated host containers to the topology. Each leaf
(or each ring/mesh node) gets `-h` hosts; spines get hosts only with
`-host-spines`. Each host has `-p` ports (default 1). Port `p` of a host
connects to the `p`-th switch after its home switch, wrapping around,
giving natural multi-homed redundancy.

Hosts are built from `Dockerfile.host` (a small Debian image with
`iproute2`, `tcpdump`, `bridge-utils`, `ifenslave`, `ethtool`, and
`iputils-ping`).

#### Host addressing

By default every host link gets its own dual-stack subnet, so hosts can
ping each other across the fabric as soon as the lab is up:

| | IPv4 | IPv6 |
|---|---|---|
| Subnet | `10.S.H.0/24` | `fd00:0:S:H::/64` |
| Switch port | `10.S.H.1` | `fd00:0:S:H::1` |
| Host port | `10.S.H.2` | `fd00:0:S:H::2` |

- `S` is the switch's index: leaf `N` (or node `N`) is `N`; spines,
  if they have hosts, start at 60 (`spine1` is 60, `spine2` is 61).
  With 60 or more leaves, leaf indexes reach the spine range; tgen
  warns, and fails only if a leaf and a spine with hosts actually
  collide.
- `H` counts the host links on that switch, starting at 1.

So `host1` on `leaf1` is `10.1.1.2` / `fd00:0:1:1::2`, and the second
host on `leaf3` is `10.3.2.2` / `fd00:0:3:2::2`. Each host routes
`10.0.0.0/8` and `fd00::/32` via its switch. With `-p > 1` a host gets an
address on every port, and the fabric route uses the last port listed.

The addresses are **not** baked into the switch config. tgen writes:

- `addressing.txt`: one address per line (`device port address
  [gateway]`), readable and editable by hand
- `apply-addressing.sh`: applies a file like it, or removes it with `-r`

`start.sh` runs `./apply-addressing.sh addressing.txt` after the lab
comes up. The script waits for each switch to finish booting (see the
ready marker below), uses `config interface ip add` on switches and
`ip addr`/`ip route` on hosts, and is safe to re-run.

**The generated files are the starting state; the devices hold what's
live.** Nothing calls `config save`, so a restarted switch comes back
without host addresses; re-apply the file (or your edited copy) to
restore them. To change addressing while the lab runs:

```bash
./apply-addressing.sh -r addressing.txt   # remove the current addresses
vi addressing.txt                         # or edit a copy
./apply-addressing.sh addressing.txt      # apply the new ones
```

or use `config interface ip add/remove` and `ip addr` directly.
No FRR change is needed: the fabric advertises any connected subnet
inside `10.0.0.0/8` or `fd00::/32` (except `fd00::/48`, which holds the
switch loopbacks and SRv6 locators), so a new subnet is advertised as
soon as it's configured and withdrawn when it's removed.

For a different scheme, pass `-no-host-addr` and configure hosts
yourself (with your own addressing file and `apply-addressing.sh`, or
any other tool). Subnets inside the two pools are routed with no other
changes. Subnets outside them need a matching change to the
`HOST-SUBNETS` route-map in the switches' FRR config.

#### Switch ready marker

`sonic_entry.sh` creates `/tmp/sonic-lab-ready` in the switch container
when boot configuration (Loopback0, FRR config, SRv6 routes) is done,
and removes it at the start of every boot. Scripts can wait for it:

```bash
until docker exec sonic-leaf1 test -f /tmp/sonic-lab-ready; do sleep 2; done
```

It means the switch is configured, not that BGP has converged. Routes to
other switches' host subnets can take another minute or two, especially
after a restart.

### How It Works

1. **Generate** — `tgen/internal/topo` builds the topology in memory
   (switch list + link list) from the flags.
2. **Write** — `tgen/internal/writer` emits:
   - `docker-compose.yml` — defines webmesh, backing Linux containers, and SONiC containers
   - `topology.yml` — the desired topology in the YAML format webmesh expects
   - `init_linux.sh` — backing container entrypoint (touches vnet-ready file)
   - `sonic_entry.sh` — SONiC container entrypoint (waits for vnet, starts supervisord,
     creates Loopback0, loads FRR config, optionally loads SRv6 routes)
   - `frr/<switch>.conf` — per-switch FRR configuration (BGP unnumbered + optional SRv6)
   - `srv6/<switch>.routes` — per-switch seg6 kernel routes (only when `-srv6`)
   - `addressing.txt` — default host addressing (only with hosts, unless `-no-host-addr`)
   - `apply-addressing.sh` — applies or removes an addressing file (only with hosts)
   - `start.sh` — convenience script to build and launch everything
   - `tmp/vnet_ready/` — shared directory for readiness signaling

### Output Directory Constraint

The output directory **must be a direct child of the repo root** (e.g.,
`../mylab/` when running from `tgen/`). This is because the generated
`docker-compose.yml` uses `context: ..` to locate `Dockerfile.webmesh`
and `Dockerfile.debian` at the repo root.

### Port Allocation

Ports use step-4 Ethernet naming (`Ethernet0`, `Ethernet4`, `Ethernet8`,
…) to match the KVM platform's 30-lane map.

- **Spine-leaf**: Spine `j` allocates ports to leaves in order
  (`Ethernet0` → leaf1, `Ethernet4` → leaf2, …). Leaf `i` allocates
  ports to spines in order.
- **Mesh**: Node `i` connects to node `j` (j > i) on port index `j-1`.
  Node `j` connects back on port index `i`.
- **Ring**: Each node uses exactly 2 ports — `Ethernet0` (prev) and
  `Ethernet4` (next).

### Constraints

| Topology | Min nodes | Max nodes | Ports per node |
|----------|-----------|-----------|----------------|
| Spine-leaf | 1 spine + 1 leaf | 30 spines, 30 leaves | S per leaf, L per spine |
| Mesh | 2 | 31 | N-1 |
| Ring | 3 | unlimited | 2 |

(30 is the max due to the KVM 30-lane platform limitation.)

### BGP Unnumbered Underlay

Every switch gets an eBGP ASN and a deterministic VTEP IPv6 ULA loopback
address (`fd00:0:0:<asn>::1/128`). The generated `frr/<switch>.conf`
file configures FRR with:

- **Loopback0** — the VTEP address, created by `sonic_entry.sh` as a
  dummy interface before BGP starts
- **BGP unnumbered** — one neighbor per switch-to-switch link, using the
  kernel interface name (`Ethernet0`, `Ethernet4`, …) with the `interface`
  keyword and a shared `TRANSIT` peer-group (sessions established over
  link-local; `ebgp-multihop` suppresses TTL check)
- **IPv6 address family** — each switch advertises its VTEP `/128` prefix;
  `next-hop-self` is set for multipath
- **Host subnets** — both address families redistribute connected
  subnets through the `HOST-SUBNETS` route-map (`10.0.0.0/8` and
  `fd00::/32`, minus `fd00::/48`). IPv4 routes use IPv6 link-local next
  hops (RFC 5549), so no IPv4 addresses are needed between switches.

Host connections are excluded from the BGP neighbor list. The FRR config
is mounted at `/tmp/frr.conf` (read-only) inside each SONiC container and
loaded explicitly via `vtysh -f` after `bgpd` is running.

### SRv6 Overlay (`-srv6`)

When `-srv6` is specified, tgen adds an SRv6 overlay on top of the BGP
unnumbered underlay. The address scheme is:

| Purpose | Prefix | Interface |
|---------|--------|-----------|
| VTEP (End SID) | `fd00:0:0:<asn>::1/128` | Loopback0 |
| Locator | `fd00:0:0:<asn>::/64` | Loopback0 |
| Overlay service | `2001:db8:<asn>::/64` | Loopback0 |

Per-switch artifacts:

- **FRR config** adds a `segment-routing srv6` block with a locator and a
  static End SID (`behavior uN`), plus `segment-routing srv6` inside the
  BGP router section (enables SID advertisement/distribution).
- **`srv6/<name>.routes`** — a shell script (executed by `sonic_entry.sh`
  after the FRR config loads) that:
  1. Adds the locator `/64` to Loopback0 as a connected address. This is the
     linchpin: FRR advertises the locator via BGP (`network <locator>/64`),
     but that advertisement only takes effect once the prefix is present in
     the kernel RIB. Making the locator a connected Loopback0 address is what
     lets every other switch learn an underlay route to it — and that underlay
     route is how the SRv6 outer-dst (End SID) lookup resolves across the
     fabric.
  2. Adds the End SID (`<locator>::100/128`, behavior `uN`) and the overlay
     `/128` to Loopback0. (The VTEP `<locator>::1/128` is added separately by
     `sonic_entry.sh`.)
  3. Programs a seg6 kernel route for each remote switch's overlay prefix:
     ```
     ip -6 route add <remote-overlay>/64 encap seg6 mode encap segs <remote-sid>
     ```
     The single-segment SRH (remote End SID) causes the kernel to encapsulate
     the packet with an outer IPv6 header destined to the remote End SID. The
     BGP underlay routes the outer packet (resolving the SID via the remote
     locator `/64`); the remote switch decapsulates and delivers the inner
     packet to its own overlay address.

The routes file is mounted at `/srv6.routes` (read-only) and executed by
`sonic_entry.sh` after the FRR config loads.

**Verifying the control plane:**

```bash
# From any switch, confirm FRR loaded the SRv6 config:
docker exec sonic-leaf1 vtysh -c "show segment-routing srv6 locator"
docker exec sonic-leaf1 vtysh -c "show segment-routing srv6 sid"

# Confirm BGP is distributing VTEP prefixes (should show all switch VTEPs):
docker exec sonic-leaf1 vtysh -c "show ipv6 bgp" | grep "fd00:0:0"
```

**Verifying the data plane:**

```bash
# From any switch, ping the remote switch's overlay address:
docker exec sonic-leaf1 ping6 -c 3 2001:db8:fe4d::1
```

> **`seg6_enabled=1` is required on all interfaces.** The kernel's default
> for `seg6_enabled` is `0`, and setting it on the `all` pseudo-interface
> does not propagate to already-created interfaces. The `sonic_entry.sh`
> script sets `seg6_enabled=1` explicitly on every `Ethernet*` and
> `Loopback0` interface before loading seg6 routes. Without this, the
> receiving kernel silently drops seg6-encapsulated packets (no counter
> increment, no error) — the decapsulation path is simply disabled.
>
> The SRv6 data plane (encap + decap across namespaces) is fully working
> in the Colima 6.8 HWE kernel environment when `seg6_enabled=1` is set
> on all interfaces on all switches.

> **IPv6 forwarding is required on multi-hop topologies (ring, spine-leaf).**
> The kernel default for `net.ipv6.conf.all.forwarding` is `0`. In a full
> mesh every pair of switches is directly connected, so no switch ever has to
> forward transit traffic and the default is harmless. In a ring or spine-leaf,
> reaching a non-adjacent switch's End SID requires intermediate switches to
> forward the (SRv6-encapsulated) outer packet; with forwarding disabled the
> kernel silently drops it and only directly-adjacent overlay pings succeed.
> `sonic_entry.sh` sets `net.ipv6.conf.all`/`default` (and per-interface)
> `forwarding=1` alongside the `seg6_enabled` sysctls before loading routes.

**Kernel requirement:** The host kernel must have `CONFIG_IPV6_SEG6_LWTUNNEL=y`
(built-in or module). Colima's 6.8.0 kernel includes it. No special Docker
image is needed — stock `docker-sonic-vs:latest` (iproute2 6.1.0) supports
seg6 lwtunnel routes.

### Build

```bash
cd tgen
go build -o tgen .
```
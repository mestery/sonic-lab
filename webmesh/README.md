# webmesh

`webmesh` is a daemon that runs inside a privileged Docker container and
manages inter-switch veth wiring for SONiC virtual switch labs. It provides
a REST API and web UI for dynamically creating, destroying, and flapping
network links at runtime — powered by eBPF.

## How It Works

Each link between two switches uses **two veth pairs** managed by webmesh:

```
Switch A netns          webmesh netns            Switch B netns
┌────────────┐      ┌─────────────────────┐      ┌────────────┐
│ EthernetX  │◄──┐  │ wm-ethA ═══ wm-ethB │◄──┐  │ EthernetY  │
│  (TAP)     │   └─►│(tc ingr)   (tc ingr)│   └─►│  (TAP)     │
└────────────┘      └─────────────────────┘      └────────────┘
```

An eBPF TC ingress program is attached to both webmesh-side veth endpoints.
On packet arrival, it looks up the ingress ifindex in a BPF map to find the
peer ifindex, then calls `bpf_redirect_peer()` to deliver the packet directly
to the remote switch's interface — zero-copy, no L2 switch, no routing.

### Key Properties

- **Dynamic rewiring** — update the BPF map at runtime to rewire links
  without tearing down interfaces
- **Link flapping** — bring a link down/up without recreating veths
- **Per-link stats** — packet and byte counters per link for observability
- **SONiC-transparent** — switches see normal Ethernet link state; they are
  unaware of the underlying mechanism

## Components

```
webmesh/
├── cmd/
│   ├── webmesh/          # Server daemon (API + eBPF + reconciler)
│   └── webmeshctl/       # Operator CLI (wait, push, links)
├── internal/
│   ├── api.go            # HTTP API + routing
│   ├── reconciler.go     # Desired vs applied state reconciliation loop
│   ├── topology.go       # Topology YAML parsing + validation
│   ├── veth.go           # veth pair creation/destruction via ip link
│   ├── ebpf.go           # eBPF program load, map management, stats
│   ├── ebpf_tc_linux.go  # TC attachment (Linux, uses vishvananda/netlink)
│   ├── ebpf_tc_other.go  # TC attachment stub (non-Linux, for cross-compile)
│   ├── flap.go           # Link flap (up/down) without veth teardown
│   ├── ui.go             # Web dashboard (single-page HTML + JS)
│   └── kvm_port_map.go   # KVM platform Ethernet→lane mapping
├── bpf/
│   └── patch_panel.c     # eBPF TC ingress program (bpf_redirect_peer)
├── sample.yml            # Reference topology (YAML format example)
├── Dockerfile            # Not here — see repo root Dockerfile.webmesh
└── go.mod                # module sonic-lab/webmesh
```

## REST API

Base URL: `http://localhost:8092` (host-mapped from container port 9090)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Web dashboard (auto-refreshes every 2s) |
| `/v1/status` | GET | Healthcheck: `{"healthy":true,"desired":N,"applied":N}` |
| `/v1/topology` | PUT | Replace desired topology (JSON body) |
| `/v1/links` | GET | List all links with state + per-link stats |
| `/v1/links/{key}` | GET | Inspect a single link |
| `/v1/links/{key}/up` | POST | Bring a link up |
| `/v1/links/{key}/down` | POST | Take a link down (flap) |

### Examples

```bash
# Check health
curl -s http://localhost:8092/v1/status | jq

# List all links with stats
curl -s http://localhost:8092/v1/links | jq

# Flap a link (take down, then bring back up)
curl -X POST http://localhost:8092/v1/links/spine1-leaf1/down
curl -X POST http://localhost:8092/v1/links/spine1-leaf1/up

# Replace the entire topology at runtime (JSON body; to push a YAML
# topology file, use `webmeshctl push` below)
curl -X PUT http://localhost:8092/v1/topology \
  -H "Content-Type: application/json" \
  -d @new-topology.json   # {"links":[{"endpoints":[{"device":...,"port":...}, ...]}, ...]}
```

## Topology File Format

```yaml
topology:
  name: my-lab
  switches:
    spine1:
      role: spine
      asn: 65000
      ports:
        Ethernet0: {}
        Ethernet4: {}
    leaf1:
      role: leaf
      asn: 65100
      ports:
        Ethernet0: {}
  links:
    - endpoints:
        - device: spine1-linux
          port: Ethernet0
        - device: leaf1-linux
          port: Ethernet0
```

- **`switches`** — map of switch name → config (role, ASN, ports)
- **`ports`** — map of port name → empty object (defines which ports exist)
- **`links`** — list of point-to-point connections between two device ports
- **`device`** — the Docker container name of the backing Linux container
  (convention: `<switchname>-linux`)

Ports use step-4 Ethernet naming (`Ethernet0`, `Ethernet4`, `Ethernet8`, …)
to match the KVM platform's 30-lane BTF map.

## Reconciler

The reconciler runs every 5 seconds and compares **desired** state (from the
topology file or API) against **applied** state (actual veth + eBPF wiring).
It creates missing links, removes extra links, and retries failed operations.

The container's healthcheck reports `healthy: true` only when
`desired count == applied count`, which gates SONiC container startup.

## webmeshctl CLI

```bash
cd webmesh
go build -o webmeshctl ./cmd/webmeshctl

# Wait for webmesh to become healthy (blocks until all links wired)
./webmeshctl wait

# Push a new topology file at runtime
./webmeshctl push -config my-topology.yml

# List all wired links
./webmeshctl links

# Also show each endpoint's webmesh-side interface
./webmeshctl links -v
```

Inside the webmesh container, each link endpoint is a veth named `wm-ethN`,
numbered in the order links are first created. `ip -d link show wm-ethN`
shows the endpoint it connects to as the interface alias, and
`GET /v1/links` returns the full map under `interfaces`.

## Build

The container is built via the multi-stage `Dockerfile.webmesh` at the repo
root (compiles the BPF program with clang, then builds the Go binary):

```bash
docker build -f Dockerfile.webmesh -t sonic-lab/webmesh:latest .
```

For local development (macOS cross-compile works for the Go binary, but not
the BPF object):

```bash
cd webmesh
go build ./...
go test ./...
```

## Container Requirements

- `privileged: true` (veth creation, netns operations)
- `pid: "host"` (access to network namespaces of other containers)
- `/var/run/docker.sock` mounted (for `nsenter` into switch namespaces)

## SRv6 Kernel Requirements

When using `-srv6` in tgen, the host kernel must have segment routing (seg6)
lwtunnel support enabled:

```
CONFIG_IPV6_SEG6_LWTUNNEL=y
CONFIG_IPV6_SEG6_HMAC=y      (optional, for HMAC-SRv6)
CONFIG_IPV6_SEG6_BPF=y       (optional, for BPF-based seg6)
```

**Colima** (the recommended macOS VM provider) ships with kernel 6.8.0 which
includes all three as built-in. No module loading or kernel rebuild is needed.

To verify on your host:

```bash
# Check if seg6 lwtunnel is available (should print "seg6")
ip -6 route add 2001:db8::/64 encap seg6 mode encap segs 2001:db8::1 dev lo 2>&1 | grep -v "Operation not supported" && echo "seg6 OK"
```

No special Docker image is required — the stock `docker-sonic-vs:latest`
includes iproute2 6.1.0 which fully supports seg6 lwtunnel route syntax.

### SRv6 Data Plane Requirement

The SRv6 data plane (seg6 encap/decap across container namespaces) is fully
functional in the Colima 6.8 HWE kernel environment. The critical requirement
is that `seg6_enabled=1` must be set on **all** interfaces (including
`Loopback0`) on every switch before seg6 routes are loaded.

The kernel's default for `seg6_enabled` is `0`, and setting it on the `all`
pseudo-interface does not propagate to already-created interfaces. The
`sonic_entry.sh` generated by tgen handles this automatically when `-srv6`
is specified — it sets `seg6_enabled=1` on every `Ethernet*` and `Loopback0`
interface before executing the seg6 routes script.

Without `seg6_enabled=1` on the receiving side, the kernel silently drops
seg6-encapsulated packets (no counter increment, no error). This was the root
cause of earlier "silent drop" observations — it is not a kernel bug.

## eBPF Program

The BPF program (`bpf/patch_panel.c`) is a TC ingress classifier that:

1. Reads the packet's ingress ifindex
2. Looks up the peer ifindex in `link_map` (BPF HASH map)
3. If found, calls `bpf_redirect_peer(peer_ifidx)` to forward the packet
4. Increments per-link packet/byte counters in `stats_map`

The map is updated at runtime by the Go daemon whenever links are created,
destroyed, or rewired — no program reload needed.

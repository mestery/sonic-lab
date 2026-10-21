# sonic-lab
[![Test Docker Compose](https://github.com/mestery/sonic-lab/actions/workflows/test-docker-compose.yml/badge.svg)](https://github.com/mestery/sonic-lab/actions/workflows/test-docker-compose.yml)

This repository contains a SONiC virtual lab built using `docker-sonic-vs`
container images. The lab builds a simple spine–leaf topology using an
eBPF-powered **webmesh** patch panel container for dynamic, API-driven
network wiring between switches.

## Repository Layout

```
├── webmesh/          # Go module: sonic-lab/webmesh
│   ├── cmd/webmesh/      # Server entrypoint
│   ├── cmd/webmeshctl/   # Operator CLI (wait, push, links)
│   ├── internal/         # Core packages (api, reconciler, veth, ebpf, ...)
│   ├── bpf/              # eBPF C program (patch_panel.c)
│   └── sample.yml        # Reference topology (YAML format example)
├── tgen/             # Go module: sonic-lab/tgen
│   ├── main.go             # CLI entrypoint
│   ├── README.md           # Full tgen documentation
│   └── internal/           # topo (generator), writer (file output)
├── example/          # Working 3-switch spine-leaf topology
│   ├── docker-compose.yml
│   ├── topology.yml
│   ├── init_linux.sh
│   ├── sonic_entry.sh
│   ├── start.sh
│   └── tmp/vnet_ready/
├── Dockerfile.webmesh  # Multi-stage build for the webmesh container
├── Dockerfile.debian   # Backing Linux image (iproute2, util-linux, docker.io)
├── start.sh            # Convenience script to start the example lab
└── docs/webmesh-design.md
```

## Topology

```
        +--------+
        | Spine  |
        +--------+
        ||      ||
   link1||      ||link2
        ||      ||
+--------+      +--------+
| Leaf1  |      | Leaf2  |
+--------+      +--------+
```

All inter-switch links are managed by the **webmesh** container, which uses
eBPF TC programs (`bpf_redirect_peer`) to wire veth pairs between switch
network namespaces. Links can be dynamically created, destroyed, and
flapped at runtime via a REST API.

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                     webmesh container                            │
│                                                                  │
│  ┌────────────┐    ┌─────────────────────────────────────────┐   │
│  │  API Server │    │         eBPF Patch Panel               │   │
│  │  (Go/HTTP) │◄──►│  TC ingress on wm-* veth endpoints     │   │
│  └────────────┘    │  bpf_redirect_peer → remote switch      │   │
│                     └─────────────────────────────────────────┘   │
│                                                                  │
│  netns: webmesh (holds wm-* veth endpoints)                      │
└──────────────────────────────────────────────────────────────────┘
         │ veth                    │ veth
         ▼                         ▼
┌──────────────┐           ┌──────────────┐
│  spine1 netns │           │  leaf1 netns │
│  (SONiC VS)  │           │  (SONiC VS)  │
└──────────────┘           └──────────────┘
```

## Host Requirements

This lab is intended to run on macOS using **Colima**, or on Linux. Prebuilt
`docker-sonic-vs` images are available for both amd64 and arm64 — see
[Getting the `docker-sonic-vs` image](#getting-the-docker-sonic-vs-image).

- Docker (via Colima)
- At least 8 GB RAM allocated to Colima
- `clang` is NOT required on the host (BPF is compiled inside the container)

## Getting the `docker-sonic-vs` image

The example topology uses the `docker-sonic-vs:latest` image. Which one you
fetch depends on your CPU architecture:

### Apple Silicon Macs (arm64)

If you're on an M-series (arm64) Mac, pull the prebuilt arm64 image from
GHCR and tag it so it matches the name the compose files reference:

```bash
docker pull ghcr.io/mestery/docker-sonic-vs:latest
docker tag ghcr.io/mestery/docker-sonic-vs:latest docker-sonic-vs:latest
```

> The arm64 image runs natively on Apple Silicon, so you don't need Rosetta
> for the SONiC containers.

### x86_64 (amd64)

If you're on Linux or an Intel machine (or emulating amd64), download the
image tarball from Azure Artifacts and load it into Docker:

```bash
wget -O /tmp/docker-sonic-vs.gz "https://artprodcus3.artifacts.visualstudio.com/Af91412a5-a906-4990-9d7c-f697b81fc04d/be1b070f-be15-4154-aade-b1d3bfb17054/_apis/artifact/cGlwZWxpbmVhcnRpZmFjdDovL21zc29uaWMvcHJvamVjdElkL2JlMWIwNzBmLWJlMTUtNDE1NC1hYWRlLWIxZDNiZmIxNzA1NC9idWlsZElkLzEyNTA3NjcvYXJ0aWZhY3ROYW1lL3NvbmljLWJ1aWxkaW1hZ2UudnM1/content?format=file&subpath=/target/docker-sonic-vs.gz"
gunzip -c /tmp/docker-sonic-vs.gz | docker load
```

Or let the convenience script do this for you (it fetches the amd64 image):

```bash
./start.sh -l
```

## Quick Start

### 1. Start Colima

```bash
colima start --vm-type vz --vz-rosetta --cpu 4 --memory 8 --mount-type virtiofs
```

### 2. Build images

```bash
docker compose -f example/docker-compose.yml build
```

Or use the convenience script (also pulls the SONiC image on first run):

```bash
./start.sh -l
```

### 3. Start the lab

```bash
./start.sh
```

Or manually:

```bash
docker compose -f example/docker-compose.yml up -d
```

The webmesh container starts first and performs a healthcheck. SONiC
containers are gated on `webmesh` being healthy (all desired links
applied). Once healthy, SONiC boots and attaches to the pre-wired
interfaces.

### 4. Wait and verify

SONiC takes a few minutes to boot. Watch progress:

```bash
docker logs -f sonic-spine1
```

Once SONiC is up, verify connectivity:

```bash
# Check that interfaces are up
docker exec sonic-spine1 ip link show | grep -E "Ethernet|UP"

# Ping between switches (replace with actual loopback IPs from your topology)
docker exec sonic-spine1 ping -c 3 10.0.0.2
```

## Webmesh Web UI

The webmesh container serves a dashboard at:

```
http://localhost:8092
```

This shows all links, their state (up/down), and packet/byte stats from
the eBPF program. The page auto-refreshes every 2 seconds.

## Webmesh REST API

Base URL: `http://localhost:8092`

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/topology` | GET | Current desired topology |
| `/v1/topology` | PUT | Replace desired topology (YAML or JSON) |
| `/v1/links` | GET | List all links with state + stats |
| `/v1/links/{key}` | GET | Inspect a single link |
| `/v1/links/{key}/up` | POST | Bring a link up |
| `/v1/links/{key}/down` | POST | Take a link down (flap) |
| `/v1/stats` | GET | Aggregate eBPF stats |
| `/v1/health` | GET | Healthcheck (desired == applied) |

### Example: Flap a link

```bash
# Take the spine1-leaf1 link down
curl -X POST http://localhost:8092/v1/links/spine1-leaf1/down

# Verify SONiC detects the link-down
docker exec sonic-spine1 show interfaces status

# Bring it back up
curl -X POST http://localhost:8092/v1/links/spine1-leaf1/up
```

### Example: View link stats

```bash
curl -s http://localhost:8092/v1/stats | python3 -m json.tool
```

## Topology File

The desired topology is defined in a YAML file (e.g., `example/topology.yml`)
and mounted into the webmesh container at `/etc/webmesh/topology.yml`. The
file watcher polls for changes (mtime) and the reconciler applies
differences every 5 seconds.

To change the topology at runtime, edit the mounted file (or use the PUT
API) and the reconciler will apply changes within ~5 seconds.

## Generating New Topologies with tgen

The `tgen` tool generates a complete `docker-compose.yml` + `topology.yml`
+ supporting scripts from command-line flags — no input file needed:

```bash
cd tgen
go run . -s 1 -l 4 -o ../mylab/     # 1 spine, 4 leaves
go run . -s 2 -l 6 -o ../dc/        # 2 spines, 6 leaves
go run . -m -n 5 -o ../mesh/        # Full mesh of 5 nodes
go run . -r -n 8 -o ../ring/        # Ring of 8 nodes
```

> **Note:** The output directory must be a direct child of the repo root
> (e.g., `../mylab/`) because the generated compose file uses
> `context: ..` to reference `Dockerfile.webmesh` and `Dockerfile.debian`.

See [`tgen/README.md`](tgen/README.md) for full documentation.

> **Note:** The output directory must be a direct child of the repo root
> (e.g., `example/`, `mylab/`) because the generated compose file uses
> relative paths (`context: ..`) to reference `Dockerfile.webmesh` and
> `Dockerfile.debian`.

## webmeshctl CLI

The `webmeshctl` tool provides convenience commands for interacting with a
running webmesh instance:

```bash
cd webmesh
go build -o webmeshctl ./cmd/webmeshctl

# Wait for webmesh to become healthy
./webmeshctl wait

# Push a new topology at runtime
./webmeshctl push -config my-topology.yml

# List all wired links
./webmeshctl links
```

## Logging Into the Switches

```bash
docker exec -it sonic-spine1 bash
docker exec -it sonic-leaf1 bash
docker exec -it sonic-leaf2 bash
```

## Cleaning Up

```bash
docker compose -f example/docker-compose.yml down
```

To also remove the webmesh image:

```bash
docker compose -f example/docker-compose.yml down && docker rmi sonic-lab/webmesh:latest
```

## How the Networking Works

### eBPF Patch Panel

Each link uses **two veth pairs**:

```
Switch A netns          webmesh netns              Switch B netns
┌────────────┐      ┌─────────────────────┐      ┌────────────┐
│ EthernetX  │◄──┐  │  wm-a-X  ═══════  wm-b-Y │  ──┐EthernetY│
│  (TAP)     │   └──│  (tc ingress)  (tc ingress)│  ──┘ (TAP)  │
└────────────┘      └─────────────────────┘      └────────────┘
```

The eBPF program is attached to the TC ingress of both webmesh-side
endpoints. On packet arrival, it looks up the ingress ifindex in
`link_map` to find the peer ifindex, then calls
`bpf_redirect_peer(peer_ifidx)` which delivers the packet directly to
the veth peer (i.e., the switch's Ethernet interface) with minimal
overhead — no L2 switch, no routing, just a redirect.

### Why This Design

* Zero-copy packet forwarding via `bpf_redirect_peer`
* Dynamically rewirable at runtime (update map, no interface changes)
* Link flapping without tearing down veths
* Per-link packet/byte statistics for observability
* SONiC is unaware of the mechanism — it sees normal Ethernet link state

## Notes

* All veth interfaces are created and managed by webmesh — do not
  modify them manually
* The webmesh container requires `privileged: true` and access to
  `/var/run/docker.sock` (for namespace operations)
* The reconciler retries every 5 seconds until desired state is applied
* State is persisted to `/var/run/webmesh/state.json` inside the container

## References

- [SONiC Documentation](https://github.com/sonic-net/SONiC)
- [webmesh Design Document](docs/webmesh-design.md)
- [libbpf Documentation](https://github.com/libbpf/libbpf)
- [cilium/ebpf](https://github.com/cilium/ebpf)
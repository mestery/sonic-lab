# sonic-lab
[![Test Docker Compose](https://github.com/mestery/sonic-lab/actions/workflows/test-docker-compose.yml/badge.svg)](https://github.com/mestery/sonic-lab/actions/workflows/test-docker-compose.yml)

This repository contains a SONiC virtual lab built using `docker-sonic-vs`
container images. The lab builds a simple spine–leaf topology using Linux
**veth pairs** to provide real packet connectivity between switches.

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

Each link is implemented using a Linux veth pair.

## Host Requirements

This lab is intended to run on macOS using **Colima** with amd64 emulation
(Rosetta), since SONiC images are amd64-only.

## Start Colima

Start colima with amd64 support using rosetta:

```bash
colima start --vm-type vz --vz-rosetta --cpu 4 --memory 8 --mount-type virtiofs
```

## Download docker-sonic-vs

If you need the docker-sonic-vs image:

```
./start.sh -l
```

## Lab Startup Overview

The lab must be started in three phases:

1. Start base Linux containers
2. Create the veth topology
3. Start SONiC containers

This ordering is mandatory.

### Phase 1: Start Base Linux containers

The base Linux containers own the network namespaces where veth interfaces
are created.

```bash
docker compose up -d spine1-linux leaf1-linux leaf2-linux
```

Wait until all containers are running. You can use `docker logs -f spine1-sonic`
to watch for  the logs.

### Phase 2: Create the Virtual network

Run the veth wiring script inside the Colima VM (requires root):

```bash
colima ssh sudo ./create_vnet.ssh
```

This script:

* Creates veth pairs
* Moves each endpoint into the correct container namespace
* Brings all interfaces up

The script is idempotent and safe to re-run.

### Phase 3: Start SONiC containers

Phase 3: Start SONiC containers

```bash
docker compose up -docker
```

SONiC will attach to the pre-existing interfaces.

## Logging Into the switches

Spine:

```bash
docker exec -it sonic-spine bash
```

Leafs:

```bash
docker exec -it sonic-leaf1 bash
docker exec -it sonic-leaf2 bash
```

## Cleaning Up the lab

```bash
docker compose down
```

## How the Networking Works

SONiC Ethernet ports must never be created, renamed, or deleted manually.

### SONiC Ethernet Ports

Inside each `docker-sonic-vs` container, SONiC creates Ethernet interfaces
such as:

```bash
Ethernet0
Ethernet4
Ethernet8
Ethernet12
```

These interfaces are backed by TAP devices created internally by SONiC.

### Linux Veth Transport Layer

Actual connectivity between switches is provided by Linux veth pairs created
outside of SONiC.

Each veth endpoint lives in a Linux container namespace and is named eth1,
eth2, etc.

Example mapping:

```
| Link               | Spine iface | Leaf iface |
|--------------------|-------------|
| Spine <-> Leaf1 #1 | eth1        | eth1       |
| Spine <-> Leaf1 #2 | eth2        | eth2       |
| Spine <-> Leaf2 #1 | eth3        | eth1       |
| Spine <-> Leaf2 #2 | eth4        | eth2       |
| ...                | ...         | ...        |
```

### Traffic Flow

```scss
SONiC EthernetX
   ↕ (TAP)
Linux ethN
   ↔ (veth)
Linux ethN
   ↕ (TAP)
SONiC EthernetY
```

SONiC is unaware that veths exist — it simply sees link state and packets.

## Why This Design Is Used

* Avoids overwriting SONiC-managed interfaces
* Matches real hardware behavior
* Allows real packet forwarding
* Makes topology deterministic and debuggable

## Notes

* All veth interfaces must exist before SONiC starts
* Reusing EthernetX names for veths will break the lab
* `create_vnet.sh` must be run as root
* The wiring script is safe to re-run

## References

[SONiC Documentation](https://github.com/sonic-net/SONiC)

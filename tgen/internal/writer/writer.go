/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

// Package writer generates all output files for a topology:
// docker-compose.yml, topology.yml, and supporting shell scripts.
package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"sonic-lab/tgen/internal/topo"
)

// sharedRT is the single route-target used by every SRv6 overlay node for both
// import and export. Using one shared RT (instead of a per-ASN RT) is what lets
// all sites import each other's VRF routes — a full-mesh L3VPN where every site
// can reach every other site. The route-distinguisher (below) stays per-ASN so
// each site's routes remain distinct in the global VPNv4 RIB.
const sharedRT = "65000:100"

// Writer generates output files into a target directory.
type Writer struct {
	OutputDir string
	Topology  *topo.Topology
}

// New creates a Writer for the given topology and output directory.
func New(outputDir string, t *topo.Topology) *Writer {
	return &Writer{
		OutputDir: outputDir,
		Topology:  t,
	}
}

// Run writes all output files.
func (w *Writer) Run() error {
	if err := os.MkdirAll(filepath.Join(w.OutputDir, "tmp", "vnet_ready"), 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if err := w.writeCompose(); err != nil {
		return fmt.Errorf("writing docker-compose.yml: %w", err)
	}
	if err := w.writeTopologyYAML(); err != nil {
		return fmt.Errorf("writing topology.yml: %w", err)
	}
	if err := w.writeInitScript(); err != nil {
		return fmt.Errorf("writing init_linux.sh: %w", err)
	}
	if err := w.writeEntryScript(); err != nil {
		return fmt.Errorf("writing sonic_entry.sh: %w", err)
	}
	if err := w.writeFRRConfigs(); err != nil {
		return fmt.Errorf("writing FRR configs: %w", err)
	}
	if w.Topology.SRv6 {
		if err := w.writeSRv6Routes(); err != nil {
			return fmt.Errorf("writing SRv6 routes: %w", err)
		}
	}
	if err := w.writeStartScript(); err != nil {
		return fmt.Errorf("writing start.sh: %w", err)
	}

	return nil
}

// --- docker-compose.yml ---

func (w *Writer) writeCompose() error {
	var sb strings.Builder

	// Anchors
	sb.WriteString("x-sonic-common: &sonic-common\n")
	sb.WriteString("  image: docker-sonic-vs:latest\n")
	sb.WriteString("  platform: linux/arm64\n")
	sb.WriteString("  privileged: true\n")
	sb.WriteString("  tty: true\n")
	sb.WriteString("  shm_size: \"2gb\"\n")
	sb.WriteString("  security_opt:\n")
	sb.WriteString("    - seccomp=unconfined\n\n")

	// The debian and host anchors carry only the image name, not build:.
	// Each image is built by exactly one service (the first that uses it);
	// if every service built the same tag, compose would run the builds in
	// parallel and they would race to create it.
	sb.WriteString("x-debian-common: &debian-common\n")
	sb.WriteString("  image: sonic-lab/debian:latest\n")
	sb.WriteString("  platform: linux/arm64\n")
	sb.WriteString("  privileged: true\n")
	sb.WriteString("  tty: true\n")
	sb.WriteString("  volumes:\n")
	sb.WriteString("    - ./init_linux.sh:/init_linux.sh:ro\n")
	sb.WriteString("    - /var/run/docker.sock:/var/run/docker.sock\n")
	sb.WriteString("    - ./tmp/vnet_ready:/tmp/vnet_ready:rw\n")
	sb.WriteString("  shm_size: \"2gb\"\n\n")

	sb.WriteString("x-host-common: &host-common\n")
	sb.WriteString("  image: sonic-lab/host:latest\n")
	sb.WriteString("  platform: linux/arm64\n")
	sb.WriteString("  privileged: true\n")
	sb.WriteString("  tty: true\n\n")

	sb.WriteString("services:\n")

	// Webmesh
	sb.WriteString("  #################################\n")
	sb.WriteString("  # Webmesh patch panel container #\n")
	sb.WriteString("  #################################\n\n")
	sb.WriteString("  webmesh:\n")
	sb.WriteString("    build:\n")
	sb.WriteString("      context: ..\n")
	sb.WriteString("      dockerfile: Dockerfile.webmesh\n")
	sb.WriteString("    image: sonic-lab/webmesh:latest\n")
	sb.WriteString("    platform: linux/arm64\n")
	sb.WriteString("    privileged: true\n")
	sb.WriteString("    pid: \"host\"\n")
	sb.WriteString("    container_name: webmesh\n")
	sb.WriteString("    ports:\n")
	sb.WriteString("      - \"8092:9090\"\n")
	sb.WriteString("    volumes:\n")
	sb.WriteString("      - ./topology.yml:/etc/webmesh/topology.yml:ro\n")
	sb.WriteString("      - /var/run/docker.sock:/var/run/docker.sock:ro\n")
	sb.WriteString("      - ./tmp/vnet_ready:/tmp/vnet_ready:rw\n")
	sb.WriteString("    healthcheck:\n")
	sb.WriteString("      test: [\"CMD-SHELL\", \"curl -sf http://localhost:9090/v1/status | grep -q '\\\"healthy\\\":true'\"]\n")
	sb.WriteString("      interval: 2s\n")
	sb.WriteString("      timeout: 3s\n")
	sb.WriteString("      retries: 60\n\n")

	// Backing Linux containers
	sb.WriteString("  ###########################\n")
	sb.WriteString("  # Backing Linux containers #\n")
	sb.WriteString("  ###########################\n\n")

	for i, sw := range w.Topology.Switches {
		dev := w.Topology.DeviceNetnsName(sw.Name)
		sb.WriteString(fmt.Sprintf("  %s:\n", dev))
		sb.WriteString("    <<: *debian-common\n")
		if i == 0 {
			writeBuild(&sb, "Dockerfile.debian")
		}
		sb.WriteString(fmt.Sprintf("    container_name: %s\n", dev))
		sb.WriteString(fmt.Sprintf("    command: [\"bash\", \"/init_linux.sh\", \"%s\"]\n\n", dev))
	}

	// SONiC containers
	sb.WriteString("  ###########################\n")
	sb.WriteString("  # SONiC VS containers      #\n")
	sb.WriteString("  ###########################\n\n")

	for i, sw := range w.Topology.Switches {
		name := sw.Name
		dev := w.Topology.DeviceNetnsName(name)
		sb.WriteString(fmt.Sprintf("  %s:\n", name))
		sb.WriteString("    <<: *sonic-common\n")
		sb.WriteString(fmt.Sprintf("    container_name: sonic-%s\n", name))
		sb.WriteString("    depends_on:\n")
		sb.WriteString("      webmesh:\n")
		sb.WriteString("        condition: service_healthy\n")
		sb.WriteString(fmt.Sprintf("      %s:\n", dev))
		sb.WriteString("        condition: service_started\n")
		sb.WriteString(fmt.Sprintf("    network_mode: \"service:%s\"\n", dev))
		sb.WriteString("    volumes:\n")
		sb.WriteString("      - ./tmp/vnet_ready:/tmp/vnet_ready:ro\n")
		sb.WriteString(fmt.Sprintf("      - /var/run/redis-vs/%s:/var/run/redis\n", name))
		sb.WriteString("      - ./sonic_entry.sh:/sonic_entry.sh:ro\n")
		sb.WriteString(fmt.Sprintf("      - ./frr/%s.conf:/tmp/frr.conf:ro\n", name))
		if w.Topology.SRv6 {
			sb.WriteString(fmt.Sprintf("      - ./srv6/%s.routes:/srv6.routes:ro\n", name))
		}
		sb.WriteString(fmt.Sprintf("    entrypoint: [\"/bin/bash\", \"/sonic_entry.sh\", \"%s\", \"%s\", \"%s\"]\n", dev, sw.VtepAddr, sw.OverlayIPv4))
		if i < len(w.Topology.Switches)-1 {
			sb.WriteString("\n")
		}
	}

	// Host containers
	if len(w.Topology.Hosts) > 0 {
		sb.WriteString("\n  ########################\n")
		sb.WriteString("  # Simulated host containers #\n")
		sb.WriteString("  ########################\n\n")

		for i, h := range w.Topology.Hosts {
			sb.WriteString(fmt.Sprintf("  %s:\n", h.Name))
			sb.WriteString("    <<: *host-common\n")
			if i == 0 {
				writeBuild(&sb, "Dockerfile.host")
			}
			sb.WriteString(fmt.Sprintf("    container_name: %s\n", h.Name))
			if i < len(w.Topology.Hosts)-1 {
				sb.WriteString("\n")
			}
		}
	}

	return os.WriteFile(filepath.Join(w.OutputDir, "docker-compose.yml"), []byte(sb.String()), 0o644)
}

// writeBuild emits a service-level build: block for a Dockerfile at the repo root.
func writeBuild(sb *strings.Builder, dockerfile string) {
	sb.WriteString("    build:\n")
	sb.WriteString("      context: ..\n")
	sb.WriteString(fmt.Sprintf("      dockerfile: %s\n", dockerfile))
}

// --- topology.yml (consumed by webmesh) ---

// topologyYAML is the YAML structure that webmesh expects.
type topologyYAML struct {
	Topology struct {
		Name     string                `yaml:"name"`
		Switches map[string]switchYAML `yaml:"switches"`
		Hosts    map[string]hostYAML   `yaml:"hosts,omitempty"`
		Links    []linkYAML            `yaml:"links"`
	} `yaml:"topology"`
}

type switchYAML struct {
	Role  string                 `yaml:"role"`
	ASN   int                    `yaml:"asn"`
	Ports map[string]interface{} `yaml:"ports"`
}

type hostYAML struct {
	Ports map[string]interface{} `yaml:"ports"`
}

type linkYAML struct {
	Endpoints []endpointYAML `yaml:"endpoints"`
}

type endpointYAML struct {
	Device string `yaml:"device"`
	Port   string `yaml:"port"`
}

func (w *Writer) writeTopologyYAML() error {
	var ty topologyYAML
	ty.Topology.Name = w.Topology.Name
	ty.Topology.Switches = make(map[string]switchYAML, len(w.Topology.Switches))

	for _, sw := range w.Topology.Switches {
		sy := switchYAML{
			Role:  sw.Role,
			ASN:   sw.ASN,
			Ports: make(map[string]interface{}, len(sw.Ports)),
		}
		for _, p := range sw.Ports {
			sy.Ports[p] = map[string]interface{}{}
		}
		ty.Topology.Switches[sw.Name] = sy
	}

	if len(w.Topology.Hosts) > 0 {
		ty.Topology.Hosts = make(map[string]hostYAML, len(w.Topology.Hosts))
		for _, h := range w.Topology.Hosts {
			hyp := make(map[string]interface{}, len(h.Ports))
			for _, p := range h.Ports {
				hyp[p] = map[string]interface{}{}
			}
			ty.Topology.Hosts[h.Name] = hostYAML{Ports: hyp}
		}
	}

	for _, l := range w.Topology.Links {
		ty.Topology.Links = append(ty.Topology.Links, linkYAML{
			Endpoints: []endpointYAML{
				{Device: l.A.Device, Port: l.A.Port},
				{Device: l.B.Device, Port: l.B.Port},
			},
		})
	}

	data, err := yaml.Marshal(&ty)
	if err != nil {
		return fmt.Errorf("marshaling topology: %w", err)
	}

	return os.WriteFile(filepath.Join(w.OutputDir, "topology.yml"), data, 0o644)
}

// --- Shell scripts ---

func (w *Writer) writeInitScript() error {
	data := []byte(`#!/usr/bin/env bash
	set -e
	echo "Initializing with arguments $@"
	touch "/tmp/vnet_ready/$1"
	echo "vnet ready"
	sleep infinity
	`)
	return os.WriteFile(filepath.Join(w.OutputDir, "init_linux.sh"), data, 0o755)
}

func (w *Writer) writeEntryScript() error {
	data := []byte(`#!/usr/bin/env bash
	set -e

	SWITCH="$1"
	VTEP="${2:-}"
	OVERLAY_V4="${3:-}"

	echo "Creating docker override"
	touch /usr/bin/docker
	chmod +x /usr/bin/docker

	echo "Waiting for vnet file /tmp/vnet_ready/$SWITCH"

	while [ ! -f "/tmp/vnet_ready/$SWITCH" ]; do
	sleep 3
	done

	echo "vnet ready"

	# Disable IPv6 on the raw data-plane veths (eth1, eth2, ...). Each
	# front-panel port is a veth (ethN, wired by webmesh) that syncd's
	# vslib relays to a tap (EthernetN), and only the tap should run an
	# IP stack. With IPv6 left on ethN, the kernel accepts every packet
	# for a global address (e.g. Loopback0) twice — once straight off
	# ethN and again from vslib's copy on EthernetN — so each echo is
	# answered twice per hop and duplicates multiply across hops. It
	# also installs a bogus RA-learned default route via ethN. vslib
	# uses raw sockets on ethN, which do not need IPv6. eth0 is the
	# Docker management interface and is left alone. See issue #45.
	for dev in /sys/class/net/eth*; do
	iface=$(basename "$dev")
	[ "$iface" = "eth0" ] && continue
	sysctl -qw "net.ipv6.conf.$iface.disable_ipv6=1" 2>/dev/null || echo "WARNING: could not disable IPv6 on $iface"
	done
	echo "Disabled IPv6 on data-plane veths"

	echo "Starting SONiC..."

	# Start supervisord in the background so we can configure
	# the VTEP interface and FRR after SONiC's daemons are up.
	/usr/local/bin/supervisord -n &
	SUPERVISOR_PID=$!

	if [ -n "$VTEP" ]; then
	# Wait for zebra (started by SONiC's start.sh).
	echo "Waiting for zebra to start..."
	for i in $(seq 1 90); do
	if supervisorctl status zebra 2>/dev/null | grep -q "RUNNING"; then
	echo "zebra is running"
	break
	fi
	sleep 2
	done

	# Create Loopback0 (dummy type) with the VTEP address.
	# NOTE: SONiC's ip binary does not support "type loopback";
	# "type dummy" is the correct type for a VTEP interface.
	ip link add Loopback0 type dummy 2>/dev/null || true
	ip addr replace "$VTEP" dev Loopback0
	ip link set Loopback0 up
	echo "Loopback0 created with VTEP address $VTEP"

	# Set admin up in SONiC CONFIG_DB for all Ethernet interfaces.
	# This tells orchagent to program the port; required for the
	# interface to show as "up" in "show interfaces status".
	for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep '^Ethernet'); do
	config interface startup "$iface" 2>/dev/null || true
	done
	echo "Set admin up for Ethernet interfaces"

	# Wait for orchagent to process the admin-up requests.
	sleep 5

	# Bring up all Ethernet* interfaces in the kernel BEFORE starting
	# bgpd. Zebra must see them as UP before bgpd connects, otherwise
	# bgpd crashes when reading the config with unnumbered neighbors.
	for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep '^Ethernet'); do
	ip link set "$iface" up 2>/dev/null || true
	done
	echo "Brought up Ethernet interfaces"

	# Give zebra time to process the interface state changes via netlink
	# before bgpd connects and requests the interface list.
	sleep 3

	# SONiC's start.sh does not start bgpd — start it explicitly now
	# that all interfaces are up and registered with zebra.
	supervisorctl start bgpd 2>/dev/null || true
	echo "Waiting for bgpd to start..."
	for i in $(seq 1 30); do
	if supervisorctl status bgpd 2>/dev/null | grep -q "RUNNING"; then
	echo "bgpd is running"
	break
	fi
	sleep 2
	done

	# Load FRR config explicitly. Non-fatal: if it fails the container
	# should stay up so we can debug.
	echo "Loading FRR configuration..."
	vtysh -f /tmp/frr.conf || echo "WARNING: vtysh -f returned non-zero"
	echo "FRR configuration loaded"

	# Enable IPv6 forwarding on every SONiC container, unconditionally.
	# Multi-hop topologies (ring, spine-leaf) require intermediate nodes
	# to transit SRv6/underlay packets; the kernel default (0) silently
	# drops transit traffic. This must stay OUTSIDE the /srv6.routes
	# guard below: spines receive route files but the forwarding
	# sysctls must be set regardless, so nesting them inside the
	# guard would leave transit nodes blackholing cross-leaf traffic.
	# Harmless on nodes that never forward.
	sysctl -w net.ipv6.conf.all.forwarding=1 2>/dev/null || true
	sysctl -w net.ipv6.conf.default.forwarding=1 2>/dev/null || true
	for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep -E '^(Ethernet|Loopback)'); do
	echo 1 > "/proc/sys/net/ipv6/conf/$iface/forwarding" 2>/dev/null || true
	done
	echo "Enabled IPv6 forwarding on all interfaces"

	# If SRv6 routes file is present, enable seg6 on all interfaces and
	# execute the routes script to program kernel seg6 routes.
	if [ -f /srv6.routes ]; then
	# Enable SRv6 segment routing. The kernel default for seg6_enabled
	# is 0. Setting it on "all"/"default" covers future interfaces but
	# does NOT propagate to already-created ones, so we also set it
	# explicitly on each existing interface below.
	sysctl -w net.ipv6.conf.all.seg6_enabled=1 2>/dev/null || true
	sysctl -w net.ipv6.conf.default.seg6_enabled=1 2>/dev/null || true
	for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep -E '^(Ethernet|Loopback)'); do
	echo 1 > "/proc/sys/net/ipv6/conf/$iface/seg6_enabled" 2>/dev/null || true
	done
	echo "Enabled seg6_enabled=1 on all interfaces"

	echo "Loading SRv6 overlay routes..."
	if bash /srv6.routes; then
	echo "SRv6 overlay routes loaded"
	else
	echo "ERROR: SRv6 overlay routes NOT fully installed (see warnings above)"
	fi
	fi

	# 1. First, create overlay IPv4 VLAN interface in VRF (SRv6 leaves only).
	if [ -n "$OVERLAY_V4" ]; then
	config vrf add Vrf100 2>/dev/null || true

	# WAIT LOOP: Wait for the SONiC backend daemon to create the Linux kernel VRF device
	echo "Waiting for Linux kernel to instantiate Vrf100..."
	for i in {1..10}; do
	if ip link show dev Vrf100 >/dev/null 2>&1 || ip link show dev vrf100 >/dev/null 2>&1; then
	break
	fi
	sleep 1
	done

	# Clean up any stale loopback config from previous attempts
	config interface vrf unbind Loopback100 2>/dev/null || true
	config loopback del Loopback100 2>/dev/null || true

	# Provision a VLAN interface as the overlay loopback
	config vlan add 100 2>/dev/null || true

	# WAIT LOOP: Wait for the SONiC backend daemon to create the Linux kernel Bridge interface
	echo "Waiting for Linux kernel to instantiate Vlan100..."
	for i in {1..10}; do
	if ip link show dev Vlan100 >/dev/null 2>&1; then
	break
	fi
	sleep 1
	done

	config interface vrf bind Vlan100 Vrf100 2>/dev/null || true
	config interface ip add Vlan100 "$OVERLAY_V4" 2>/dev/null || true
	config interface startup Vlan100 2>/dev/null || true

	# =====================================================================
	# CRITICAL CONTAINER FIX: Force the IP and link state directly into kernel 
	# to bypass SONiC database sync dropping empty VLAN mappings.
	# =====================================================================
	ip addr add "$OVERLAY_V4" dev Vlan100 2>/dev/null || true
	ip link set dev Vlan100 master Vrf100 2>/dev/null || ip link set dev Vlan100 master vrf100 2>/dev/null || true
	ip link set dev Vlan100 up 2>/dev/null || true
	# =====================================================================

	echo "Created Vlan100 in Vrf100 with $OVERLAY_V4"
	config save -y 2>/dev/null || true
	fi
	fi

	# Fix lldpd socket race condition: lldpd may fail to create its
	# Unix socket if /run is not fully ready at supervisord startup.
	# If the socket is missing, restart lldpd to force socket creation.
	if ! [ -S /run/lldpd.socket ]; then
	echo "lldpd socket missing, restarting lldpd..."
	supervisorctl restart lldpd 2>/dev/null || true
	sleep 3
	fi

	# Fix 'show lldp table': the lldpshow script expects a separate 'lldp'
	# container (standard multi-ASIC design). In VS mode, lldpd runs in the
	# main container, so patch the script to call lldpctl directly.
	sed -i "s/\['sudo', 'docker', 'exec', '-i', 'lldp{}'.format(self\.lldp_instance\[lldp_instace_num\]), 'lldpctl'\]/['lldpctl']/" /usr/local/bin/lldpshow 2>/dev/null || true

	# Keep the container alive by waiting on supervisord.
	wait $SUPERVISOR_PID
	`)
	return os.WriteFile(filepath.Join(w.OutputDir, "sonic_entry.sh"), data, 0o755)
}

// --- FRR configuration ---

// kernelPortName returns the kernel interface name for a SONiC port.
// In SONiC VS, the kernel interface name is the same as the port name
// (Ethernet0, Ethernet4, Ethernet8, etc.).
func kernelPortName(portName string) string {
	return portName
}

// neighborInfo describes a BGP neighbor relationship for one local port.
type neighborInfo struct {
	localPort string
	peerASN   int
}

// switchNeighbors returns the list of switch-to-switch BGP neighbors
// for the given switch, excluding host connections.
func (w *Writer) switchNeighbors(sw topo.Switch) []neighborInfo {
	var result []neighborInfo
	swDevice := w.Topology.DeviceNetnsName(sw.Name)

	for _, link := range w.Topology.Links {
		var local, peer *topo.Endpoint
		if link.A.Device == swDevice {
			local, peer = &link.A, &link.B
		} else if link.B.Device == swDevice {
			local, peer = &link.B, &link.A
		} else {
			continue
		}

		// Only include links to other switches, not hosts.
		peerSwitch, ok := w.findSwitchByDevice(peer.Device)
		if !ok {
			continue
		}

		result = append(result, neighborInfo{
			localPort: local.Port,
			peerASN:   peerSwitch.ASN,
		})
	}

	return result
}

// findSwitchByDevice returns the switch whose backing netns container is dev.
// Host endpoints don't match any switch.
func (w *Writer) findSwitchByDevice(dev string) (*topo.Switch, bool) {
	for i := range w.Topology.Switches {
		if w.Topology.DeviceNetnsName(w.Topology.Switches[i].Name) == dev {
			return &w.Topology.Switches[i], true
		}
	}
	return nil, false
}

// generateFRRConfig produces a complete FRR configuration for one switch.
// Spines get SRv6 locator + BGP SRv6 sections but no VRF or per-VRF BGP.
// Leaves and nodes (when SRv6 is enabled) get the full config: VRF, SRv6
// locator with End SID, L3VPN address family, and a per-VRF BGP instance.
func (w *Writer) generateFRRConfig(sw topo.Switch) string {
	neighbors := w.switchNeighbors(sw)
	isSpine := sw.Role == "spine"
	srv6 := w.Topology.SRv6

	var sb strings.Builder

	sb.WriteString("! FRR configuration generated by tgen\n")
	sb.WriteString(fmt.Sprintf("! Switch: %s  Role: %s  ASN: %d  VTEP: %s\n", sw.Name, sw.Role, sw.ASN, sw.VtepAddr))
	sb.WriteString("!\n")
	sb.WriteString("frr defaults traditional\n")
	sb.WriteString(fmt.Sprintf("hostname %s\n", sw.Name))
	sb.WriteString("!\n")

	// VRF definition (SRv6 leaves/nodes only — spines are VRF-less transit).
	if srv6 && !isSpine {
		sb.WriteString("vrf Vrf100\n")
		sb.WriteString(" vni 100\n")
		sb.WriteString("exit\n")
		sb.WriteString("!\n")
	}

	// Loopback0 with VTEP address
	sb.WriteString("interface Loopback0\n")
	sb.WriteString(fmt.Sprintf(" ipv6 address %s\n", sw.VtepAddr))
	sb.WriteString("exit\n")
	sb.WriteString("!\n")

	// SRv6 locator + static End SID (SRv6 enabled, both spines and leaves/nodes).
	// Must appear BEFORE router bgp — FRR 10.4.1 parser requires this
	// ordering to transition between top-level nodes.
	if srv6 {
		locatorName := sw.Name
		locatorPrefix := topo.LocatorPrefixForASN(sw.ASN)
		sidAddr := topo.SidAddrForASN(sw.ASN)

		sb.WriteString("segment-routing\n")
		sb.WriteString(" srv6\n")
		sb.WriteString("  locators\n")
		sb.WriteString(fmt.Sprintf("   locator %s\n", locatorName))
		sb.WriteString(fmt.Sprintf("    prefix %s\n", locatorPrefix))
		sb.WriteString("   exit\n")
		sb.WriteString("  exit\n")
		sb.WriteString("  static-sids\n")
		sb.WriteString(fmt.Sprintf("   sid %s/128 locator %s behavior uN\n", sidAddr, locatorName))
		sb.WriteString("  exit\n")
		sb.WriteString(" exit\n")
		sb.WriteString("exit\n")
		sb.WriteString("!\n")
	}

	// BGP router
	sb.WriteString(fmt.Sprintf("router bgp %d\n", sw.ASN))
	sb.WriteString(" no bgp ebgp-requires-policy\n")
	sb.WriteString(" no bgp default ipv4-unicast\n")
	sb.WriteString(" bgp disable-ebgp-connected-route-check\n")
	sb.WriteString(" bgp bestpath as-path multipath-relax\n")
	sb.WriteString(" !\n")

	// Peer-group for unnumbered eBGP transit
	sb.WriteString(" neighbor TRANSIT peer-group\n")
	sb.WriteString(" neighbor TRANSIT remote-as external\n")
	sb.WriteString(" neighbor TRANSIT ebgp-multihop\n")
	sb.WriteString(" !\n")

	// Unnumbered neighbors via interface keyword + peer-group
	for _, nb := range neighbors {
		kernel := kernelPortName(nb.localPort)
		sb.WriteString(fmt.Sprintf(" neighbor %s interface peer-group TRANSIT\n", kernel))
	}
	sb.WriteString(" !\n")

	// IPv6 unicast address family (underlay)
	sb.WriteString(" address-family ipv6 unicast\n")
	sb.WriteString(fmt.Sprintf("  network %s\n", sw.VtepAddr))
	if srv6 && !isSpine {
		sb.WriteString(fmt.Sprintf("  network %s\n", topo.LocatorPrefixForASN(sw.ASN)))
	}
	sb.WriteString("  neighbor TRANSIT activate\n")
	sb.WriteString("  neighbor TRANSIT next-hop-self\n")
	sb.WriteString(" exit-address-family\n")

	// L3VPN relay (SRv6 spines only).
	// Spines relay VPN routes between leaves; next-hop-self ensures
	// re-advertised routes point back to the spine.
	if srv6 && isSpine {
		sb.WriteString(" !\n")
		sb.WriteString(" address-family ipv4 vpn\n")
		sb.WriteString("  neighbor TRANSIT activate\n")
		sb.WriteString("  neighbor TRANSIT next-hop-self\n")
		sb.WriteString(" exit-address-family\n")
	}

	// L3VPN (SRv6 leaves/nodes only).
	if srv6 && !isSpine {
		sb.WriteString(" !\n")
		sb.WriteString(" address-family ipv4 vpn\n")
		sb.WriteString("  neighbor TRANSIT activate\n")
		sb.WriteString(" exit-address-family\n")
	}

	// SRv6 under BGP — both spines and leaves/nodes need this for
	// locator advertisement and End SID assignment in BGP.
	if srv6 {
		sb.WriteString(" !\n")
		sb.WriteString(" segment-routing srv6\n")
		sb.WriteString(fmt.Sprintf("  locator %s\n", sw.Name))
		sb.WriteString(" !\n")
	}

	sb.WriteString("exit\n")
	sb.WriteString("!\n")

	// Per-VRF BGP instance (SRv6 leaves/nodes only).
	if srv6 && !isSpine {
		sb.WriteString(fmt.Sprintf("router bgp %d vrf Vrf100\n", sw.ASN))
		sb.WriteString(" !\n")
		sb.WriteString(" address-family ipv4 unicast\n")
		sb.WriteString("  redistribute connected\n")
		sb.WriteString("  sid vpn export auto\n")
		sb.WriteString(fmt.Sprintf("  rd vpn export %d:100\n", sw.ASN))
		sb.WriteString(fmt.Sprintf("  rt vpn both %s\n", sharedRT))
		sb.WriteString("  export vpn\n")
		sb.WriteString("  import vpn\n")
		sb.WriteString(" exit-address-family\n")
		sb.WriteString("exit\n")
		sb.WriteString("!\n")
	}

	sb.WriteString("line vty\n")
	sb.WriteString("!\n")

	return sb.String()
}

// writeFRRConfigs writes one FRR config file per switch into the frr/ subdirectory.
func (w *Writer) writeFRRConfigs() error {
	frrDir := filepath.Join(w.OutputDir, "frr")
	if err := os.MkdirAll(frrDir, 0o755); err != nil {
		return fmt.Errorf("creating frr dir: %w", err)
	}

	for _, sw := range w.Topology.Switches {
		config := w.generateFRRConfig(sw)
		path := filepath.Join(frrDir, sw.Name+".conf")
		if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
			return fmt.Errorf("writing frr config for %s: %w", sw.Name, err)
		}
	}

	return nil
}

// writeSRv6Routes writes per-switch seg6 route files into the srv6/
// subdirectory. All switches (spines and leaves/nodes) get route files
// so that every SRv6-capable node has its locator /64, End SID, and
// overlay /128 assigned and can reach remote overlay prefixes.
func (w *Writer) writeSRv6Routes() error {
	srv6Dir := filepath.Join(w.OutputDir, "srv6")
	if err := os.MkdirAll(srv6Dir, 0o755); err != nil {
		return fmt.Errorf("creating srv6 dir: %w", err)
	}

	for _, sw := range w.Topology.Switches {
		var sb strings.Builder
		sb.WriteString("#!/bin/bash\n")
		sb.WriteString(fmt.Sprintf("# SRv6 overlay routes for %s (ASN %d)\n", sw.Name, sw.ASN))
		sb.WriteString("# Generated by tgen — do not edit manually.\n")
		sb.WriteString("#\n")
		sidAddr := topo.SidAddrForASN(sw.ASN)
		sb.WriteString(fmt.Sprintf("# End SID:          %s\n", sidAddr))
		sb.WriteString(fmt.Sprintf("# Overlay prefix:   %s\n\n", sw.OverlayPrefix))
		sb.WriteString("FAILED=\"\"\n\n")

		// Assign the SRv6 locator /64 to Loopback0 as a connected address.
		// This is what actually enters the kernel RIB: without it the locator
		// is only a /128 (the End SID) sitting in the /64 space and is never
		// a connected prefix, so the FRR `network <locator>/64` statement
		// advertises nothing (BGP only advertises prefixes present in the
		// RIB). Once connected, every remote node installs an underlay route
		// to this switch's locator and can resolve the SRv6 outer destination
		// (the End SID, which lives within the locator). Without this the
		// outer-dst FIB lookup falls through to the default (RA) route and the
		// overlay is broken.
		locatorPrefix := topo.LocatorPrefixForASN(sw.ASN)
		sb.WriteString(fmt.Sprintf("ip addr replace %s dev Loopback0 2>/dev/null || true\n", locatorPrefix))
		sb.WriteString("\n")

		// Assign the End SID to Loopback0 so the kernel can perform
		// seg6 decapsulation for packets destined to this switch.
		sb.WriteString(fmt.Sprintf("ip addr replace %s/128 dev Loopback0 2>/dev/null || true\n", sidAddr))
		sb.WriteString("\n")

		// Add the overlay /128 to Loopback0 so the kernel can deliver
		// decapsulated packets to this switch.
		overlayHost := sw.OverlayPrefix[:len(sw.OverlayPrefix)-3] + "1/128"
		sb.WriteString(fmt.Sprintf("ip addr replace %s dev Loopback0 2>/dev/null || true\n", overlayHost))
		sb.WriteString("\n")

		// Seg6 routes to every remote non-spine switch's overlay prefix.
		// Each route: dest = remote overlay /64, SRH = [remote End SID].
		// The outer dst (remote SID) falls within the remote locator /64
		// advertised via BGP; seg6_output_core() re-runs ip6_route_output
		// on the encapsulated header with flowi6.oif = 0, so the packet is
		// forwarded by that lookup (BGP ECMP nexthops included) rather than
		// by the device named below. Verified on Linux 6.8: identical
		// encapsulated traffic reaches the peer with "dev Loopback0" and
		// with the transit interface.
		//
		// A device clause is mandatory here — iproute2 rejects a seg6 encap
		// route without one ("RTNETLINK answers: No such device"). It is
		// not purely cosmetic though: it also steers outer-source-address
		// selection (dev Loopback0 yields the End SID as outer src, an
		// egress interface yields that interface's underlay address).
		// Loopback0 is used because the generator cannot name a unique
		// transit interface per prefix in ring/spine-leaf topologies, where
		// a remote prefix is reachable over several uplinks.
		//
		// "replace" instead of "add" keeps re-runs idempotent: "add" fails
		// with EEXIST once the route exists (e.g. container restart).
		// Errors are no longer sent to /dev/null; a failed install records
		// its prefix in FAILED and the summary at the bottom surfaces it
		// (exit 1) instead of silently blackholing the overlay.
		for _, other := range w.Topology.Switches {
			if other.Name == sw.Name {
				continue
			}
			if other.Role == "spine" {
				continue
			}
			remoteSID := topo.SidAddrForASN(other.ASN)
			sb.WriteString(fmt.Sprintf(
				"ip -6 route replace %s encap seg6 mode encap segs %s dev Loopback0 || FAILED=\"$FAILED %s\"\n",
				other.OverlayPrefix, remoteSID, other.OverlayPrefix,
			))
		}

		sb.WriteString("\nif [ -n \"$FAILED\" ]; then\n")
		sb.WriteString("\techo \"WARNING: failed to install seg6 routes:$FAILED\"\n")
		sb.WriteString("\techo \"WARNING: check CONFIG_IPV6_SEG6_LWTUNNEL and seg6_enabled; SRv6 overlay will blackhole\"\n")
		sb.WriteString("\texit 1\n")
		sb.WriteString("fi\n")
		sb.WriteString("echo \"All seg6 routes installed\"\n")

		path := filepath.Join(srv6Dir, sw.Name+".routes")
		if err := os.WriteFile(path, []byte(sb.String()), 0o755); err != nil {
			return fmt.Errorf("writing srv6 routes for %s: %w", sw.Name, err)
		}
	}

	return nil
}

func (w *Writer) writeStartScript() error {
	data := []byte(`#!/usr/bin/env bash
	set -e

	echo "Building images..."
	docker compose build

	echo "Starting topology..."
	docker compose up -d

	echo
	echo "Running containers:"
	docker ps --filter "name=sonic-"
	echo
	echo "Webmesh UI: http://localhost:8092"
	`)
	return os.WriteFile(filepath.Join(w.OutputDir, "start.sh"), data, 0o755)
}

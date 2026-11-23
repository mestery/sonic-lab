#!/usr/bin/env bash
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
	
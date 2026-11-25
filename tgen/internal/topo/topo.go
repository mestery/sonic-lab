/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

// Package topo generates network topologies from simple parameters.
// It supports three topology types:
//
//   - Spine-leaf: S spines, L leaves, every leaf uplinks to every spine
//   - Full mesh: N nodes, every node connected to every other node
//   - Ring: N nodes connected in a circular loop
//
// Hosts can be attached to any topology via the HostsPerSwitch and
// PortsPerHost options.
package topo

import (
	"fmt"
)

// Topology is the generated network topology.
type Topology struct {
	Name        string
	Switches    []Switch
	Hosts       []Host
	Links       []Link
	Attachments []HostAttachment // one per host↔switch link, with default addressing
	SRv6        bool             // enable SRv6 overlay generation
	NetnsSuffix string           // suffix for each switch's backing netns container (e.g. "linux")
	Warnings    []string         // non-fatal problems found while generating, for the CLI to print
}

// DefaultNetnsSuffix is the default suffix for backing netns containers
// ("spine1" → "spine1-linux").
const DefaultNetnsSuffix = "linux"

// DeviceNetnsName returns the name of the backing netns container for a switch.
// This is the device name used in link endpoints and the compose service
// that the SONiC container shares its network namespace with.
func (t *Topology) DeviceNetnsName(switchName string) string {
	return switchName + "-" + t.NetnsSuffix
}

// Switch represents a single SONiC virtual switch.
type Switch struct {
	Name          string
	Role          string // "spine", "leaf", or "node"
	ASN           int
	VtepAddr      string // VTEP IPv6 ULA loopback address (e.g. fd00:0:0:fd98::1/128)
	OverlayPrefix string // SRv6 overlay service prefix (e.g. 2001:db8:fd98::/64)
	OverlayIPv4   string // IPv4 overlay address in VRF (e.g. 192.168.1.1/24)
	Ports         []string
	// AddrIndex is the switch number used in default host addressing
	// (see HostAttachment). Leaves and ring/mesh nodes are numbered
	// 1..N in order; spines start at SpineHostBase so their host
	// subnets don't collide with leaf subnets.
	AddrIndex int
}

// Host represents a simulated host container attached to one or more switches.
type Host struct {
	Name  string
	Ports []string
}

// Link represents a connection between two switch ports.
type Link struct {
	A Endpoint
	B Endpoint
}

// Endpoint identifies a specific port on a specific device.
type Endpoint struct {
	Device string
	Port   string
}

// HostAttachment describes one host↔switch link and its default
// addressing. The addresses are the lab's starting state only: they are
// applied after boot and may be changed freely while the lab runs, so
// nothing else in the generated fabric (e.g. FRR) depends on them.
//
// Default scheme, with S = switch AddrIndex and H = HostIndex:
//
//	IPv4: 10.S.H.0/24       switch 10.S.H.1, host 10.S.H.2
//	IPv6: fd00:0:S:H::/64   switch fd00:0:S:H::1, host fd00:0:S:H::2
//
// S and H are written as the same decimal digits in both families so the
// two line up visually (10.3.1.0/24 ↔ fd00:0:3:1::/64); in IPv6 the
// digits are read as hex, which is harmless.
type HostAttachment struct {
	Switch     string // switch name, e.g. "leaf1"
	SwitchPort string // SONiC port on the switch, e.g. "Ethernet8"
	Host       string // host name, e.g. "host1"
	HostPort   string // logical port on the host, as in topology.yml ("Ethernet0")
	HostIface  string // kernel interface inside the host container ("eth1")
	HostIndex  int    // 1-based index of this attachment on its switch

	SwitchIPv4 string // e.g. "10.3.1.1/24"
	HostIPv4   string // e.g. "10.3.1.2/24"
	SwitchIPv6 string // e.g. "fd00:0:3:1::1/64"
	HostIPv6   string // e.g. "fd00:0:3:1::2/64"
}

// Host address pools. The fabric advertises any connected prefix inside
// these pools (see the HOST-SUBNETS policy in the FRR config), so users can
// re-address hosts anywhere within them without touching FRR.
//
// HostIPv6Reserved is carved out of HostIPv6Pool because it holds the
// switch loopbacks, SRv6 locators and End SIDs (fd00:0:0:<asn>::), which
// are advertised separately. Host subnets never fall inside it because
// AddrIndex is always >= 1.
const (
	HostIPv4Pool     = "10.0.0.0/8"
	HostIPv6Pool     = "fd00::/32"
	HostIPv6Reserved = "fd00::/48"

	// SpineHostBase is the first AddrIndex given to spines, so spine host
	// subnets start at 10.60.x.0/24. Leaves/nodes numbered at or above it
	// overlap the spine range: a warning normally, an error when spines
	// also get hosts and the subnets would actually collide.
	SpineHostBase = 60
	// maxAddrOctet bounds AddrIndex and HostIndex: both form an IPv4 octet.
	maxAddrOctet = 255
)

// HostSubnetAddrs returns the default switch/host addresses for host
// attachment H on the switch with AddrIndex S.
func HostSubnetAddrs(s, h int) (switchV4, hostV4, switchV6, hostV6 string) {
	return fmt.Sprintf("10.%d.%d.1/24", s, h),
		fmt.Sprintf("10.%d.%d.2/24", s, h),
		fmt.Sprintf("fd00:0:%d:%d::1/64", s, h),
		fmt.Sprintf("fd00:0:%d:%d::2/64", s, h)
}

// HostKernelIface returns the kernel interface name a host's logical port
// gets inside its container. webmesh uses the KVM lane map, under which
// EthernetN is eth(N/4 + 1): Ethernet0 → eth1, Ethernet4 → eth2.
func HostKernelIface(port string) (string, error) {
	var n int
	if _, err := fmt.Sscanf(port, "Ethernet%d", &n); err != nil || n%4 != 0 || n < 0 {
		return "", fmt.Errorf("unexpected host port name %q", port)
	}
	return fmt.Sprintf("eth%d", n/4+1), nil
}

// Options controls topology generation.
type Options struct {
	Spines         int    // -s: number of spine switches
	Leaves         int    // -l: number of leaf switches
	Mesh           bool   // -m: generate full mesh topology
	Ring           bool   // -r: generate ring topology
	Nodes          int    // -n: number of nodes (for mesh/ring)
	Links          int    // -links: parallel links per switch pair (default 1)
	HostsPerSwitch int    // -h: number of hosts attached to each eligible switch
	PortsPerHost   int    // -p: number of ports per host (default 1)
	HostsOnSpines  bool   // -host-spines: also attach hosts to spines
	SRv6           bool   // -srv6: enable SRv6 overlay generation
	NetnsSuffix    string // -base: backing container suffix (default "linux")
}

// MaxPorts is the maximum number of ports available per switch
// (KVM platform maps 30 lanes: Ethernet0 through Ethernet116, step 4).
const MaxPorts = 30

// portName returns the Ethernet interface name for a 0-based port index.
// Ports use step-4 naming to match the KVM lane map.
func portName(index int) string {
	return fmt.Sprintf("Ethernet%d", index*4)
}

// VtepAddrForASN returns a deterministic VTEP IPv6 ULA loopback address
// derived from the switch's ASN. Scheme: fd00:0:0:<asn>::1/128.
// Only 2-byte ASNs (0-65535) are supported, which covers all lab use cases.
func VtepAddrForASN(asn int) string {
	return fmt.Sprintf("fd00:0:0:%x::1/128", asn)
}

// LocatorPrefixForASN returns the SRv6 locator /64 prefix derived from the
// switch's ASN. Scheme: fd00:0:0:<asn>::/64.
func LocatorPrefixForASN(asn int) string {
	return fmt.Sprintf("fd00:0:0:%x::/64", asn)
}

// OverlayPrefixForASN returns the SRv6 overlay service /64 prefix derived
// from the switch's ASN. Uses IPv6 documentation space (2001:db8::/32).
// Scheme: 2001:db8:<asn>::/64.
func OverlayPrefixForASN(asn int) string {
	return fmt.Sprintf("2001:db8:%x::/64", asn)
}

// VtepBareAddr returns the VTEP address without the /128 prefix length.
// E.g. "fd00:0:0:fde8::1/128" → "fd00:0:0:fde8::1"
func VtepBareAddr(vtepAddr string) string {
	return vtepAddr[:len(vtepAddr)-4] // strip "/128"
}

// SidAddrForASN returns the SRv6 End SID (uN behavior) as a bare IPv6
// address derived from the switch's ASN. Scheme: fd00:0:0:<asn>::100.
// This is deliberately distinct from the VTEP address (::1) to avoid a
// collision with the loopback interface address. The SID must be a
// locally-assigned address for the kernel to perform seg6 decapsulation.
func SidAddrForASN(asn int) string {
	return fmt.Sprintf("fd00:0:0:%x::100", asn)
}

// OverlayIPv4ForASN returns a deterministic IPv4 /24 address for the
// switch's VRF overlay interface. The second octet is the ASN offset from
// 65000, reduced modulo 255 into a positive residue and shifted into 1..255,
// so every switch gets a unique, always-valid /24 for any ASN. (The old fixed
// base asn-65100+1 produced negative octets — invalid addresses — for
// topologies like ring/mesh whose ASNs start below 65100, which silently left
// the VRF overlay interface unaddressed and the L3VPN with nothing to
// advertise.) The positive-modulo keeps the octet valid even when the ASN is
// below the 65000 base, since Go's % keeps the dividend's sign.
func OverlayIPv4ForASN(asn int) string {
	offset := ((asn-65000)%255+255)%255 + 1
	return fmt.Sprintf("192.168.%d.1/24", offset)
}

// Generate builds a Topology from the given Options.
func Generate(opts Options) (*Topology, error) {
	// Default to 1 link per pair if not specified
	if opts.Links < 1 {
		opts.Links = 1
	}
	if opts.NetnsSuffix == "" {
		opts.NetnsSuffix = DefaultNetnsSuffix
	}

	var t *Topology

	switch {
	case opts.Mesh:
		if opts.Nodes < 2 {
			return nil, fmt.Errorf("mesh requires at least 2 nodes (got %d)", opts.Nodes)
		}
		if (opts.Nodes-1)*opts.Links > MaxPorts {
			return nil, fmt.Errorf("mesh of %d nodes with %d links requires %d ports per node, exceeds max %d",
				opts.Nodes, opts.Links, (opts.Nodes-1)*opts.Links, MaxPorts)
		}
		t = mesh(opts.Nodes, opts.Links, opts.NetnsSuffix)

	case opts.Ring:
		if opts.Nodes < 3 {
			return nil, fmt.Errorf("ring requires at least 3 nodes (got %d)", opts.Nodes)
		}
		if 2*opts.Links > MaxPorts {
			return nil, fmt.Errorf("ring with %d links requires %d ports per node, exceeds max %d",
				opts.Links, 2*opts.Links, MaxPorts)
		}
		t = ring(opts.Nodes, opts.Links, opts.NetnsSuffix)

	default:
		if opts.Spines < 1 {
			return nil, fmt.Errorf("spine-leaf requires at least 1 spine (got %d)", opts.Spines)
		}
		if opts.Leaves < 1 {
			return nil, fmt.Errorf("spine-leaf requires at least 1 leaf (got %d)", opts.Leaves)
		}
		if opts.Leaves*opts.Links > MaxPorts {
			return nil, fmt.Errorf("each spine would need %d ports (%d leaves x %d links), exceeds max %d",
				opts.Leaves*opts.Links, opts.Leaves, opts.Links, MaxPorts)
		}
		if opts.Spines*opts.Links > MaxPorts {
			return nil, fmt.Errorf("each leaf would need %d ports (%d spines x %d links), exceeds max %d",
				opts.Spines*opts.Links, opts.Spines, opts.Links, MaxPorts)
		}
		t = spineLeaf(opts.Spines, opts.Leaves, opts.Links, opts.NetnsSuffix)
	}

	assignAddrIndexes(t)

	// Add hosts if requested
	if opts.HostsPerSwitch > 0 {
		if err := addHosts(t, opts.HostsPerSwitch, opts.PortsPerHost, opts.HostsOnSpines); err != nil {
			return nil, err
		}
	}

	// Set SRv6 flag and overlay prefixes
	t.SRv6 = opts.SRv6
	if opts.SRv6 {
		for i := range t.Switches {
			t.Switches[i].OverlayPrefix = OverlayPrefixForASN(t.Switches[i].ASN)
			if t.Switches[i].Role != "spine" {
				t.Switches[i].OverlayIPv4 = OverlayIPv4ForASN(t.Switches[i].ASN)
			}
		}
	}

	return t, nil
}

// assignAddrIndexes numbers switches for default host addressing:
// non-spine switches (leaves, ring/mesh nodes) 1..N in order, spines
// SpineHostBase, SpineHostBase+1, ... in order.
func assignAddrIndexes(t *Topology) {
	nextOther, nextSpine := 1, SpineHostBase
	for i := range t.Switches {
		if t.Switches[i].Role == "spine" {
			t.Switches[i].AddrIndex = nextSpine
			nextSpine++
		} else {
			t.Switches[i].AddrIndex = nextOther
			nextOther++
		}
	}
}

// checkAddrIndexes validates the AddrIndex of every switch that gets hosts.
// Every index must fit in an IPv4 octet. Leaves/nodes numbered at or above
// SpineHostBase overlap the spine range: if a spine with hosts has the
// same index the default subnets would collide, which is an error;
// otherwise it is only a warning, since nothing actually clashes.
func checkAddrIndexes(t *Topology, eligible []int) error {
	spineIdx := map[int]string{}
	for _, si := range eligible {
		if sw := t.Switches[si]; sw.Role == "spine" {
			spineIdx[sw.AddrIndex] = sw.Name
		}
	}

	var inSpineRange []string
	for _, si := range eligible {
		sw := t.Switches[si]
		if sw.AddrIndex > maxAddrOctet {
			return fmt.Errorf("%s: address index %d exceeds %d; too many switches for default host addressing",
				sw.Name, sw.AddrIndex, maxAddrOctet)
		}
		if sw.Role == "spine" || sw.AddrIndex < SpineHostBase {
			continue
		}
		if spine, ok := spineIdx[sw.AddrIndex]; ok {
			return fmt.Errorf("%s and %s would both get host subnets 10.%d.x.0/24: more than %d leaves/nodes with hosts on spines",
				sw.Name, spine, sw.AddrIndex, SpineHostBase-1)
		}
		inSpineRange = append(inSpineRange, sw.Name)
	}
	if len(inSpineRange) > 0 {
		t.Warnings = append(t.Warnings, fmt.Sprintf(
			"%d leaf/node switches with hosts are numbered %d or higher (%s ... %s), overlapping the spine host range (SpineHostBase=%d); "+
				"no subnets collide now, but they would if spines also got hosts",
			len(inSpineRange), SpineHostBase, inSpineRange[0], inSpineRange[len(inSpineRange)-1], SpineHostBase))
	}
	return nil
}

// addHosts appends host containers and host-to-switch links to the topology.
// Hosts attach to non-spine switches only, unless onSpines is set (in a
// Clos fabric hosts belong on leaves). Each eligible switch gets
// hostsPerSwitch hosts. Each host has portsPerHost ports, with port p
// connecting to eligible switch (home + p) % len(eligible).
//
// Every host link is also recorded as a HostAttachment with its default
// addressing.
func addHosts(t *Topology, hostsPerSwitch, portsPerHost int, onSpines bool) error {
	if portsPerHost < 1 {
		portsPerHost = 1
	}

	// Indexes into t.Switches of the switches that get hosts.
	var eligible []int
	for i, sw := range t.Switches {
		if sw.Role != "spine" || onSpines {
			eligible = append(eligible, i)
		}
	}
	if err := checkAddrIndexes(t, eligible); err != nil {
		return err
	}

	totalSwitches := len(eligible)
	if totalSwitches == 0 {
		return fmt.Errorf("no switches eligible for hosts (hosts attach to leaves/nodes; use -host-spines to include spines)")
	}
	totalHosts := hostsPerSwitch * totalSwitches

	// Create host entries
	for i := 0; i < totalHosts; i++ {
		h := Host{Name: fmt.Sprintf("host%d", i+1)}
		for p := 0; p < portsPerHost; p++ {
			h.Ports = append(h.Ports, portName(p))
		}
		t.Hosts = append(t.Hosts, h)
	}

	// Track next available port index per switch (starts after switch-to-switch ports)
	nextPort := make([]int, len(t.Switches))
	hostCount := make([]int, len(t.Switches)) // attachments so far, per switch
	for i, sw := range t.Switches {
		nextPort[i] = len(sw.Ports)
	}

	// Create host-to-switch links
	for i := 0; i < totalHosts; i++ {
		homeSwitch := i / hostsPerSwitch
		hostName := fmt.Sprintf("host%d", i+1)
		for p := 0; p < portsPerHost; p++ {
			target := eligible[(homeSwitch+p)%totalSwitches]
			sw := &t.Switches[target]

			idx := nextPort[target]
			if idx >= MaxPorts {
				return fmt.Errorf("%s would need %d ports (host connections), exceeds max %d",
					sw.Name, idx+1, MaxPorts)
			}
			nextPort[target]++
			hostCount[target]++

			swPort, hostPort := portName(idx), portName(p)
			sw.Ports = append(sw.Ports, swPort)

			t.Links = append(t.Links, Link{
				A: Endpoint{Device: t.DeviceNetnsName(sw.Name), Port: swPort},
				B: Endpoint{Device: hostName, Port: hostPort},
			})

			iface, err := HostKernelIface(hostPort)
			if err != nil {
				return err
			}
			swV4, hV4, swV6, hV6 := HostSubnetAddrs(sw.AddrIndex, hostCount[target])
			t.Attachments = append(t.Attachments, HostAttachment{
				Switch:     sw.Name,
				SwitchPort: swPort,
				Host:       hostName,
				HostPort:   hostPort,
				HostIface:  iface,
				HostIndex:  hostCount[target],
				SwitchIPv4: swV4,
				HostIPv4:   hV4,
				SwitchIPv6: swV6,
				HostIPv6:   hV6,
			})
		}
	}

	return nil
}

// spineLeaf generates a classic spine-leaf topology where every leaf
// has `links` parallel uplinks to every spine (for ECMP).
func spineLeaf(spines, leaves, links int, netnsSuffix string) *Topology {
	t := &Topology{Name: fmt.Sprintf("spine-leaf-%dx%d", spines, leaves), NetnsSuffix: netnsSuffix}

	// Create spines: each has leaves*links ports
	for s := 0; s < spines; s++ {
		asn := 65000 + s
		sw := Switch{
			Name:     fmt.Sprintf("spine%d", s+1),
			Role:     "spine",
			ASN:      asn,
			VtepAddr: VtepAddrForASN(asn),
		}
		// Ports grouped by leaf: leaf l uses port indices [l*links, (l+1)*links)
		for l := 0; l < leaves; l++ {
			for k := 0; k < links; k++ {
				sw.Ports = append(sw.Ports, portName(l*links+k))
			}
		}
		t.Switches = append(t.Switches, sw)
	}

	// Create leaves: each has spines*links ports
	for l := 0; l < leaves; l++ {
		asn := 65100 + l
		sw := Switch{
			Name:     fmt.Sprintf("leaf%d", l+1),
			Role:     "leaf",
			ASN:      asn,
			VtepAddr: VtepAddrForASN(asn),
		}
		// Ports grouped by spine: spine s uses port indices [s*links, (s+1)*links)
		for s := 0; s < spines; s++ {
			for k := 0; k < links; k++ {
				sw.Ports = append(sw.Ports, portName(s*links+k))
			}
		}
		t.Switches = append(t.Switches, sw)
	}

	// Create links: leaf[l] ↔ spine[s], `links` parallel links per pair
	for s := 0; s < spines; s++ {
		for l := 0; l < leaves; l++ {
			for k := 0; k < links; k++ {
				t.Links = append(t.Links, Link{
					A: Endpoint{
						Device: t.DeviceNetnsName(fmt.Sprintf("spine%d", s+1)),
						Port:   portName(l*links + k),
					},
					B: Endpoint{
						Device: t.DeviceNetnsName(fmt.Sprintf("leaf%d", l+1)),
						Port:   portName(s*links + k),
					},
				})
			}
		}
	}

	return t
}

// mesh generates a full mesh topology where every node is connected
// to every other node with `links` parallel links per pair.
func mesh(n, links int, netnsSuffix string) *Topology {
	t := &Topology{Name: fmt.Sprintf("mesh-%d", n), NetnsSuffix: netnsSuffix}

	// Create nodes: each has (n-1)*links ports
	for i := 0; i < n; i++ {
		asn := 65000 + i
		sw := Switch{
			Name:     fmt.Sprintf("node%d", i+1),
			Role:     "node",
			ASN:      asn,
			VtepAddr: VtepAddrForASN(asn),
		}
		// Ports grouped by peer position (self skipped)
		for p := 0; p < n-1; p++ {
			for k := 0; k < links; k++ {
				sw.Ports = append(sw.Ports, portName(p*links+k))
			}
		}
		t.Switches = append(t.Switches, sw)
	}

	// Create links: for each pair (i, j) where i < j
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			// Node i: peer j is at position (j-1) in the peer list [skip self at i]
			// Node j: peer i is at position i in the peer list [i < j, so i keeps its index]
			for k := 0; k < links; k++ {
				t.Links = append(t.Links, Link{
					A: Endpoint{
						Device: t.DeviceNetnsName(fmt.Sprintf("node%d", i+1)),
						Port:   portName((j-1)*links + k),
					},
					B: Endpoint{
						Device: t.DeviceNetnsName(fmt.Sprintf("node%d", j+1)),
						Port:   portName(i*links + k),
					},
				})
			}
		}
	}

	return t
}

// ring generates a ring topology where each node connects to its
// two neighbors with `links` parallel links per neighbor (for ECMP).
func ring(n, links int, netnsSuffix string) *Topology {
	t := &Topology{Name: fmt.Sprintf("ring-%d", n), NetnsSuffix: netnsSuffix}

	// Create nodes: each has 2*links ports
	for i := 0; i < n; i++ {
		asn := 65000 + i
		sw := Switch{
			Name:     fmt.Sprintf("node%d", i+1),
			Role:     "node",
			ASN:      asn,
			VtepAddr: VtepAddrForASN(asn),
		}
		// Ports 0..links-1: "prev" neighbor; ports links..2*links-1: "next" neighbor
		for p := 0; p < 2*links; p++ {
			sw.Ports = append(sw.Ports, portName(p))
		}
		t.Switches = append(t.Switches, sw)
	}

	// Create links: for each pair of neighbors in the ring, `links` parallel links
	for i := 0; i < n; i++ {
		next := (i + 1) % n
		for k := 0; k < links; k++ {
			t.Links = append(t.Links, Link{
				A: Endpoint{
					Device: t.DeviceNetnsName(fmt.Sprintf("node%d", i+1)),
					Port:   portName(links + k), // "next" side
				},
				B: Endpoint{
					Device: t.DeviceNetnsName(fmt.Sprintf("node%d", next+1)),
					Port:   portName(k), // "prev" side
				},
			})
		}
	}

	return t
}

/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package topo

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

func mustGenerate(t *testing.T, opts Options) *Topology {
	t.Helper()
	tt, err := Generate(opts)
	if err != nil {
		t.Fatalf("Generate(%+v) error: %v", opts, err)
	}
	return tt
}

func TestSpineLeaf_Basic(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2})

	if tt.Name != "spine-leaf-1x2" {
		t.Errorf("Name = %q, want %q", tt.Name, "spine-leaf-1x2")
	}
	if len(tt.Switches) != 3 {
		t.Fatalf("Switches = %d, want 3", len(tt.Switches))
	}
	if len(tt.Links) != 2 {
		t.Fatalf("Links = %d, want 2", len(tt.Links))
	}

	// Verify spine has 2 ports (one per leaf)
	var spine, leaf1, leaf2 *Switch
	for i := range tt.Switches {
		switch tt.Switches[i].Name {
		case "spine1":
			spine = &tt.Switches[i]
		case "leaf1":
			leaf1 = &tt.Switches[i]
		case "leaf2":
			leaf2 = &tt.Switches[i]
		}
	}

	if spine == nil || leaf1 == nil || leaf2 == nil {
		t.Fatal("missing expected switches")
	}

	if len(spine.Ports) != 2 {
		t.Errorf("spine1 ports = %d, want 2", len(spine.Ports))
	}
	if len(leaf1.Ports) != 1 {
		t.Errorf("leaf1 ports = %d, want 1", len(leaf1.Ports))
	}
	if len(leaf2.Ports) != 1 {
		t.Errorf("leaf2 ports = %d, want 1", len(leaf2.Ports))
	}

	// Verify ASNs
	if spine.ASN != 65000 {
		t.Errorf("spine1 ASN = %d, want 65000", spine.ASN)
	}
	if leaf1.ASN != 65100 {
		t.Errorf("leaf1 ASN = %d, want 65100", leaf1.ASN)
	}
	if leaf2.ASN != 65101 {
		t.Errorf("leaf2 ASN = %d, want 65101", leaf2.ASN)
	}
}

func TestSpineLeaf_DualSpine(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 3})

	if len(tt.Switches) != 5 {
		t.Fatalf("Switches = %d, want 5", len(tt.Switches))
	}
	if len(tt.Links) != 6 {
		t.Fatalf("Links = %d, want 6", len(tt.Links))
	}

	// Each leaf should have 2 ports (one per spine)
	for _, sw := range tt.Switches {
		if sw.Role == "leaf" && len(sw.Ports) != 2 {
			t.Errorf("%s ports = %d, want 2", sw.Name, len(sw.Ports))
		}
		if sw.Role == "spine" && len(sw.Ports) != 3 {
			t.Errorf("%s ports = %d, want 3", sw.Name, len(sw.Ports))
		}
	}
}

func TestSpineLeaf_PortNaming(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 3})

	var spine *Switch
	for i := range tt.Switches {
		if tt.Switches[i].Name == "spine1" {
			spine = &tt.Switches[i]
		}
	}

	wantPorts := []string{"Ethernet0", "Ethernet4", "Ethernet8"}
	if len(spine.Ports) != 3 {
		t.Fatalf("spine1 ports = %d, want 3", len(spine.Ports))
	}
	for i, want := range wantPorts {
		if spine.Ports[i] != want {
			t.Errorf("spine1.Ports[%d] = %q, want %q", i, spine.Ports[i], want)
		}
	}
}

func TestSpineLeaf_LinkEndpoints(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2})

	// Link 0: spine1:Ethernet0 <-> leaf1:Ethernet0
	l := tt.Links[0]
	if l.A.Device != "spine1-linux" || l.A.Port != "Ethernet0" {
		t.Errorf("link[0].A = %s:%s, want spine1-linux:Ethernet0", l.A.Device, l.A.Port)
	}
	if l.B.Device != "leaf1-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[0].B = %s:%s, want leaf1-linux:Ethernet0", l.B.Device, l.B.Port)
	}

	// Link 1: spine1:Ethernet4 <-> leaf2:Ethernet0
	l = tt.Links[1]
	if l.A.Device != "spine1-linux" || l.A.Port != "Ethernet4" {
		t.Errorf("link[1].A = %s:%s, want spine1-linux:Ethernet4", l.A.Device, l.A.Port)
	}
	if l.B.Device != "leaf2-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[1].B = %s:%s, want leaf2-linux:Ethernet0", l.B.Device, l.B.Port)
	}
}

func TestMesh_Basic(t *testing.T) {
	tt := mustGenerate(t, Options{Mesh: true, Nodes: 3})

	if tt.Name != "mesh-3" {
		t.Errorf("Name = %q, want %q", tt.Name, "mesh-3")
	}
	if len(tt.Switches) != 3 {
		t.Fatalf("Switches = %d, want 3", len(tt.Switches))
	}
	if len(tt.Links) != 3 {
		t.Fatalf("Links = %d, want 3 (C(3,2))", len(tt.Links))
	}

	// Each node should have 2 ports
	for _, sw := range tt.Switches {
		if len(sw.Ports) != 2 {
			t.Errorf("%s ports = %d, want 2", sw.Name, len(sw.Ports))
		}
	}
}

func TestMesh_PortMapping(t *testing.T) {
	tt := mustGenerate(t, Options{Mesh: true, Nodes: 3})

	// Node1 (index 0) connects to:
	//   Node2 (index 1): port index (1-1)=0 → Ethernet0
	//   Node3 (index 2): port index (2-1)=1 → Ethernet4
	// Node2 (index 1) connects to:
	//   Node3 (index 2): port index (2-1)=1 → Ethernet4
	// Node3 (index 2) has no j>i

	// Link 0: node1:Ethernet0 <-> node2:Ethernet0
	l := tt.Links[0]
	if l.A.Device != "node1-linux" || l.A.Port != "Ethernet0" {
		t.Errorf("link[0].A = %s:%s, want node1-linux:Ethernet0", l.A.Device, l.A.Port)
	}
	if l.B.Device != "node2-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[0].B = %s:%s, want node2-linux:Ethernet0", l.B.Device, l.B.Port)
	}

	// Link 1: node1:Ethernet4 <-> node3:Ethernet0
	l = tt.Links[1]
	if l.A.Device != "node1-linux" || l.A.Port != "Ethernet4" {
		t.Errorf("link[1].A = %s:%s, want node1-linux:Ethernet4", l.A.Device, l.A.Port)
	}
	if l.B.Device != "node3-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[1].B = %s:%s, want node3-linux:Ethernet0", l.B.Device, l.B.Port)
	}

	// Link 2: node2:Ethernet4 <-> node3:Ethernet4
	l = tt.Links[2]
	if l.A.Device != "node2-linux" || l.A.Port != "Ethernet4" {
		t.Errorf("link[2].A = %s:%s, want node2-linux:Ethernet4", l.A.Device, l.A.Port)
	}
	if l.B.Device != "node3-linux" || l.B.Port != "Ethernet4" {
		t.Errorf("link[2].B = %s:%s, want node3-linux:Ethernet4", l.B.Device, l.B.Port)
	}
}

func TestMesh_LinksCount(t *testing.T) {
	// N nodes in full mesh → N*(N-1)/2 links
	for n := 2; n <= 5; n++ {
		tt := mustGenerate(t, Options{Mesh: true, Nodes: n})
		want := n * (n - 1) / 2
		if len(tt.Links) != want {
			t.Errorf("mesh(%d) links = %d, want %d", n, len(tt.Links), want)
		}
	}
}

func TestRing_Basic(t *testing.T) {
	tt := mustGenerate(t, Options{Ring: true, Nodes: 4})

	if tt.Name != "ring-4" {
		t.Errorf("Name = %q, want %q", tt.Name, "ring-4")
	}
	if len(tt.Switches) != 4 {
		t.Fatalf("Switches = %d, want 4", len(tt.Switches))
	}
	if len(tt.Links) != 4 {
		t.Fatalf("Links = %d, want 4", len(tt.Links))
	}

	// Each node has exactly 2 ports
	for _, sw := range tt.Switches {
		if len(sw.Ports) != 2 {
			t.Errorf("%s ports = %d, want 2", sw.Name, len(sw.Ports))
		}
		if sw.Ports[0] != "Ethernet0" || sw.Ports[1] != "Ethernet4" {
			t.Errorf("%s ports = %v, want [Ethernet0 Ethernet4]", sw.Name, sw.Ports)
		}
	}
}

func TestRing_Wraparound(t *testing.T) {
	tt := mustGenerate(t, Options{Ring: true, Nodes: 4})

	// Last link should be node4:Ethernet4 <-> node1:Ethernet0 (wraparound)
	last := tt.Links[len(tt.Links)-1]
	if last.A.Device != "node4-linux" || last.A.Port != "Ethernet4" {
		t.Errorf("last link A = %s:%s, want node4-linux:Ethernet4", last.A.Device, last.A.Port)
	}
	if last.B.Device != "node1-linux" || last.B.Port != "Ethernet0" {
		t.Errorf("last link B = %s:%s, want node1-linux:Ethernet0", last.B.Device, last.B.Port)
	}
}

func TestMesh_TooSmall(t *testing.T) {
	_, err := Generate(Options{Mesh: true, Nodes: 1})
	if err == nil {
		t.Error("expected error for mesh with 1 node")
	}
}

func TestRing_TooSmall(t *testing.T) {
	_, err := Generate(Options{Ring: true, Nodes: 2})
	if err == nil {
		t.Error("expected error for ring with 2 nodes")
	}
}

func TestSpineLeaf_NoSpines(t *testing.T) {
	_, err := Generate(Options{Spines: 0, Leaves: 3})
	if err == nil {
		t.Error("expected error for 0 spines")
	}
}

func TestSpineLeaf_NoLeaves(t *testing.T) {
	_, err := Generate(Options{Spines: 2, Leaves: 0})
	if err == nil {
		t.Error("expected error for 0 leaves")
	}
}

func TestSpineLeaf_ExceedsMaxPorts(t *testing.T) {
	_, err := Generate(Options{Spines: 31, Leaves: 1})
	if err == nil {
		t.Error("expected error for 31 spines (exceeds 30 port limit)")
	}
}

func TestMesh_ExceedsMaxPorts(t *testing.T) {
	_, err := Generate(Options{Mesh: true, Nodes: 32})
	if err == nil {
		t.Error("expected error for mesh with 32 nodes (needs 31 ports > 30 max)")
	}
}

func TestPortName(t *testing.T) {
	tests := []struct {
		index int
		want  string
	}{
		{0, "Ethernet0"},
		{1, "Ethernet4"},
		{2, "Ethernet8"},
		{7, "Ethernet28"},
		{29, "Ethernet116"},
	}
	for _, tc := range tests {
		if got := portName(tc.index); got != tc.want {
			t.Errorf("portName(%d) = %q, want %q", tc.index, got, tc.want)
		}
	}
}
func TestHosts_Basic(t *testing.T) {
	// 2 spines, 1 leaf, 1 host per switch (3 total hosts)
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 1, HostsPerSwitch: 1, HostsOnSpines: true})

	if len(tt.Hosts) != 3 {
		t.Fatalf("Hosts = %d, want 3", len(tt.Hosts))
	}
	// 2 switch-to-switch links (2 spines × 1 leaf) + 3 host links = 5 total
	if len(tt.Links) != 5 {
		t.Fatalf("Links = %d, want 5", len(tt.Links))
	}

	// Each host has 1 port
	for _, h := range tt.Hosts {
		if len(h.Ports) != 1 {
			t.Errorf("%s ports = %d, want 1", h.Name, len(h.Ports))
		}
		if h.Ports[0] != "Ethernet0" {
			t.Errorf("%s port = %q, want Ethernet0", h.Name, h.Ports[0])
		}
	}
}

func TestHosts_Naming(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 1, HostsPerSwitch: 2, HostsOnSpines: true})

	// Total hosts = 2 per switch * 3 switches = 6
	if len(tt.Hosts) != 6 {
		t.Fatalf("Hosts = %d, want 6", len(tt.Hosts))
	}
	wantNames := []string{"host1", "host2", "host3", "host4", "host5", "host6"}
	for i, want := range wantNames {
		if tt.Hosts[i].Name != want {
			t.Errorf("Hosts[%d].Name = %q, want %q", i, tt.Hosts[i].Name, want)
		}
	}
}

func TestHosts_MultiPort(t *testing.T) {
	// 2 spines, 1 leaf, 2 hosts per switch, 2 ports per host
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 1, HostsPerSwitch: 2, HostsOnSpines: true, PortsPerHost: 2})

	// Total hosts = 2 * 3 = 6
	if len(tt.Hosts) != 6 {
		t.Fatalf("Hosts = %d, want 6", len(tt.Hosts))
	}
	// Each host has 2 ports
	for _, h := range tt.Hosts {
		if len(h.Ports) != 2 {
			t.Errorf("%s ports = %d, want 2", h.Name, len(h.Ports))
		}
		if h.Ports[0] != "Ethernet0" || h.Ports[1] != "Ethernet4" {
			t.Errorf("%s ports = %v, want [Ethernet0 Ethernet4]", h.Name, h.Ports)
		}
	}
	// Links: 2 switch-switch (2 spines × 1 leaf) + 6 hosts × 2 ports = 14
	if len(tt.Links) != 14 {
		t.Fatalf("Links = %d, want 14", len(tt.Links))
	}
}

func TestHosts_PortMapping(t *testing.T) {
	// 2 spines, 1 leaf, 1 host per switch
	// Switch order: spine1(0), spine2(1), leaf1(2)
	// host0: home=spine1(0), port0→spine1
	// host1: home=spine2(1), port0→spine2
	// host2: home=leaf1(2), port0→leaf1
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 1, HostsPerSwitch: 1, HostsOnSpines: true})

	// Find host links (links where one endpoint is a host)
	var hostLinks []Link
	for _, l := range tt.Links {
		if isHostEndpoint(l.A) || isHostEndpoint(l.B) {
			hostLinks = append(hostLinks, l)
		}
	}
	if len(hostLinks) != 3 {
		t.Fatalf("host links = %d, want 3", len(hostLinks))
	}

	// host0 → spine1-linux
	if hostLinks[0].B.Device != "host1" {
		t.Errorf("hostLinks[0].B.Device = %q, want host1", hostLinks[0].B.Device)
	}
	if hostLinks[0].A.Device != "spine1-linux" {
		t.Errorf("hostLinks[0].A.Device = %q, want spine1-linux", hostLinks[0].A.Device)
	}

	// host1 → spine2-linux
	if hostLinks[1].B.Device != "host2" {
		t.Errorf("hostLinks[1].B.Device = %q, want host2", hostLinks[1].B.Device)
	}
	if hostLinks[1].A.Device != "spine2-linux" {
		t.Errorf("hostLinks[1].A.Device = %q, want spine2-linux", hostLinks[1].A.Device)
	}

	// host2 → leaf1-linux
	if hostLinks[2].B.Device != "host3" {
		t.Errorf("hostLinks[2].B.Device = %q, want host3", hostLinks[2].B.Device)
	}
	if hostLinks[2].A.Device != "leaf1-linux" {
		t.Errorf("hostLinks[2].A.Device = %q, want leaf1-linux", hostLinks[2].A.Device)
	}
}

func TestHosts_Redundancy(t *testing.T) {
	// 2 spines, 2 leaves, 1 host per switch, 2 ports per host
	// host0: home=spine1(0), port0→spine1(0), port1→spine2(1)
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 2, HostsPerSwitch: 1, HostsOnSpines: true, PortsPerHost: 2})

	// 4 switches * 1 host * 2 ports = 8 host links
	var hostLinks []Link
	for _, l := range tt.Links {
		if isHostEndpoint(l.A) || isHostEndpoint(l.B) {
			hostLinks = append(hostLinks, l)
		}
	}
	if len(hostLinks) != 8 {
		t.Fatalf("host links = %d, want 8", len(hostLinks))
	}

	// Verify host1 (index 0, home=spine1) connects to spine1 and spine2
	var h1Links []Link
	for _, l := range hostLinks {
		if l.A.Device == "host1" || l.B.Device == "host1" {
			h1Links = append(h1Links, l)
		}
	}
	if len(h1Links) != 2 {
		t.Fatalf("host1 links = %d, want 2", len(h1Links))
	}
	seen := map[string]bool{}
	for _, l := range h1Links {
		other := l.A.Device
		if l.A.Device == "host1" {
			other = l.B.Device
		}
		seen[other] = true
	}
	if !seen["spine1-linux"] {
		t.Error("host1 should connect to spine1-linux")
	}
	if !seen["spine2-linux"] {
		t.Error("host1 should connect to spine2-linux")
	}
}

func TestHosts_NoHosts(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 2})
	if len(tt.Hosts) != 0 {
		t.Errorf("Hosts = %d, want 0", len(tt.Hosts))
	}
}

func TestHosts_SwitchPortAllocation(t *testing.T) {
	// 1 spine, 2 leaves, 1 host per switch
	// spine1 has 2 switch ports (for 2 leaves) + 1 host port = 3 total
	// leaf1 has 1 switch port (for 1 spine) + 1 host port = 2 total
	// leaf2 has 1 switch port (for 1 spine) + 1 host port = 2 total
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2, HostsPerSwitch: 1, HostsOnSpines: true})

	for _, sw := range tt.Switches {
		switch sw.Name {
		case "spine1":
			if len(sw.Ports) != 3 {
				t.Errorf("spine1 ports = %d, want 3 (2 switch + 1 host)", len(sw.Ports))
			}
			if sw.Ports[2] != "Ethernet8" {
				t.Errorf("spine1.Ports[2] = %q, want Ethernet8", sw.Ports[2])
			}
		case "leaf1":
			if len(sw.Ports) != 2 {
				t.Errorf("leaf1 ports = %d, want 2 (1 switch + 1 host)", len(sw.Ports))
			}
			if sw.Ports[1] != "Ethernet4" {
				t.Errorf("leaf1.Ports[1] = %q, want Ethernet4", sw.Ports[1])
			}
		case "leaf2":
			if len(sw.Ports) != 2 {
				t.Errorf("leaf2 ports = %d, want 2 (1 switch + 1 host)", len(sw.Ports))
			}
			if sw.Ports[1] != "Ethernet4" {
				t.Errorf("leaf2.Ports[1] = %q, want Ethernet4", sw.Ports[1])
			}
		}
	}
}

// isHostEndpoint returns true if the endpoint's device name starts with "host".
func isHostEndpoint(e Endpoint) bool {
	return len(e.Device) > 4 && e.Device[:4] == "host"
}

func TestVtepAddrForASN(t *testing.T) {
	tests := []struct {
		asn  int
		want string
	}{
		{1, "fd00:0:0:1::1/128"},
		{65000, "fd00:0:0:fde8::1/128"},
		{65100, "fd00:0:0:fe4c::1/128"},
		{65535, "fd00:0:0:ffff::1/128"},
	}
	for _, tc := range tests {
		if got := VtepAddrForASN(tc.asn); got != tc.want {
			t.Errorf("VtepAddrForASN(%d) = %q, want %q", tc.asn, got, tc.want)
		}
	}
}

func TestVtepAddr_SetInTopology(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2})
	for _, sw := range tt.Switches {
		if sw.VtepAddr == "" {
			t.Errorf("%s: VtepAddr is empty", sw.Name)
		}
		// Verify the address encodes the ASN
		want := VtepAddrForASN(sw.ASN)
		if sw.VtepAddr != want {
			t.Errorf("%s: VtepAddr = %q, want %q", sw.Name, sw.VtepAddr, want)
		}
	}
}

func TestSpineLeaf_MultiLink(t *testing.T) {
	// 2 spines, 4 leaves, 4 links per pair
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 4, Links: 4})

	if len(tt.Switches) != 6 {
		t.Fatalf("Switches = %d, want 6", len(tt.Switches))
	}
	// Total links: 2 spines × 4 leaves × 4 links = 32
	if len(tt.Links) != 32 {
		t.Fatalf("Links = %d, want 32", len(tt.Links))
	}

	// Each spine has 4 leaves × 4 links = 16 ports
	for _, sw := range tt.Switches {
		if sw.Role == "spine" && len(sw.Ports) != 16 {
			t.Errorf("%s ports = %d, want 16", sw.Name, len(sw.Ports))
		}
		// Each leaf has 2 spines × 4 links = 8 ports
		if sw.Role == "leaf" && len(sw.Ports) != 8 {
			t.Errorf("%s ports = %d, want 8", sw.Name, len(sw.Ports))
		}
	}
}

func TestSpineLeaf_MultiLink_PortGrouping(t *testing.T) {
	// 2 spines, 2 leaves, 3 links per pair
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 2, Links: 3})

	var spine1, leaf1, leaf2 *Switch
	for i := range tt.Switches {
		switch tt.Switches[i].Name {
		case "spine1":
			spine1 = &tt.Switches[i]
		case "leaf1":
			leaf1 = &tt.Switches[i]
		case "leaf2":
			leaf2 = &tt.Switches[i]
		}
	}

	// spine1: leaf1 uses indices [0,3), leaf2 uses [3,6)
	wantSpine1 := []string{"Ethernet0", "Ethernet4", "Ethernet8", "Ethernet12", "Ethernet16", "Ethernet20"}
	if len(spine1.Ports) != 6 {
		t.Fatalf("spine1 ports = %d, want 6", len(spine1.Ports))
	}
	for i, want := range wantSpine1 {
		if spine1.Ports[i] != want {
			t.Errorf("spine1.Ports[%d] = %q, want %q", i, spine1.Ports[i], want)
		}
	}

	// leaf1: spine1 uses indices [0,3), spine2 uses [3,6)
	wantLeaf1 := []string{"Ethernet0", "Ethernet4", "Ethernet8", "Ethernet12", "Ethernet16", "Ethernet20"}
	if len(leaf1.Ports) != 6 {
		t.Fatalf("leaf1 ports = %d, want 6", len(leaf1.Ports))
	}
	for i, want := range wantLeaf1 {
		if leaf1.Ports[i] != want {
			t.Errorf("leaf1.Ports[%d] = %q, want %q", i, leaf1.Ports[i], want)
		}
	}

	// leaf2: spine1 uses indices [0,3), spine2 uses [3,6)
	if len(leaf2.Ports) != 6 {
		t.Fatalf("leaf2 ports = %d, want 6", len(leaf2.Ports))
	}
}

func TestSpineLeaf_MultiLink_Endpoints(t *testing.T) {
	// 1 spine, 2 leaves, 2 links per pair
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2, Links: 2})

	// 1 spine × 2 leaves × 2 links = 4 links
	if len(tt.Links) != 4 {
		t.Fatalf("Links = %d, want 4", len(tt.Links))
	}

	// Link 0: spine1:Ethernet0 <-> leaf1:Ethernet0
	l := tt.Links[0]
	if l.A.Device != "spine1-linux" || l.A.Port != "Ethernet0" {
		t.Errorf("link[0].A = %s:%s, want spine1-linux:Ethernet0", l.A.Device, l.A.Port)
	}
	if l.B.Device != "leaf1-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[0].B = %s:%s, want leaf1-linux:Ethernet0", l.B.Device, l.B.Port)
	}

	// Link 1: spine1:Ethernet4 <-> leaf1:Ethernet4
	l = tt.Links[1]
	if l.A.Device != "spine1-linux" || l.A.Port != "Ethernet4" {
		t.Errorf("link[1].A = %s:%s, want spine1-linux:Ethernet4", l.A.Device, l.A.Port)
	}
	if l.B.Device != "leaf1-linux" || l.B.Port != "Ethernet4" {
		t.Errorf("link[1].B = %s:%s, want leaf1-linux:Ethernet4", l.B.Device, l.B.Port)
	}

	// Link 2: spine1:Ethernet8 <-> leaf2:Ethernet0
	l = tt.Links[2]
	if l.A.Device != "spine1-linux" || l.A.Port != "Ethernet8" {
		t.Errorf("link[2].A = %s:%s, want spine1-linux:Ethernet8", l.A.Device, l.A.Port)
	}
	if l.B.Device != "leaf2-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[2].B = %s:%s, want leaf2-linux:Ethernet0", l.B.Device, l.B.Port)
	}

	// Link 3: spine1:Ethernet12 <-> leaf2:Ethernet4
	l = tt.Links[3]
	if l.A.Device != "spine1-linux" || l.A.Port != "Ethernet12" {
		t.Errorf("link[3].A = %s:%s, want spine1-linux:Ethernet12", l.A.Device, l.A.Port)
	}
	if l.B.Device != "leaf2-linux" || l.B.Port != "Ethernet4" {
		t.Errorf("link[3].B = %s:%s, want leaf2-linux:Ethernet4", l.B.Device, l.B.Port)
	}
}

func TestMesh_MultiLink(t *testing.T) {
	// 3 nodes, 2 links per pair
	tt := mustGenerate(t, Options{Mesh: true, Nodes: 3, Links: 2})

	if len(tt.Switches) != 3 {
		t.Fatalf("Switches = %d, want 3", len(tt.Switches))
	}
	// C(3,2) × 2 = 6 links
	if len(tt.Links) != 6 {
		t.Fatalf("Links = %d, want 6", len(tt.Links))
	}
	// Each node: 2 peers × 2 links = 4 ports
	for _, sw := range tt.Switches {
		if len(sw.Ports) != 4 {
			t.Errorf("%s ports = %d, want 4", sw.Name, len(sw.Ports))
		}
	}
}

func TestRing_MultiLink(t *testing.T) {
	// 4 nodes, 2 links per neighbor
	tt := mustGenerate(t, Options{Ring: true, Nodes: 4, Links: 2})

	if len(tt.Switches) != 4 {
		t.Fatalf("Switches = %d, want 4", len(tt.Switches))
	}
	// 4 nodes × 2 links per edge = 8 links
	if len(tt.Links) != 8 {
		t.Fatalf("Links = %d, want 8", len(tt.Links))
	}
	// Each node: 2 neighbors × 2 links = 4 ports
	for _, sw := range tt.Switches {
		if len(sw.Ports) != 4 {
			t.Errorf("%s ports = %d, want 4", sw.Name, len(sw.Ports))
		}
	}
}

func TestRing_MultiLink_Endpoints(t *testing.T) {
	// 3 nodes, 2 links per neighbor
	tt := mustGenerate(t, Options{Ring: true, Nodes: 3, Links: 2})

	// 3 edges × 2 links = 6 links
	if len(tt.Links) != 6 {
		t.Fatalf("Links = %d, want 6", len(tt.Links))
	}

	// Edge node1→node2: links at node1 ports [2,4) → node2 ports [0,2)
	// Link 0: node1:Ethernet8 <-> node2:Ethernet0
	l := tt.Links[0]
	if l.A.Device != "node1-linux" || l.A.Port != "Ethernet8" {
		t.Errorf("link[0].A = %s:%s, want node1-linux:Ethernet8", l.A.Device, l.A.Port)
	}
	if l.B.Device != "node2-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[0].B = %s:%s, want node2-linux:Ethernet0", l.B.Device, l.B.Port)
	}
	// Link 1: node1:Ethernet12 <-> node2:Ethernet4
	l = tt.Links[1]
	if l.A.Device != "node1-linux" || l.A.Port != "Ethernet12" {
		t.Errorf("link[1].A = %s:%s, want node1-linux:Ethernet12", l.A.Device, l.A.Port)
	}
	if l.B.Device != "node2-linux" || l.B.Port != "Ethernet4" {
		t.Errorf("link[1].B = %s:%s, want node2-linux:Ethernet4", l.B.Device, l.B.Port)
	}

	// Edge node3→node1 (wraparound): links at node3 ports [2,4) → node1 ports [0,2)
	// Link 4: node3:Ethernet8 <-> node1:Ethernet0
	l = tt.Links[4]
	if l.A.Device != "node3-linux" || l.A.Port != "Ethernet8" {
		t.Errorf("link[4].A = %s:%s, want node3-linux:Ethernet8", l.A.Device, l.A.Port)
	}
	if l.B.Device != "node1-linux" || l.B.Port != "Ethernet0" {
		t.Errorf("link[4].B = %s:%s, want node1-linux:Ethernet0", l.B.Device, l.B.Port)
	}
}

func TestLinks_ExceedsMaxPorts(t *testing.T) {
	// 2 spines, 15 leaves, 2 links → spine needs 30 ports (ok), but let's push over
	// 2 spines, 16 leaves, 2 links → spine needs 32 ports (> 30)
	_, err := Generate(Options{Spines: 2, Leaves: 16, Links: 2})
	if err == nil {
		t.Error("expected error: 16 leaves × 2 links = 32 ports > 30 max")
	}

	// Mesh: 16 nodes, 2 links → 30 ports (ok), 17 nodes, 2 links → 32 ports
	_, err = Generate(Options{Mesh: true, Nodes: 17, Links: 2})
	if err == nil {
		t.Error("expected error: 17 nodes × 2 links - 2 = 32 ports > 30 max")
	}

	// Ring: 16 links → 32 ports > 30
	_, err = Generate(Options{Ring: true, Nodes: 3, Links: 16})
	if err == nil {
		t.Error("expected error: ring with 16 links needs 32 ports > 30 max")
	}
}

func TestLinks_DefaultToOne(t *testing.T) {
	// Links: 0 should be treated as 1
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 2, Links: 0})
	if len(tt.Links) != 4 {
		t.Fatalf("Links = %d, want 4 (default 1 per pair)", len(tt.Links))
	}
}

func TestLocatorPrefixForASN(t *testing.T) {
	tests := []struct {
		asn  int
		want string
	}{
		{65000, "fd00:0:0:fde8::/64"},
		{65001, "fd00:0:0:fde9::/64"},
		{65100, "fd00:0:0:fe4c::/64"},
		{1, "fd00:0:0:1::/64"},
	}
	for _, tc := range tests {
		if got := LocatorPrefixForASN(tc.asn); got != tc.want {
			t.Errorf("LocatorPrefixForASN(%d) = %q, want %q", tc.asn, got, tc.want)
		}
	}
}

func TestOverlayPrefixForASN(t *testing.T) {
	tests := []struct {
		asn  int
		want string
	}{
		{65000, "2001:db8:fde8::/64"},
		{65001, "2001:db8:fde9::/64"},
		{65100, "2001:db8:fe4c::/64"},
		{1, "2001:db8:1::/64"},
	}
	for _, tc := range tests {
		if got := OverlayPrefixForASN(tc.asn); got != tc.want {
			t.Errorf("OverlayPrefixForASN(%d) = %q, want %q", tc.asn, got, tc.want)
		}
	}
}

func TestVtepBareAddr(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"fd00:0:0:fde8::1/128", "fd00:0:0:fde8::1"},
		{"fd00:0:0:fe4c::1/128", "fd00:0:0:fe4c::1"},
		{"fd00:0:0:1::1/128", "fd00:0:0:1::1"},
	}
	for _, tc := range tests {
		if got := VtepBareAddr(tc.addr); got != tc.want {
			t.Errorf("VtepBareAddr(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestSRv6_TopologyFlag(t *testing.T) {
	// SRv6 disabled (default)
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2, SRv6: false})
	if tt.SRv6 {
		t.Error("SRv6 = true, want false when flag is not set")
	}
	for _, sw := range tt.Switches {
		if sw.OverlayPrefix != "" {
			t.Errorf("%s.OverlayPrefix = %q, want empty when SRv6 disabled", sw.Name, sw.OverlayPrefix)
		}
	}

	// SRv6 enabled
	tt = mustGenerate(t, Options{Spines: 1, Leaves: 2, SRv6: true})
	if !tt.SRv6 {
		t.Error("SRv6 = false, want true when flag is set")
	}
	for _, sw := range tt.Switches {
		want := OverlayPrefixForASN(sw.ASN)
		if sw.OverlayPrefix != want {
			t.Errorf("%s.OverlayPrefix = %q, want %q", sw.Name, sw.OverlayPrefix, want)
		}
	}
}

func TestSidAddrForASN(t *testing.T) {
	tests := []struct {
		asn  int
		want string
	}{
		{65000, "fd00:0:0:fde8::100"},
		{65100, "fd00:0:0:fe4c::100"},
		{65101, "fd00:0:0:fe4d::100"},
		{1, "fd00:0:0:1::100"},
	}
	for _, tc := range tests {
		if got := SidAddrForASN(tc.asn); got != tc.want {
			t.Errorf("SidAddrForASN(%d) = %q, want %q", tc.asn, got, tc.want)
		}
	}
}

func TestOverlayIPv4ForASN(t *testing.T) {
	// Expected values for the common lab ASNs (ring starts at 65000, clos
	// leaves at 65100). The old asn-65100+1 scheme produced negative octets
	// for the ring ASNs; the fixed scheme keeps every value in 1..255.
	tests := []struct {
		asn  int
		want string
	}{
		{65000, "192.168.1.1/24"},
		{65004, "192.168.5.1/24"},
		{65100, "192.168.101.1/24"},
		{65103, "192.168.104.1/24"},
	}
	for _, tc := range tests {
		if got := OverlayIPv4ForASN(tc.asn); got != tc.want {
			t.Errorf("OverlayIPv4ForASN(%d) = %q, want %q", tc.asn, got, tc.want)
		}
	}

	// Every ASN must map to a syntactically valid IPv4 CIDR (the original bug
	// emitted negative octets for ring/mesh ASNs below 65100).
	for _, asn := range []int{0, 1, 64999, 65000, 65004, 65100, 65103, 65535, 70000, 1000000} {
		got := OverlayIPv4ForASN(asn)
		if _, _, err := net.ParseCIDR(got); err != nil {
			t.Errorf("OverlayIPv4ForASN(%d) = %q is not a valid CIDR: %v", asn, got, err)
		}
	}

	// Consecutive ASNs must map to unique /24s within a single topology-sized
	// window (a lab has far fewer than 255 nodes).
	seen := make(map[string]bool)
	for asn := 65000; asn < 65000+255; asn++ {
		got := OverlayIPv4ForASN(asn)
		if seen[got] {
			t.Fatalf("duplicate overlay /24 %q within a 255-ASN window (ASN %d)", got, asn)
		}
		seen[got] = true
	}
}

func TestSRv6_Mesh(t *testing.T) {
	tt := mustGenerate(t, Options{Mesh: true, Nodes: 3, SRv6: true})
	if !tt.SRv6 {
		t.Fatal("SRv6 = false, want true")
	}
	if len(tt.Switches) != 3 {
		t.Fatalf("Switches = %d, want 3", len(tt.Switches))
	}
	// Each node should have a unique overlay prefix
	seen := make(map[string]bool)
	for _, sw := range tt.Switches {
		if sw.OverlayPrefix == "" {
			t.Errorf("%s.OverlayPrefix is empty", sw.Name)
		}
		if seen[sw.OverlayPrefix] {
			t.Errorf("duplicate overlay prefix %q", sw.OverlayPrefix)
		}
		seen[sw.OverlayPrefix] = true
	}
}

func TestNetnsSuffix_DeviceNames(t *testing.T) {
	tests := []struct {
		netnsSuffix string
		want        string
	}{
		{"", "spine1-linux"}, // empty defaults to DefaultNetnsSuffix
		{"net", "spine1-net"},
	}
	for _, tc := range tests {
		tt, err := Generate(Options{Spines: 1, Leaves: 1, HostsPerSwitch: 1, NetnsSuffix: tc.netnsSuffix})
		if err != nil {
			t.Fatalf("Generate(netnsSuffix=%q) error: %v", tc.netnsSuffix, err)
		}
		if got := tt.DeviceNetnsName("spine1"); got != tc.want {
			t.Errorf("netnsSuffix=%q: DeviceNetnsName(spine1) = %q, want %q", tc.netnsSuffix, got, tc.want)
		}
		suffix := "-" + tt.NetnsSuffix
		for _, l := range tt.Links {
			for _, ep := range []Endpoint{l.A, l.B} {
				isHost := strings.HasPrefix(ep.Device, "host")
				if !isHost && !strings.HasSuffix(ep.Device, suffix) {
					t.Errorf("netnsSuffix=%q: switch endpoint %q missing suffix %q", tc.netnsSuffix, ep.Device, suffix)
				}
			}
		}
	}
}

func TestHosts_LeavesOnlyByDefault(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 2, HostsPerSwitch: 1})

	if len(tt.Hosts) != 2 {
		t.Fatalf("Hosts = %d, want 2 (one per leaf, none on spines)", len(tt.Hosts))
	}
	for _, sw := range tt.Switches {
		want := 2 // two spines or two leaves of fabric ports
		if sw.Role == "leaf" {
			want = 3 // plus one host port
		}
		if len(sw.Ports) != want {
			t.Errorf("%s ports = %d, want %d", sw.Name, len(sw.Ports), want)
		}
	}
	for _, a := range tt.Attachments {
		if !strings.HasPrefix(a.Switch, "leaf") {
			t.Errorf("%s attached to %s, want a leaf", a.Host, a.Switch)
		}
	}
}

func TestAddrIndex(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 4})
	want := map[string]int{
		"spine1": SpineHostBase, "spine2": SpineHostBase + 1,
		"leaf1": 1, "leaf2": 2, "leaf3": 3, "leaf4": 4,
	}
	for _, sw := range tt.Switches {
		if sw.AddrIndex != want[sw.Name] {
			t.Errorf("%s AddrIndex = %d, want %d", sw.Name, sw.AddrIndex, want[sw.Name])
		}
	}

	for _, opts := range []Options{{Ring: true, Nodes: 3}, {Mesh: true, Nodes: 3}} {
		tt := mustGenerate(t, opts)
		for i, sw := range tt.Switches {
			if sw.AddrIndex != i+1 {
				t.Errorf("%+v: %s AddrIndex = %d, want %d", opts, sw.Name, sw.AddrIndex, i+1)
			}
		}
	}
}

func TestHostAttachments_DefaultAddressing(t *testing.T) {
	// 2 spines, 2 leaves, 2 hosts per leaf. Each leaf uses Ethernet0/4
	// for the spines, so hosts land on Ethernet8 and Ethernet12.
	tt := mustGenerate(t, Options{Spines: 2, Leaves: 2, HostsPerSwitch: 2})

	want := []HostAttachment{
		{"leaf1", "Ethernet8", "host1", "Ethernet0", "eth1", 1, "10.1.1.1/24", "10.1.1.2/24", "fd00:0:1:1::1/64", "fd00:0:1:1::2/64"},
		{"leaf1", "Ethernet12", "host2", "Ethernet0", "eth1", 2, "10.1.2.1/24", "10.1.2.2/24", "fd00:0:1:2::1/64", "fd00:0:1:2::2/64"},
		{"leaf2", "Ethernet8", "host3", "Ethernet0", "eth1", 1, "10.2.1.1/24", "10.2.1.2/24", "fd00:0:2:1::1/64", "fd00:0:2:1::2/64"},
		{"leaf2", "Ethernet12", "host4", "Ethernet0", "eth1", 2, "10.2.2.1/24", "10.2.2.2/24", "fd00:0:2:2::1/64", "fd00:0:2:2::2/64"},
	}
	if len(tt.Attachments) != len(want) {
		t.Fatalf("Attachments = %d, want %d", len(tt.Attachments), len(want))
	}
	for i, w := range want {
		if tt.Attachments[i] != w {
			t.Errorf("Attachments[%d] = %+v\nwant %+v", i, tt.Attachments[i], w)
		}
	}

	// Every attachment must match a generated link.
	for _, a := range tt.Attachments {
		found := false
		for _, l := range tt.Links {
			if l.A == (Endpoint{tt.DeviceNetnsName(a.Switch), a.SwitchPort}) && l.B == (Endpoint{a.Host, a.HostPort}) {
				found = true
			}
		}
		if !found {
			t.Errorf("no link for attachment %+v", a)
		}
	}
}

func TestHostAttachments_SpinesStartAtSpineHostBase(t *testing.T) {
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 1, HostsPerSwitch: 1, HostsOnSpines: true})
	got := map[string]HostAttachment{}
	for _, a := range tt.Attachments {
		got[a.Switch] = a
	}
	wantV4 := fmt.Sprintf("10.%d.1.1/24", SpineHostBase)
	wantV6 := fmt.Sprintf("fd00:0:%d:1::2/64", SpineHostBase)
	if a := got["spine1"]; a.SwitchIPv4 != wantV4 || a.HostIPv6 != wantV6 {
		t.Errorf("spine1 attachment = %+v, want %s / %s", a, wantV4, wantV6)
	}
	if a := got["leaf1"]; a.SwitchIPv4 != "10.1.1.1/24" || a.HostIPv6 != "fd00:0:1:1::2/64" {
		t.Errorf("leaf1 attachment = %+v, want 10.1.1.x / fd00:0:1:1::x", a)
	}
}

func TestHostAttachments_MultiPort(t *testing.T) {
	// One host per leaf, two ports each: host1 → leaf1, leaf2;
	// host2 → leaf2, leaf1. Each leaf then has two attachments, and the
	// host index counts attachments per switch.
	tt := mustGenerate(t, Options{Spines: 1, Leaves: 2, HostsPerSwitch: 1, PortsPerHost: 2})
	if len(tt.Attachments) != 4 {
		t.Fatalf("Attachments = %d, want 4", len(tt.Attachments))
	}
	seen := map[string]bool{}
	for _, a := range tt.Attachments {
		if seen[a.SwitchIPv4] {
			t.Errorf("duplicate switch address %s", a.SwitchIPv4)
		}
		seen[a.SwitchIPv4] = true
	}
	if a := tt.Attachments[1]; a.Host != "host1" || a.HostIface != "eth2" || a.Switch != "leaf2" {
		t.Errorf("host1 second port = %+v, want eth2 on leaf2", a)
	}
}

// Every default address must sit inside the pools the fabric advertises,
// and IPv6 must stay out of the reserved loopback/locator block.
func TestHostAttachments_InsideAdvertisedPools(t *testing.T) {
	_, v4Pool, _ := net.ParseCIDR(HostIPv4Pool)
	_, v6Pool, _ := net.ParseCIDR(HostIPv6Pool)
	_, v6Reserved, _ := net.ParseCIDR(HostIPv6Reserved)

	tt := mustGenerate(t, Options{Spines: 2, Leaves: 4, HostsPerSwitch: 2, HostsOnSpines: true})
	for _, a := range tt.Attachments {
		for _, cidr := range []string{a.SwitchIPv4, a.HostIPv4} {
			ip, _, err := net.ParseCIDR(cidr)
			if err != nil || !v4Pool.Contains(ip) {
				t.Errorf("%s: %s not inside %s (err %v)", a.Host, cidr, HostIPv4Pool, err)
			}
		}
		for _, cidr := range []string{a.SwitchIPv6, a.HostIPv6} {
			ip, _, err := net.ParseCIDR(cidr)
			if err != nil || !v6Pool.Contains(ip) || v6Reserved.Contains(ip) {
				t.Errorf("%s: %s not inside %s minus %s (err %v)", a.Host, cidr, HostIPv6Pool, HostIPv6Reserved, err)
			}
		}
	}
}

func TestHostKernelIface(t *testing.T) {
	for port, want := range map[string]string{"Ethernet0": "eth1", "Ethernet4": "eth2", "Ethernet116": "eth30"} {
		if got, err := HostKernelIface(port); err != nil || got != want {
			t.Errorf("HostKernelIface(%q) = %q, %v; want %q", port, got, err, want)
		}
	}
	for _, bad := range []string{"eth1", "Ethernet2", ""} {
		if _, err := HostKernelIface(bad); err == nil {
			t.Errorf("HostKernelIface(%q) should fail", bad)
		}
	}
}

func TestHosts_AddrIndexWarning(t *testing.T) {
	// Up to SpineHostBase-1 nodes with hosts: no warning.
	tt := mustGenerate(t, Options{Ring: true, Nodes: SpineHostBase - 1, HostsPerSwitch: 1})
	if len(tt.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tt.Warnings)
	}

	// One more and the last node is numbered SpineHostBase: a warning,
	// not an error, because no spine has hosts to collide with.
	tt = mustGenerate(t, Options{Ring: true, Nodes: SpineHostBase, HostsPerSwitch: 1})
	if len(tt.Warnings) != 1 || !strings.Contains(tt.Warnings[0], fmt.Sprintf("node%d", SpineHostBase)) {
		t.Errorf("Warnings = %v, want one naming node%d", tt.Warnings, SpineHostBase)
	}

	// Without hosts, addressing doesn't matter: no warning.
	tt = mustGenerate(t, Options{Ring: true, Nodes: SpineHostBase})
	if len(tt.Warnings) != 0 {
		t.Errorf("unexpected warnings without hosts: %v", tt.Warnings)
	}
}

func TestHosts_AddrIndexCollision(t *testing.T) {
	// Spine-leaf can't reach SpineHostBase leaves through Generate (spine
	// port limit), so build the topology directly: with hosts on spines,
	// leaf<SpineHostBase> and spine1 would share 10.<SpineHostBase>.x.0/24.
	tt := &Topology{NetnsSuffix: DefaultNetnsSuffix}
	tt.Switches = append(tt.Switches, Switch{Name: "spine1", Role: "spine"})
	for l := 1; l <= SpineHostBase; l++ {
		tt.Switches = append(tt.Switches, Switch{Name: fmt.Sprintf("leaf%d", l), Role: "leaf"})
	}
	assignAddrIndexes(tt)
	if err := addHosts(tt, 1, 1, true); err == nil || !strings.Contains(err.Error(), "spine1") {
		t.Errorf("addHosts error = %v, want a collision naming spine1", err)
	}
}

func TestHosts_AddrIndexOctetLimit(t *testing.T) {
	if _, err := Generate(Options{Ring: true, Nodes: 256, HostsPerSwitch: 1}); err == nil {
		t.Error("expected an error: node256's address index doesn't fit in an IPv4 octet")
	}
}

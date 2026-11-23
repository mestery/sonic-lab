/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonic-lab/tgen/internal/topo"
)

func TestKernelPortName(t *testing.T) {
	tests := []struct {
		port string
		want string
	}{
		{"Ethernet0", "Ethernet0"},
		{"Ethernet4", "Ethernet4"},
		{"Ethernet8", "Ethernet8"},
		{"Ethernet28", "Ethernet28"},
		{"Ethernet116", "Ethernet116"},
	}
	for _, tc := range tests {
		if got := kernelPortName(tc.port); got != tc.want {
			t.Errorf("kernelPortName(%q) = %q, want %q", tc.port, got, tc.want)
		}
	}
}

func mustGenerate(t *testing.T, opts topo.Options) *topo.Topology {
	t.Helper()
	tt, err := topo.Generate(opts)
	if err != nil {
		t.Fatalf("Generate(%+v) error: %v", opts, err)
	}
	return tt
}

func TestGenerateFRRConfig_SpineLeaf(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 2})
	w := New("/tmp/test", tt)

	// Test spine1 config
	var spine *topo.Switch
	for i := range tt.Switches {
		if tt.Switches[i].Name == "spine1" {
			spine = &tt.Switches[i]
		}
	}
	if spine == nil {
		t.Fatal("spine1 not found")
	}

	cfg := w.generateFRRConfig(*spine)

	// Should contain hostname
	if !strings.Contains(cfg, "hostname spine1") {
		t.Errorf("spine1 config missing 'hostname spine1'")
	}

	// Should contain the router bgp statement with correct ASN
	if !strings.Contains(cfg, "router bgp 65000") {
		t.Errorf("spine1 config missing 'router bgp 65000'")
	}

	// Should contain the VTEP address (ASN 65000 = 0xFDE8)
	if !strings.Contains(cfg, "fd00:0:0:fde8::1/128") {
		t.Errorf("spine1 config missing VTEP address")
	}

	// Should have peer-group definition
	if !strings.Contains(cfg, "neighbor TRANSIT peer-group") {
		t.Errorf("spine1 config missing 'neighbor TRANSIT peer-group'")
	}
	if !strings.Contains(cfg, "neighbor TRANSIT remote-as external") {
		t.Errorf("spine1 config missing 'neighbor TRANSIT remote-as external'")
	}
	if !strings.Contains(cfg, "neighbor TRANSIT ebgp-multihop") {
		t.Errorf("spine1 config missing 'neighbor TRANSIT ebgp-multihop'")
	}

	// Should have unnumbered neighbors via interface keyword
	if !strings.Contains(cfg, "neighbor Ethernet0 interface peer-group TRANSIT") {
		t.Errorf("spine1 config missing 'neighbor Ethernet0 interface peer-group TRANSIT'")
	}
	if !strings.Contains(cfg, "neighbor Ethernet4 interface peer-group TRANSIT") {
		t.Errorf("spine1 config missing 'neighbor Ethernet4 interface peer-group TRANSIT'")
	}

	// Should have BGP tuning statements
	if !strings.Contains(cfg, "no bgp ebgp-requires-policy") {
		t.Errorf("spine1 config missing 'no bgp ebgp-requires-policy'")
	}
	if !strings.Contains(cfg, "bgp disable-ebgp-connected-route-check") {
		t.Errorf("spine1 config missing 'bgp disable-ebgp-connected-route-check'")
	}
	if !strings.Contains(cfg, "bgp bestpath as-path multipath-relax") {
		t.Errorf("spine1 config missing 'bgp bestpath as-path multipath-relax'")
	}

	// Should have IPv6 unicast address family with activate and next-hop-self
	if !strings.Contains(cfg, "address-family ipv6 unicast") {
		t.Errorf("spine1 config missing 'address-family ipv6 unicast'")
	}
	if !strings.Contains(cfg, "neighbor TRANSIT activate") {
		t.Errorf("spine1 config missing 'neighbor TRANSIT activate'")
	}
	if !strings.Contains(cfg, "neighbor TRANSIT next-hop-self") {
		t.Errorf("spine1 config missing 'neighbor TRANSIT next-hop-self'")
	}
	if !strings.Contains(cfg, "network fd00:0:0:fde8::1/128") {
		t.Errorf("spine1 config missing network statement")
	}
}

func TestGenerateFRRConfig_Leaf(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 2, Leaves: 1})
	w := New("/tmp/test", tt)

	var leaf *topo.Switch
	for i := range tt.Switches {
		if tt.Switches[i].Name == "leaf1" {
			leaf = &tt.Switches[i]
		}
	}
	if leaf == nil {
		t.Fatal("leaf1 not found")
	}

	cfg := w.generateFRRConfig(*leaf)

	// Leaf1 connects to spine1 (Ethernet0) and spine2 (Ethernet4)
	if !strings.Contains(cfg, "neighbor Ethernet0 interface peer-group TRANSIT") {
		t.Errorf("leaf1 config missing 'neighbor Ethernet0 interface peer-group TRANSIT'")
	}
	if !strings.Contains(cfg, "neighbor Ethernet4 interface peer-group TRANSIT") {
		t.Errorf("leaf1 config missing 'neighbor Ethernet4 interface peer-group TRANSIT'")
	}

	// Should have correct VTEP address for leaf1 (ASN 65100 = 0xFE4C)
	if !strings.Contains(cfg, "fd00:0:0:fe4c::1/128") {
		t.Errorf("leaf1 config missing VTEP address fd00:0:0:fe4c::1/128")
	}
}

func TestGenerateFRRConfig_Mesh(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Mesh: true, Nodes: 3})
	w := New("/tmp/test", tt)

	// node1 connects to node2 (Ethernet0) and node3 (Ethernet4)
	var node1 *topo.Switch
	for i := range tt.Switches {
		if tt.Switches[i].Name == "node1" {
			node1 = &tt.Switches[i]
		}
	}
	cfg := w.generateFRRConfig(*node1)

	if !strings.Contains(cfg, "neighbor Ethernet0 interface peer-group TRANSIT") {
		t.Errorf("node1 config missing 'neighbor Ethernet0 interface peer-group TRANSIT' (node2)")
	}
	if !strings.Contains(cfg, "neighbor Ethernet4 interface peer-group TRANSIT") {
		t.Errorf("node1 config missing 'neighbor Ethernet4 interface peer-group TRANSIT' (node3)")
	}
}

func TestGenerateFRRConfig_Ring(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Ring: true, Nodes: 3})
	w := New("/tmp/test", tt)

	// node1:Ethernet0 (connects to node3) and Ethernet4 (connects to node2)
	// Ring link: node[i]:Ethernet4 → node[(i+1)%n]:Ethernet0
	// node1:Ethernet4 → node2:Ethernet0
	// node3:Ethernet4 → node1:Ethernet0
	// So node1 has: Ethernet0 (connects to node3) and Ethernet4 (connects to node2)

	var node1 *topo.Switch
	for i := range tt.Switches {
		if tt.Switches[i].Name == "node1" {
			node1 = &tt.Switches[i]
		}
	}
	cfg := w.generateFRRConfig(*node1)

	// node1 should have exactly 2 neighbors (ring topology)
	if !strings.Contains(cfg, "neighbor Ethernet0 interface peer-group TRANSIT") {
		t.Errorf("node1 config missing 'neighbor Ethernet0 interface peer-group TRANSIT' (node3 via ring wrap)")
	}
	if !strings.Contains(cfg, "neighbor Ethernet4 interface peer-group TRANSIT") {
		t.Errorf("node1 config missing 'neighbor Ethernet4 interface peer-group TRANSIT' (node2)")
	}
}

func TestGenerateFRRConfig_ExcludesHostLinks(t *testing.T) {
	// 1 spine, 1 leaf, 1 host per switch
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 1, HostsPerSwitch: 1})
	w := New("/tmp/test", tt)

	var spine *topo.Switch
	for i := range tt.Switches {
		if tt.Switches[i].Name == "spine1" {
			spine = &tt.Switches[i]
		}
	}

	cfg := w.generateFRRConfig(*spine)

	// spine1 has 2 ports: Ethernet0 (to leaf1) and Ethernet4 (to host1)
	// The FRR config should only include the leaf1 neighbor (Ethernet0), not the host link (Ethernet4)
	if !strings.Contains(cfg, "neighbor Ethernet0 interface peer-group TRANSIT") {
		t.Errorf("spine1 config missing 'neighbor Ethernet0 interface peer-group TRANSIT' (leaf1)")
	}
	if strings.Contains(cfg, "neighbor Ethernet4") {
		t.Errorf("spine1 config should not include host link on Ethernet4")
	}
}

func TestWriteFRRConfigs_CreatesFiles(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 2})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeFRRConfigs(); err != nil {
		t.Fatalf("writeFRRConfigs error: %v", err)
	}

	// Check that each switch has a config file
	for _, sw := range tt.Switches {
		path := filepath.Join(dir, "frr", sw.Name+".conf")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if len(data) == 0 {
			t.Errorf("%s: config file is empty", sw.Name)
		}
	}
}

func TestWriteFRRConfigs_Content(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 1})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeFRRConfigs(); err != nil {
		t.Fatalf("writeFRRConfigs error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "frr", "spine1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)

	// Verify key sections
	if !strings.Contains(cfg, "frr defaults traditional") {
		t.Error("missing 'frr defaults traditional'")
	}
	if !strings.Contains(cfg, "interface Loopback0") {
		t.Error("missing 'interface Loopback0'")
	}
	if !strings.Contains(cfg, "router bgp 65000") {
		t.Error("missing 'router bgp 65000'")
	}
	if strings.Contains(cfg, "bgp router-id") {
		t.Error("should not contain 'bgp router-id' (invalid for IPv6)")
	}
	if !strings.Contains(cfg, "line vty") {
		t.Error("missing 'line vty'")
	}
}

func TestSRv6_FRRConfig(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 2, SRv6: true})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeFRRConfigs(); err != nil {
		t.Fatalf("writeFRRConfigs error: %v", err)
	}

	// Spine should have SRv6 locator + BGP SRv6 (transit with SRv6)
	data, err := os.ReadFile(filepath.Join(dir, "frr", "spine1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	spineCfg := string(data)
	if !strings.Contains(spineCfg, "segment-routing") {
		t.Error("spine1 should contain 'segment-routing' (locator + End SID)")
	}
	if !strings.Contains(spineCfg, "locator spine1") {
		t.Error("spine1 missing 'locator spine1'")
	}
	if !strings.Contains(spineCfg, "segment-routing srv6") {
		t.Error("spine1 should contain 'segment-routing srv6' in BGP section")
	}
	if strings.Contains(spineCfg, "vrf Vrf100") {
		t.Error("spine1 should not contain 'vrf Vrf100'")
	}
	// Spines relay L3VPN between leaves
	if !strings.Contains(spineCfg, "address-family ipv4 vpn") {
		t.Error("spine1 should contain 'address-family ipv4 vpn' (for VPN relay)")
	}
	if !strings.Contains(spineCfg, "next-hop-self") {
		t.Error("spine1 should have 'next-hop-self' in ipv4 vpn AF (for relay)")
	}

	// Leaf should have full SRv6 config
	data, err = os.ReadFile(filepath.Join(dir, "frr", "leaf1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)

	// VRF
	if !strings.Contains(cfg, "vrf Vrf100") {
		t.Error("leaf1 missing 'vrf Vrf100'")
	}
	if !strings.Contains(cfg, "vni 100") {
		t.Error("leaf1 missing 'vni 100'")
	}

	// SRv6 locator + static SID
	if !strings.Contains(cfg, "segment-routing") {
		t.Error("leaf1 missing 'segment-routing' section")
	}
	if !strings.Contains(cfg, "locator leaf1") {
		t.Error("leaf1 missing 'locator leaf1'")
	}
	if !strings.Contains(cfg, "prefix fd00:0:0:fe4c::/64") {
		t.Error("leaf1 missing locator prefix fd00:0:0:fe4c::/64")
	}
	if !strings.Contains(cfg, "fd00:0:0:fe4c::100/128") {
		t.Error("leaf1 missing End SID fd00:0:0:fe4c::100/128")
	}
	if !strings.Contains(cfg, "behavior uN") {
		t.Error("leaf1 missing 'behavior uN' (End SID)")
	}

	// BGP ipv6 unicast with locator /64 network
	if !strings.Contains(cfg, "address-family ipv6 unicast") {
		t.Error("leaf1 missing 'address-family ipv6 unicast'")
	}
	if !strings.Contains(cfg, "network fd00:0:0:fe4c::/64") {
		t.Error("leaf1 missing 'network fd00:0:0:fe4c::/64' (locator /64 in BGP)")
	}

	// L3VPN address family
	if !strings.Contains(cfg, "address-family ipv4 vpn") {
		t.Error("leaf1 missing 'address-family ipv4 vpn'")
	}

	// SRv6 under BGP
	if !strings.Contains(cfg, "segment-routing srv6") {
		t.Error("leaf1 missing 'segment-routing srv6' in BGP section")
	}

	// Per-VRF BGP instance
	if !strings.Contains(cfg, "router bgp 65100 vrf Vrf100") {
		t.Error("leaf1 missing 'router bgp 65100 vrf Vrf100'")
	}
}

func TestSRv6_SharedRT(t *testing.T) {
	// Full mesh: every node is a non-spine with its own VRF. All must use the
	// SAME route-target (so they import each other's routes — a full-mesh L3VPN)
	// but a distinct per-ASN route-distinguisher.
	tt := mustGenerate(t, topo.Options{Mesh: true, Nodes: 3, SRv6: true})
	dir := t.TempDir()
	w := New(dir, tt)
	if err := w.writeFRRConfigs(); err != nil {
		t.Fatalf("writeFRRConfigs error: %v", err)
	}
	for _, sw := range tt.Switches {
		data, err := os.ReadFile(filepath.Join(dir, "frr", sw.Name+".conf"))
		if err != nil {
			t.Fatalf("read %s.conf: %v", sw.Name, err)
		}
		cfg := string(data)
		if !strings.Contains(cfg, "rt vpn both 65000:100") {
			t.Errorf("%s: missing shared route-target 'rt vpn both 65000:100'", sw.Name)
		}
		if !strings.Contains(cfg, fmt.Sprintf("rd vpn export %d:100", sw.ASN)) {
			t.Errorf("%s: missing per-ASN route-distinguisher 'rd vpn export %d:100'", sw.Name, sw.ASN)
		}
	}
}

func TestSRv6_FRRConfig_Disabled(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 2, SRv6: false})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeFRRConfigs(); err != nil {
		t.Fatalf("writeFRRConfigs error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "frr", "spine1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)

	// Verify SRv6 section is NOT present
	if strings.Contains(cfg, "segment-routing") {
		t.Error("should not contain 'segment-routing' when SRv6 is disabled")
	}
}

func TestSRv6_Routes(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 2, SRv6: true})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeSRv6Routes(); err != nil {
		t.Fatalf("writeSRv6Routes error: %v", err)
	}

	// Spine should have a route file with SRv6 locators and End SIDs
	spineRoutes, err := os.ReadFile(filepath.Join(dir, "srv6", "spine1.routes"))
	if err != nil {
		t.Fatalf("spine1 should have an SRv6 route file: %v", err)
	}
	spineRoutesStr := string(spineRoutes)
	// Spine route file should have locator /64, End SID, and overlay /128
	if !strings.Contains(spineRoutesStr, "fd00:0:0:fde8::/64") {
		t.Error("spine1 missing locator prefix fd00:0:0:fde8::/64 on Loopback0")
	}
	if !strings.Contains(spineRoutesStr, "fd00:0:0:fde8::100/128") {
		t.Error("spine1 missing End SID fd00:0:0:fde8::100/128 on Loopback0")
	}
	if !strings.Contains(spineRoutesStr, "2001:db8:fde8::1/128") {
		t.Error("spine1 missing overlay prefix /128 on Loopback0")
	}

	// Leaf1 should have a route file
	data, err := os.ReadFile(filepath.Join(dir, "srv6", "leaf1.routes"))
	if err != nil {
		t.Fatal(err)
	}
	routes := string(data)

	// Verify End SID is assigned to Loopback0
	if !strings.Contains(routes, "fd00:0:0:fe4c::100/128") {
		t.Error("missing End SID fd00:0:0:fe4c::100/128 on Loopback0")
	}

	// Verify overlay /128 is added to Loopback0
	if !strings.Contains(routes, "2001:db8:fe4c::1/128") {
		t.Error("missing overlay prefix /128 on Loopback0")
	}

	// Verify seg6 encap syntax: routes must use "replace" (idempotent on
	// re-run) and record failures in FAILED instead of swallowing them
	// with "2>/dev/null || true".
	if !strings.Contains(routes, "encap seg6 mode encap segs") {
		t.Fatal("missing seg6 encap syntax")
	}
	for _, line := range strings.Split(routes, "\n") {
		if !strings.Contains(line, "encap seg6 mode encap segs") {
			continue
		}
		if !strings.HasPrefix(line, "ip -6 route replace ") {
			t.Errorf("seg6 route must be installed with 'ip -6 route replace': %q", line)
		}
		if strings.Contains(line, "|| true") {
			t.Errorf("seg6 route failure must not be swallowed: %q", line)
		}
		if !strings.Contains(line, "|| FAILED=") {
			t.Errorf("seg6 route failure must be recorded in FAILED: %q", line)
		}
	}
	if !strings.Contains(routes, "All seg6 routes installed") {
		t.Error("missing success summary line in route script")
	}

	// leaf1 should have a route to leaf2
	if !strings.Contains(routes, "2001:db8:fe4d::/64") {
		t.Error("missing route to leaf2 overlay prefix")
	}
	if !strings.Contains(routes, "fd00:0:0:fe4d::100") {
		t.Error("missing leaf2 End SID as SRH segment")
	}
	// Should NOT have a route to spine1 (spines don't terminate SRv6)
	if strings.Contains(routes, "2001:db8:fde8::/64 encap") {
		t.Error("should not have seg6 route to spine1 overlay prefix (spines don't terminate SRv6)")
	}
	// Should NOT have a route to itself
	if strings.Contains(routes, "2001:db8:fe4c::/64 encap") {
		t.Error("should not have seg6 route to own overlay prefix")
	}
}

func TestSRv6_Routes_Mesh(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Mesh: true, Nodes: 3, SRv6: true})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeSRv6Routes(); err != nil {
		t.Fatalf("writeSRv6Routes error: %v", err)
	}

	// In a mesh of 3, node1 should have routes to node2 and node3
	data, err := os.ReadFile(filepath.Join(dir, "srv6", "node1.routes"))
	if err != nil {
		t.Fatal(err)
	}
	routes := string(data)

	// node1 ASN = 65000, node2 ASN = 65001, node3 ASN = 65002
	if !strings.Contains(routes, "fd00:0:0:fde9::100") {
		t.Error("missing node2 End SID as SRH segment")
	}
	if !strings.Contains(routes, "fd00:0:0:fdea::100") {
		t.Error("missing node3 End SID as SRH segment")
	}
	if strings.Contains(routes, "route add") {
		t.Error("seg6 routes must use 'route replace' (idempotent), not 'route add'")
	}
	// Should not route to itself
	if strings.Contains(routes, "fd00:0:0:fde8::100 encap") {
		t.Error("should not have seg6 route to own End SID")
	}
}

func TestSRv6_ComposeMount(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 1, SRv6: true})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeCompose(); err != nil {
		t.Fatalf("writeCompose error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)

	// Spine should now have a srv6 routes mount (spines terminate SRv6)
	if !strings.Contains(compose, "./srv6/spine1.routes:/srv6.routes:ro") {
		t.Error("spine1 should have a srv6 routes mount")
	}
	// Leaf1 should have the mount
	if !strings.Contains(compose, "./srv6/leaf1.routes:/srv6.routes:ro") {
		t.Error("missing srv6 routes volume mount for leaf1")
	}
}

func TestSRv6_ComposeMount_Disabled(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 1, SRv6: false})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeCompose(); err != nil {
		t.Fatalf("writeCompose error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)

	if strings.Contains(compose, "./srv6/") {
		t.Error("should not contain srv6 volume mounts when SRv6 is disabled")
	}
}

func TestCompose_EachImageBuiltOnce(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 2, Leaves: 2, HostsPerSwitch: 1, PortsPerHost: 1})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeCompose(); err != nil {
		t.Fatalf("writeCompose error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)

	// Multiple services building the same image tag race under parallel
	// "docker compose build", so each Dockerfile must appear exactly once.
	for _, df := range []string{"Dockerfile.webmesh", "Dockerfile.debian", "Dockerfile.host"} {
		if n := strings.Count(compose, "dockerfile: "+df); n != 1 {
			t.Errorf("%s referenced by %d build blocks, want 1", df, n)
		}
	}
}

func TestSRv6_EntryScript(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 1, SRv6: true})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeEntryScript(); err != nil {
		t.Fatalf("writeEntryScript error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sonic_entry.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)

	// The entry script should conditionally execute /srv6.routes, and a
	// failed route install must be reported instead of swallowed (otherwise
	// the overlay silently blackholes and the log looks clean).
	if !strings.Contains(script, "/srv6.routes") {
		t.Error("missing /srv6.routes execution in entry script")
	}
	if strings.Contains(script, "bash /srv6.routes ||") {
		t.Error("entry script must not swallow /srv6.routes failures with '|| echo'")
	}
	if !strings.Contains(script, "if bash /srv6.routes; then") {
		t.Error("entry script must branch on the /srv6.routes exit status")
	}

	// IPv6 forwarding is required on transit nodes (spines). Spines now
	// receive a /srv6.routes file, but the forwarding sysctls must still
	// live OUTSIDE the "[ -f /srv6.routes ]" guard — a regression that
	// nests them re-introduces silent cross-leaf blackholing. Assert
	// the forwarding block appears before the guard, not inside it.
	fwdIdx := strings.Index(script, "net.ipv6.conf.all.forwarding=1")
	guardIdx := strings.Index(script, "if [ -f /srv6.routes ]; then")
	switch {
	case fwdIdx < 0:
		t.Error("missing IPv6 forwarding enablement in entry script")
	case guardIdx < 0:
		t.Error("missing /srv6.routes guard in entry script")
	case fwdIdx > guardIdx:
		t.Error("IPv6 forwarding must be enabled before (and unconditionally to) the /srv6.routes guard")
	}
}

func TestNetnsSuffix_ComposeAndNeighbors(t *testing.T) {
	tt := mustGenerate(t, topo.Options{Spines: 1, Leaves: 2, HostsPerSwitch: 1, NetnsSuffix: "net"})
	dir := t.TempDir()
	w := New(dir, tt)

	if err := w.writeCompose(); err != nil {
		t.Fatalf("writeCompose error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)

	for _, want := range []string{
		"  spine1-net:\n",
		"    container_name: spine1-net\n",
		`command: ["bash", "/init_linux.sh", "spine1-net"]`,
		"      spine1-net:\n        condition: service_started\n",
		`network_mode: "service:spine1-net"`,
		`entrypoint: ["/bin/bash", "/sonic_entry.sh", "spine1-net",`,
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose missing %q", want)
		}
	}
	if strings.Contains(compose, "-linux") {
		t.Error("compose still contains '-linux' with -base net")
	}

	// BGP neighbors are found by matching link endpoints to switch devices,
	// so they must still be generated with a non-default netns suffix.
	var spine topo.Switch
	for _, sw := range tt.Switches {
		if sw.Name == "spine1" {
			spine = sw
		}
	}
	if got := len(w.switchNeighbors(spine)); got != 2 {
		t.Errorf("spine1 switchNeighbors = %d, want 2 (one per leaf, hosts excluded)", got)
	}
}

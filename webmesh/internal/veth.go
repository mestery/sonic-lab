/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package webmesh

import (
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// wmIfname returns the webmesh-side veth endpoint name for interface n,
// e.g. 3 → "wm-eth3". The reconciler assigns n per endpoint.
func wmIfname(n int) string {
	return "wm-eth" + strconv.Itoa(n)
}

// swIfname generates the temporary name for the switch-side veth endpoint
// before it is moved into the target namespace and renamed.
func swIfname(n int) string {
	return "sw-eth" + strconv.Itoa(n)
}

// containerPID returns the host PID of the init process of the named container.
func containerPID(name string) (int, error) {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Pid}}", name).Output()
	if err != nil {
		return 0, fmt.Errorf("docker inspect %s: %w", name, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parsing pid for %s: %w", name, err)
	}
	if pid == 0 {
		return 0, fmt.Errorf("container %s is not running", name)
	}
	return pid, nil
}

// linkInNS checks whether a network interface exists in the given PID's netns.
func linkInNS(pid int, ifname string) bool {
	return exec.Command("nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "show", ifname).Run() == nil
}

// linkIsUp checks whether a network interface is administratively UP
// in the given PID's netns.
func linkIsUp(pid int, ifname string) bool {
	out, err := exec.Command("nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "show", ifname).Output()
	if err != nil {
		return false
	}
	s := string(out)
	idx := strings.Index(s, "<")
	if idx < 0 {
		return false
	}
	end := strings.Index(s[idx:], ">")
	if end < 0 {
		return false
	}
	flags := s[idx : idx+end]
	for _, f := range strings.Split(flags, ",") {
		if strings.TrimSpace(f) == "UP" {
			return true
		}
	}
	return false
}

// ifindexByName returns the ifindex of a link in the current namespace.
func ifindexByName(name string) (int, error) {
	out, err := exec.Command("ip", "-j", "link", "show", name).Output()
	if err != nil {
		return 0, fmt.Errorf("ip link show %s: %w", name, err)
	}
	// The JSON output is a single object: {"ifindex":3,"ifname":"wm-eth0",...}
	// Parse minimally: find "ifindex":N
	s := string(out)
	key := `"ifindex":`
	idx := strings.Index(s, key)
	if idx < 0 {
		return 0, fmt.Errorf("ifindex not found in output for %s", name)
	}
	s = s[idx+len(key):]
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, fmt.Errorf("parsing ifindex for %s: %w", name, err)
	}
	return n, nil
}

// disableTxOffload disables TX checksum offload on ifname in the netns
// owned by pid.
//
// veth pairs advertise hardware checksum offload, so the kernel queues
// outgoing packets with CHECKSUM_PARTIAL and an unfilled L4 checksum,
// expecting a real NIC to fill it in. Through the virtual switch that
// never happens: the SAI VS userspace switch re-emits the bytes as-is
// (CHECKSUM_NONE), so the far endpoint validates a garbage TCP checksum
// and drops the packet. (ICMP is unaffected; it is always checksummed
// in software.) Disabling TX offload makes the kernel fill in the
// checksum before the packet enters the veth, fixing host-to-host TCP.
//
// Best-effort: a failure (ethtool missing, interface gone) is logged
// and never blocks link creation.
func disableTxOffload(pid int, ifname string) {
	if err := run("nsenter", "-t", strconv.Itoa(pid), "-n", "ethtool", "-K", ifname, "tx", "off"); err != nil {
		log.Printf("ethtool -K %s tx off in pid %d: %v", ifname, pid, err)
	}
}

// createLink wires two veth pairs for a link through the webmesh namespace.
//
//
//   switch A netns       webmesh netns        switch B netns
//   ┌──────────┐  veth  ┌────────────┐  eBPF ┌────────────┐  veth ┌──────────┐
//   │  ethN    │◄──────►│ wm-ethA    │◄─────►│ wm-ethB    │◄──────►│  ethM    │
//   └──────────┘        └────────────┘       └────────────┘        └──────────┘
//
// The webmesh-side endpoints (wm-ethA, wm-ethB, numbered per endpoint by
// the reconciler) remain in the webmesh namespace. The switch-side
// endpoints are moved into the respective switch namespaces and renamed
// to the SONiC kernel interface names (ethN), which SAI VS maps to
// logical ports (EthernetN) via the lane-based lanemap.
//
// The container-side endpoints are also given disabled TX checksum
// offload (see disableTxOffload) so packets entering the veth pair
// carry valid L4 checksums.
//
// The operation is idempotent: if either switch-side endpoint already
// exists in its target namespace, the function returns nil.
func createLink(devA, portA string, nA int, devB, portB string, nB int) error {
	pidA, err := containerPID(devA)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", devA, err)
	}
	pidB, err := containerPID(devB)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", devB, err)
	}

	ka := kernelIfname(portA)
	kb := kernelIfname(portB)

	// Idempotency: if either switch-side endpoint already exists, the
	// wiring is done. Re-assert the TX offload setting in case the link
	// predates it (webmesh upgrade, state-file loss), then skip.
	if linkInNS(pidA, ka) || linkInNS(pidB, kb) {
		log.Printf("link %s:%s <-> %s:%s already exists, skipping", devA, portA, devB, portB)
		disableTxOffload(pidA, ka)
		disableTxOffload(pidB, kb)
		return nil
	}

	wmA := wmIfname(nA)
	swA := swIfname(nA)
	wmB := wmIfname(nB)
	swB := swIfname(nB)

	// --- Veth pair A: webmesh ↔ switch A ---
	if err := run("ip", "link", "add", wmA, "type", "veth", "peer", "name", swA); err != nil {
		return fmt.Errorf("create veth %s<->%s: %w", wmA, swA, err)
	}

	if err := run("ip", "link", "set", swA, "netns", strconv.Itoa(pidA)); err != nil {
		cleanupWmPair(wmA)
		return fmt.Errorf("move %s → %s: %w", swA, devA, err)
	}

	if err := run("nsenter", "-t", strconv.Itoa(pidA), "-n", "ip", "link", "set", swA, "name", ka); err != nil {
		cleanupWmPair(wmA)
		return fmt.Errorf("rename %s→%s in %s: %w", swA, ka, devA, err)
	}

	disableTxOffload(pidA, ka)

	// --- Veth pair B: webmesh ↔ switch B ---
	if err := run("ip", "link", "add", wmB, "type", "veth", "peer", "name", swB); err != nil {
		cleanupWmPair(wmA)
		return fmt.Errorf("create veth %s<->%s: %w", wmB, swB, err)
	}

	if err := run("ip", "link", "set", swB, "netns", strconv.Itoa(pidB)); err != nil {
		cleanupWmPair(wmA)
		cleanupWmPair(wmB)
		return fmt.Errorf("move %s → %s: %w", swB, devB, err)
	}

	if err := run("nsenter", "-t", strconv.Itoa(pidB), "-n", "ip", "link", "set", swB, "name", kb); err != nil {
		cleanupWmPair(wmA)
		cleanupWmPair(wmB)
		return fmt.Errorf("rename %s→%s in %s: %w", swB, kb, devB, err)
	}

	disableTxOffload(pidB, kb)

	// Label the webmesh-side endpoints so `ip link` shows what they connect to
	runQuiet("ip", "link", "set", wmA, "alias", devA+":"+portA)
	runQuiet("ip", "link", "set", wmB, "alias", devB+":"+portB)

	// Bring up all four endpoints (best-effort)
	runQuiet("ip", "link", "set", wmA, "up")
	runQuiet("ip", "link", "set", wmB, "up")
	runQuiet("nsenter", "-t", strconv.Itoa(pidA), "-n", "ip", "link", "set", ka, "up")
	runQuiet("nsenter", "-t", strconv.Itoa(pidB), "-n", "ip", "link", "set", kb, "up")

	log.Printf("created link %s:%s <-> %s:%s (kernel: %s, %s; wm: %s, %s)",
		devA, portA, devB, portB, ka, kb, wmA, wmB)
	return nil
}

// deleteLink removes both veth pairs for a link.
// Deleting the webmesh-side endpoint also deletes the switch-side peer,
// which causes the SONiC interface to go down.
func deleteLink(devA, portA string, nA int, devB, portB string, nB int) error {
	wmA := wmIfname(nA)
	wmB := wmIfname(nB)

	// Delete webmesh-side endpoints (peers in switch ns are removed too)
	runQuiet("ip", "link", "set", wmA, "down")
	runQuiet("ip", "link", "del", wmA)
	runQuiet("ip", "link", "set", wmB, "down")
	runQuiet("ip", "link", "del", wmB)

	log.Printf("deleted link %s:%s <-> %s:%s (wm: %s, %s)", devA, portA, devB, portB, wmA, wmB)
	return nil
}

// cleanupWmPair removes a webmesh-side veth endpoint (and its peer) if it
// exists in the current namespace.
func cleanupWmPair(wmName string) {
	runQuiet("ip", "link", "del", wmName)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runQuiet(name string, args ...string) {
	_ = run(name, args...)
}
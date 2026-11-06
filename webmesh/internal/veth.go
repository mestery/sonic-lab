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
// The operation is idempotent: if both switch-side endpoints already
// exist in their target namespaces, the function returns nil. If only
// one exists (e.g. the other container was recreated and its veth pair
// torn down), only the missing veth pair is created. The surviving pair
// is left untouched so the surviving switch's SONiC association is
// preserved.
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

	existsA := linkInNS(pidA, ka)
	existsB := linkInNS(pidB, kb)

	// Idempotency: both switch-side endpoints already exist. Re-assert
	// the TX offload setting in case the link predates it (webmesh
	// upgrade, state-file loss), then skip.
	if existsA && existsB {
		log.Printf("link %s:%s <-> %s:%s already exists, skipping", devA, portA, devB, portB)
		disableTxOffload(pidA, ka)
		disableTxOffload(pidB, kb)
		return nil
	}

	// Create only the missing veth pair(s). When one end survives (the
	// other container was recreated and its veth pair torn down), the
	// surviving pair is left untouched: recreating it would replace the
	// surviving switch's veth and break its SONiC association.
	if !existsA {
		if err := createVethPair(nA, devA, portA, pidA); err != nil {
			return fmt.Errorf("create veth pair for %s:%s: %w", devA, portA, err)
		}
	}
	if !existsB {
		if err := createVethPair(nB, devB, portB, pidB); err != nil {
			// If we just created pair A and pair B fails, roll back A.
			if !existsA {
				cleanupWmPair(wmIfname(nA))
			}
			return fmt.Errorf("create veth pair for %s:%s: %w", devB, portB, err)
		}
	}

	log.Printf("created link %s:%s <-> %s:%s (wm: %s, %s)",
		devA, portA, devB, portB, wmIfname(nA), wmIfname(nB))
	return nil
}

// createVethPair creates one veth pair: the webmesh-side endpoint
// (wm-ethN) stays in the webmesh namespace, the peer is moved into the
// container's netns and renamed to the SONiC kernel interface name.
// On failure it removes any partially created interface.
func createVethPair(n int, dev, port string, pid int) error {
	wm := wmIfname(n)
	sw := swIfname(n)
	ka := kernelIfname(port)

	if err := run("ip", "link", "add", wm, "type", "veth", "peer", "name", sw); err != nil {
		return fmt.Errorf("create veth %s<->%s: %w", wm, sw, err)
	}
	if err := run("ip", "link", "set", sw, "netns", strconv.Itoa(pid)); err != nil {
		cleanupWmPair(wm)
		return fmt.Errorf("move %s → %s: %w", sw, dev, err)
	}
	if err := run("nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", sw, "name", ka); err != nil {
		cleanupWmPair(wm)
		return fmt.Errorf("rename %s→%s in %s: %w", sw, ka, dev, err)
	}

	// Label the webmesh-side endpoint so `ip link` shows what it connects to.
	runQuiet("ip", "link", "set", wm, "alias", dev+":"+port)

disableTxOffload(pid, ka)

	// Bring up both ends (best-effort).
	runQuiet("ip", "link", "set", wm, "up")
	runQuiet("nsenter", "-t", strconv.Itoa(pid), "-n", "ip", "link", "set", ka, "up")

	log.Printf("created veth pair %s:%s (wm: %s, kernel: %s)", dev, port, wm, ka)
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
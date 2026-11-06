//go:build linux

/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package webmesh

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/vishvananda/netlink"
)

// tcIngressParent is the TC handle for the ingress hook.
// netlink.HANDLE_INGRESS = 0xFFFFFFF1
const tcIngressParent = netlink.HANDLE_INGRESS

// filterName is the name attached to every patch_panel BPF filter.
const filterName = "patch_panel"

// AttachTC attaches the patch_panel program as a TC ingress filter on
// the interface with the given link index.
//
// AttachTC is idempotent per process: it returns nil if this process
// already attached the filter. If a filter named "patch_panel" is
// present but was attached by a previous webmesh process — the kernel
// keeps it alive after the loader exits, and its program's maps are
// empty in the new process — it is deleted and replaced with the
// current program so traffic flows through the current maps again.
func (p *PatchPanel) AttachTC(linkIdx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.attached[linkIdx] {
		return nil
	}

	link, err := netlink.LinkByIndex(linkIdx)
	if err != nil {
		return fmt.Errorf("lookup link ifidx %d: %w", linkIdx, err)
	}
	ifname := link.Attrs().Name

	// Ensure the ingress qdisc exists (idempotent). We use the tc CLI
	// because the netlink library's QdiscAdd is rejected by some kernel
	// versions with "Ingress filters must set Parent to HANDLE_INGRESS".
	cmd := exec.Command("tc", "qdisc", "add", "dev", ifname, "ingress")
	if out, err := cmd.CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "exists") {
			return fmt.Errorf("tc qdisc add dev %s ingress: %s: %w", ifname, string(out), err)
		}
	}

	// Replace a filter left behind by a previous process. The kernel
	// matches a deletion on chain/priority/protocol, not on the
	// program, so the read-back filter is a valid deletion spec even
	// though its program belongs to a dead process.
	existing, err := netlink.FilterList(link, tcIngressParent)
	if err != nil {
		return fmt.Errorf("list filters on %s: %w", ifname, err)
	}
	for _, f := range existing {
		if bf, ok := f.(*netlink.BpfFilter); ok && bf.Name == filterName {
			if err := netlink.FilterDel(bf); err != nil {
				return fmt.Errorf("remove stale BPF filter on %s: %w", ifname, err)
			}
		}
	}

	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: linkIdx,
			Parent:    tcIngressParent,
			Priority:  1,
			Protocol:  0x0003, // ETH_P_ALL
		},
		Fd:           p.prog.FD(),
		Name:         filterName,
		DirectAction: true,
	}
	if err := netlink.FilterAdd(filter); err != nil {
		return fmt.Errorf("add BPF filter on %s: %w", ifname, err)
	}
	p.attached[linkIdx] = true
	return nil
}

// DetachTC removes the patch_panel BPF filter from the interface. The
// link is untracked either way, so a later AttachTC starts from a clean
// slate instead of assuming a filter that may no longer exist.
func (p *PatchPanel) DetachTC(linkIdx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer delete(p.attached, linkIdx)

	link, err := netlink.LinkByIndex(linkIdx)
	if err != nil {
		return fmt.Errorf("lookup link ifidx %d: %w", linkIdx, err)
	}
	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: linkIdx,
			Parent:    tcIngressParent,
			Priority:  1,
			Protocol:  0x0003,
		},
		Fd:           p.prog.FD(),
		Name:         filterName,
		DirectAction: true,
	}
	if err := netlink.FilterDel(filter); err != nil {
		return fmt.Errorf("remove BPF filter on %s: %w", link.Attrs().Name, err)
	}
	return nil
}



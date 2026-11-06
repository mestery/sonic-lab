/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package webmesh

import (
	"fmt"
	"log"
	"sync"

	"github.com/cilium/ebpf"
)

// LinkStats mirrors the C struct in patch_panel.c.
type LinkStats struct {
	Packets uint64
	Bytes   uint64
}

// PatchPanel holds the loaded eBPF program and its maps.
type PatchPanel struct {
	prog     *ebpf.Program
	linkMap  *ebpf.Map
	statsMap *ebpf.Map
	coll     *ebpf.Collection

	// mu guards attached. The kernel keeps a TC filter alive after the
	// loader process exits (the filter holds a reference to the
	// program), so after an in-place restart a "patch_panel" filter on
	// a surviving wm-ethN interface may belong to a dead process.
	// attached records the links this process attached, so AttachTC can
	// tell its own idempotent re-calls apart from stale filters.
	mu       sync.Mutex
	attached map[int]bool
}

// LoadPatchPanel loads the compiled eBPF object file and returns handles
// to the program and maps.
func LoadPatchPanel(objFile string) (*PatchPanel, error) {
	spec, err := ebpf.LoadCollectionSpec(objFile)
	if err != nil {
		return nil, fmt.Errorf("loading BPF object %s: %w", objFile, err)
	}

	if _, ok := spec.Programs["patch_panel"]; !ok {
		return nil, fmt.Errorf("program 'patch_panel' not found in %s", objFile)
	}

	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return nil, fmt.Errorf("creating BPF collection: %w", err)
	}

	prog, ok := coll.Programs["patch_panel"]
	if !ok {
		coll.Close()
		return nil, fmt.Errorf("program 'patch_panel' not loaded")
	}

	linkMap, ok := coll.Maps["link_map"]
	if !ok {
		coll.Close()
		return nil, fmt.Errorf("map 'link_map' not loaded")
	}

	statsMap, ok := coll.Maps["stats_map"]
	if !ok {
		coll.Close()
		return nil, fmt.Errorf("map 'stats_map' not loaded")
	}

	log.Printf("eBPF patch panel loaded: prog fd=%d", prog.FD())

	return &PatchPanel{
		prog:     prog,
		linkMap:  linkMap,
		statsMap: statsMap,
		coll:     coll,
		attached: make(map[int]bool),
	}, nil
}

// Close releases all BPF resources (unloads program, frees maps).
func (p *PatchPanel) Close() {
	p.coll.Close()
}

// UpdateLink inserts bidirectional entries in link_map and initializes
// stats entries for the given pair of ifindices.
func (p *PatchPanel) UpdateLink(ifA, ifB uint32) error {
	if err := p.linkMap.Update(ifA, ifB, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("link_map[%d]=%d: %w", ifA, ifB, err)
	}
	if err := p.linkMap.Update(ifB, ifA, ebpf.UpdateAny); err != nil {
		_ = p.linkMap.Delete(ifA) // rollback
		return fmt.Errorf("link_map[%d]=%d: %w", ifB, ifA, err)
	}
	// Initialize stats entries
	zero := LinkStats{}
	_ = p.statsMap.Update(ifA, zero, ebpf.UpdateAny)
	_ = p.statsMap.Update(ifB, zero, ebpf.UpdateAny)
	return nil
}

// RemoveLink deletes bidirectional map entries for the given ifindices.
func (p *PatchPanel) RemoveLink(ifA, ifB uint32) {
	_ = p.linkMap.Delete(ifA)
	_ = p.linkMap.Delete(ifB)
	_ = p.statsMap.Delete(ifA)
	_ = p.statsMap.Delete(ifB)
}

// GetStats reads the stats counters for a given ifindex.
func (p *PatchPanel) GetStats(ifidx uint32) (LinkStats, error) {
	var s LinkStats
	if err := p.statsMap.Lookup(ifidx, &s); err != nil {
		return LinkStats{}, err
	}
	return s, nil
}
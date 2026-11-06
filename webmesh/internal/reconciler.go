/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package webmesh

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Reconciler maintains the desired and applied link sets and drives
// create/delete operations to converge the two.
type Reconciler struct {
	mu           sync.Mutex
	desired      map[string]Link
	applied      map[string]Link
	linkStates   map[string]bool // key → true=up, false=down
	wmNums       map[string]int  // endpoint "device:port" → N in its wm-ethN name; covers every applied endpoint
	stateFile    string
	topologyFile string
	topoMtime    time.Time
	interval     time.Duration
	panel        *PatchPanel // may be nil if eBPF is unavailable
	adopted      bool        // set once, after the startup eBPF adoption pass
}

// NewReconciler creates a Reconciler that persists state to stateFile.
// If topologyFile is non-empty it will be loaded on first Run() call.
// If panel is nil, eBPF operations are skipped (fallback to Phase 1 mode).
func NewReconciler(stateFile, topologyFile string, interval time.Duration, panel *PatchPanel) *Reconciler {
	r := &Reconciler{
		desired:      make(map[string]Link),
		applied:      make(map[string]Link),
		linkStates:   make(map[string]bool),
		wmNums:       make(map[string]int),
		stateFile:    stateFile,
		topologyFile: topologyFile,
		interval:     interval,
		panel:        panel,
	}
	r.loadState()
	r.checkTopoFile()
	return r
}

// SetDesired replaces the desired topology and immediately reconciles.
func (r *Reconciler) SetDesired(t Topology) (created, deleted int, errs []string) {
	r.mu.Lock()
	newDesired := make(map[string]Link, len(t.Links))
	for _, l := range t.Links {
		if len(l.Endpoints) != 2 {
			errs = append(errs, fmt.Sprintf("skipping link %q: expected 2 endpoints, got %d", l.Key(), len(l.Endpoints)))
			continue
		}
		newDesired[l.Key()] = l
	}
	r.desired = newDesired
	r.mu.Unlock()

	created, deleted, recErrs := r.Reconcile()
	errs = append(errs, recErrs...)
	return created, deleted, errs
}

// AddLink adds a single link to the desired set and reconciles.
func (r *Reconciler) AddLink(l Link) error {
	if len(l.Endpoints) != 2 {
		return fmt.Errorf("link must have exactly 2 endpoints")
	}
	r.mu.Lock()
	r.desired[l.Key()] = l
	r.mu.Unlock()

	_, _, errs := r.Reconcile()
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// RemoveLink removes a link (by key) from the desired set and reconciles.
func (r *Reconciler) RemoveLink(key string) error {
	r.mu.Lock()
	delete(r.desired, key)
	r.mu.Unlock()

	_, _, errs := r.Reconcile()
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Reconcile creates links that are desired but not yet applied, and deletes
// links that are applied but no longer desired.
func (r *Reconciler) Reconcile() (created, deleted int, errs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// One-shot: if applied links were restored from the state file but
	// their wm-ethN interfaces survived this process's restart, the
	// kernel still runs the previous process's eBPF program against its
	// (now empty) maps while the fresh maps sit unused. Rebuild the maps
	// and re-attach the current program before the create/delete passes.
	if !r.adopted {
		r.adopted = true
		r.adoptExisting()
	}

	// Create missing links in key order, so pushing the same topology to a
	// fresh webmesh always gives the same wm-ethN numbering
	keys := make([]string, 0, len(r.desired))
	for key := range r.desired {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		link := r.desired[key]
		if _, ok := r.applied[key]; ok {
			continue
		}
		a, b := link.Endpoints[0], link.Endpoints[1]
		if err := r.applyLink(a.Device, a.Port, r.assignWmNum(a), b.Device, b.Port, r.assignWmNum(b)); err != nil {
			log.Printf("create %s: %v", key, err)
			errs = append(errs, fmt.Sprintf("create %s: %v", key, err))
			continue
		}
		r.applied[key] = link
		created++
	}

	// Delete extra links
	for key, link := range r.applied {
		if _, ok := r.desired[key]; ok {
			continue
		}
		a, b := link.Endpoints[0], link.Endpoints[1]
		r.unapplyLink(r.wmNames(link))
		if err := deleteLink(a.Device, a.Port, r.wmNum(a), b.Device, b.Port, r.wmNum(b)); err != nil {
			log.Printf("delete %s: %v", key, err)
			errs = append(errs, fmt.Sprintf("delete %s: %v", key, err))
			continue
		}
		delete(r.applied, key)
		deleted++
	}

	r.pruneWmNums()

	// Drift correction: verify applied links are actually UP
	r.verifyLinks()

	if created > 0 || deleted > 0 {
		r.saveState()
		log.Printf("reconcile: %d created, %d deleted", created, deleted)
	}
	return
}

// verifyLinks checks all applied links and corrects drift:
//   - If interfaces are missing (container restarted), removes from applied
//     so the next cycle re-creates them.
//   - If interfaces exist but are DOWN, brings them back UP
//     (unless explicitly downed via the flap API).
func (r *Reconciler) verifyLinks() {
	for key, link := range r.applied {
		// Respect explicit down state from the flap API
		if down, ok := r.linkStates[key]; ok && !down {
			continue
		}
		a, b := link.Endpoints[0], link.Endpoints[1]

		pidA, errA := containerPID(a.Device)
		pidB, errB := containerPID(b.Device)
		if errA != nil || errB != nil {
			// Container not running yet; skip
			continue
		}

		ka := kernelIfname(a.Port)
		kb := kernelIfname(b.Port)

		// Check if interfaces exist; if not, mark for re-creation
		if !linkInNS(pidA, ka) || !linkInNS(pidB, kb) {
			log.Printf("verify: link %s interfaces missing, will re-create", key)
			r.unapplyLink(r.wmNames(link))
			delete(r.applied, key)
			r.saveState()
			continue
		}

		// Interfaces exist; ensure they are UP
		wmA, wmB := r.wmNames(link)

		needUp := false
		if !linkIsUp(pidA, ka) {
			needUp = true
			runQuiet("nsenter", "-t", strconv.Itoa(pidA), "-n", "ip", "link", "set", ka, "up")
		}
		if !linkIsUp(pidB, kb) {
			needUp = true
			runQuiet("nsenter", "-t", strconv.Itoa(pidB), "-n", "ip", "link", "set", kb, "up")
		}
		if needUp {
			// Also ensure webmesh-side is up
			runQuiet("ip", "link", "set", wmA, "up")
			runQuiet("ip", "link", "set", wmB, "up")
			log.Printf("verify: brought link %s back up", key)
		}
	}
}

// adoptExisting re-registers applied links with the freshly loaded eBPF
// patch panel. On a webmesh process restart the kernel keeps the
// previous process's unpinned program and TC filters alive (the filters
// hold references to it), but the new process's maps are empty: traffic
// hitting a stale filter is dropped and its stats stay at zero.
//
// For every applied link whose wm-ethN interfaces still exist, it
// rewrites the BPF map entries and re-attaches the current program
// (AttachTC replaces any stale filter). Links whose interfaces are gone
// are skipped; verifyLinks notices and the create path re-attaches when
// it re-creates them. Runs once, on the first Reconcile after startup.
// Caller must hold r.mu.
func (r *Reconciler) adoptExisting() {
	if r.panel == nil {
		return
	}
	keys := make([]string, 0, len(r.applied))
	for key := range r.applied {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	adopted := 0
	for _, key := range keys {
		link := r.applied[key]
		wmA, wmB := r.wmNames(link)
		idxA, errA := ifindexByName(wmA)
		idxB, errB := ifindexByName(wmB)
		if errA != nil || errB != nil {
			// Interface did not survive (e.g. the container's netns was
			// recreated). Leave it to verifyLinks and the create path.
			continue
		}
		if err := r.panel.UpdateLink(uint32(idxA), uint32(idxB)); err != nil {
			log.Printf("adopt %s: %v", key, err)
			continue
		}
		if err := r.panel.AttachTC(idxA); err != nil {
			log.Printf("adopt %s (tc %s): %v", key, wmA, err)
		}
		if err := r.panel.AttachTC(idxB); err != nil {
			log.Printf("adopt %s (tc %s): %v", key, wmB, err)
		}
		adopted++
	}
	if adopted > 0 {
		log.Printf("adopted %d existing links into the eBPF patch panel", adopted)
	}
}

// applyLink creates the veth pairs and (if eBPF is available) configures
// the BPF map and TC attachments.
func (r *Reconciler) applyLink(devA, portA string, nA int, devB, portB string, nB int) error {
	if err := createLink(devA, portA, nA, devB, portB, nB); err != nil {
		return err
	}

	// Configure eBPF (skip if not available)
	if r.panel == nil {
		return nil
	}

	wmA := wmIfname(nA)
	wmB := wmIfname(nB)

	ifIdxA, err := ifindexByName(wmA)
	if err != nil {
		return fmt.Errorf("resolve ifindex %s: %w", wmA, err)
	}
	ifIdxB, err := ifindexByName(wmB)
	if err != nil {
		return fmt.Errorf("resolve ifindex %s: %w", wmB, err)
	}

	if err := r.panel.UpdateLink(uint32(ifIdxA), uint32(ifIdxB)); err != nil {
		return fmt.Errorf("update BPF map: %w", err)
	}

	if err := r.panel.AttachTC(ifIdxA); err != nil {
		return fmt.Errorf("attach TC on %s: %w", wmA, err)
	}
	if err := r.panel.AttachTC(ifIdxB); err != nil {
		return fmt.Errorf("attach TC on %s: %w", wmB, err)
	}

	return nil
}

// unapplyLink detaches TC and removes BPF map entries before the veths
// are deleted.
func (r *Reconciler) unapplyLink(wmA, wmB string) {
	if r.panel == nil {
		return
	}

	idxA, aErr := ifindexByName(wmA)
	idxB, bErr := ifindexByName(wmB)

	if aErr == nil {
		r.panel.DetachTC(idxA)
	}
	if bErr == nil {
		r.panel.DetachTC(idxB)
	}
	if aErr == nil && bErr == nil {
		r.panel.RemoveLink(uint32(idxA), uint32(idxB))
	}
}

// endpointKey identifies an endpoint in wmNums.
func endpointKey(e Endpoint) string {
	return e.Device + ":" + e.Port
}

// assignWmNum returns the N for an endpoint's wm-ethN interface, assigning
// the lowest unused N on first use. Only the create path and loadState
// assign numbers; everything else looks them up with wmNum.
// Caller must hold r.mu.
func (r *Reconciler) assignWmNum(e Endpoint) int {
	key := endpointKey(e)
	if n, ok := r.wmNums[key]; ok {
		return n
	}
	used := make(map[int]bool, len(r.wmNums))
	for _, n := range r.wmNums {
		used[n] = true
	}
	n := 0
	for used[n] {
		n++
	}
	r.wmNums[key] = n
	return n
}

// wmNum returns the N already assigned to an endpoint. Every applied
// endpoint has one. Caller must hold r.mu.
func (r *Reconciler) wmNum(e Endpoint) int {
	return r.wmNums[endpointKey(e)]
}

// ensureWmNums assigns numbers to applied endpoints that lack one (e.g.
// from a state file written before wm_nums existed), in link-key order so
// the result is deterministic. Reports whether any were assigned.
// Caller must hold r.mu.
func (r *Reconciler) ensureWmNums() bool {
	keys := make([]string, 0, len(r.applied))
	for key := range r.applied {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assigned := false
	for _, key := range keys {
		for _, e := range r.applied[key].Endpoints {
			if _, ok := r.wmNums[endpointKey(e)]; !ok {
				r.assignWmNum(e)
				assigned = true
			}
		}
	}
	return assigned
}

// wmNames returns the webmesh-side interface names for a link's endpoints.
// Caller must hold r.mu.
func (r *Reconciler) wmNames(l Link) (string, string) {
	return wmIfname(r.wmNum(l.Endpoints[0])), wmIfname(r.wmNum(l.Endpoints[1]))
}

// pruneWmNums frees the numbers of endpoints that are neither desired nor
// applied. Caller must hold r.mu.
func (r *Reconciler) pruneWmNums() {
	inUse := make(map[string]bool)
	for _, set := range []map[string]Link{r.desired, r.applied} {
		for _, l := range set {
			for _, e := range l.Endpoints {
				inUse[endpointKey(e)] = true
			}
		}
	}
	for key := range r.wmNums {
		if !inUse[key] {
			delete(r.wmNums, key)
		}
	}
}

// Interfaces maps each applied link's webmesh-side interface name to the
// endpoint it connects to, e.g. "wm-eth0" → "spine1-linux:Ethernet0".
func (r *Reconciler) Interfaces() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, 2*len(r.applied))
	for _, l := range r.applied {
		for _, e := range l.Endpoints {
			out[wmIfname(r.wmNum(e))] = endpointKey(e)
		}
	}
	return out
}

// checkTopoFile records the current mtime of the topology file so that
// subsequent calls can detect changes. Call once at startup.
func (r *Reconciler) checkTopoFile() {
	if r.topologyFile == "" {
		return
	}
	if info, err := os.Stat(r.topologyFile); err == nil {
		r.mu.Lock()
		r.topoMtime = info.ModTime()
		r.mu.Unlock()
	}
}

// reloadTopoFile checks whether the topology file has been modified since
// the last load. If so, it reloads the file and sets the desired topology.
// Returns true if the file was reloaded.
func (r *Reconciler) reloadTopoFile() bool {
	if r.topologyFile == "" {
		return false
	}
	info, err := os.Stat(r.topologyFile)
	if err != nil {
		return false
	}
	r.mu.Lock()
	changed := info.ModTime().After(r.topoMtime)
	if changed {
		r.topoMtime = info.ModTime()
	}
	r.mu.Unlock()

	if !changed {
		return false
	}
	top, err := LoadTopologyFile(r.topologyFile)
	if err != nil {
		log.Printf("reload topology error: %v", err)
		return false
	}
	log.Printf("topology file changed: loaded %d links from %s", len(top.Links), r.topologyFile)
	r.SetDesired(top)
	return true
}

// Run blocks forever, reconciling on a ticker, watching the topology file
// for changes, and reloading on SIGHUP. Call in a goroutine.
func (r *Reconciler) Run() {
	if r.topologyFile != "" {
		top, err := LoadTopologyFile(r.topologyFile)
		if err != nil {
			log.Printf("warning: could not load topology file %s: %v", r.topologyFile, err)
		} else {
			log.Printf("loaded %d links from %s", len(top.Links), r.topologyFile)
			r.SetDesired(top)
		}
	}
	r.checkTopoFile()

	// Initial reconcile
	r.Reconcile()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Watch for topology file changes
			r.reloadTopoFile()
			// Reconcile any drift
			r.Reconcile()
		case <-sigCh:
			log.Println("SIGHUP: reloading topology file")
			if r.topologyFile != "" {
				if top, err := LoadTopologyFile(r.topologyFile); err != nil {
					log.Printf("reload error: %v", err)
				} else {
					r.SetDesired(top)
				}
				r.checkTopoFile()
			}
		}
	}
}

// DesiredCount returns the number of links in the desired set.
func (r *Reconciler) DesiredCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.desired)
}

// AppliedCount returns the number of links in the applied set.
func (r *Reconciler) AppliedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.applied)
}

// AppliedLinks returns a slice of all applied links.
func (r *Reconciler) AppliedLinks() []Link {
	r.mu.Lock()
	defer r.mu.Unlock()
	links := make([]Link, 0, len(r.applied))
	for _, l := range r.applied {
		links = append(links, l)
	}
	return links
}

// DesiredLinks returns a slice of all desired links.
func (r *Reconciler) DesiredLinks() []Link {
	r.mu.Lock()
	defer r.mu.Unlock()
	links := make([]Link, 0, len(r.desired))
	for _, l := range r.desired {
		links = append(links, l)
	}
	return links
}

// Panel returns the PatchPanel (may be nil).
func (r *Reconciler) Panel() *PatchPanel {
	return r.panel
}

// LinkStatsResponse is the JSON response for GET /v1/links/{key}/stats.
type LinkStatsResponse struct {
	LinkID string   `json:"link_id"`
	AToB   LinkStats `json:"a_to_b"`
	BToA   LinkStats `json:"b_to_a"`
}

// GetLinkStats returns per-direction stats for the given link.
func (r *Reconciler) GetLinkStats(key string) (LinkStatsResponse, error) {
	r.mu.Lock()
	link, ok := r.applied[key]
	var wmA, wmB string
	if ok {
		wmA, wmB = r.wmNames(link)
	}
	r.mu.Unlock()
	if !ok {
		return LinkStatsResponse{}, fmt.Errorf("link %q not found in applied set", key)
	}
	if r.panel == nil {
		return LinkStatsResponse{}, fmt.Errorf("eBPF not loaded")
	}

	ifIdxA, err := ifindexByName(wmA)
	if err != nil {
		return LinkStatsResponse{}, fmt.Errorf("resolve %s: %w", wmA, err)
	}
	ifIdxB, err := ifindexByName(wmB)
	if err != nil {
		return LinkStatsResponse{}, fmt.Errorf("resolve %s: %w", wmB, err)
	}

	aToB, _ := r.panel.GetStats(uint32(ifIdxA))
	bToA, _ := r.panel.GetStats(uint32(ifIdxB))

	return LinkStatsResponse{
		LinkID: key,
		AToB:   aToB,
		BToA:   bToA,
	}, nil
}

func (r *Reconciler) loadState() {
	data, err := os.ReadFile(r.stateFile)
	if err != nil {
		return
	}
	var s struct {
		Applied    []Link          `json:"applied"`
		LinkStates map[string]bool `json:"link_states,omitempty"`
		WmNums     map[string]int  `json:"wm_nums,omitempty"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("warning: could not parse state file %s: %v", r.stateFile, err)
		return
	}
	for _, l := range s.Applied {
		r.applied[l.Key()] = l
	}
	if s.LinkStates != nil {
		for k, v := range s.LinkStates {
			r.linkStates[k] = v
		}
	}
	for k, v := range s.WmNums {
		r.wmNums[k] = v
	}
	if r.ensureWmNums() {
		r.saveState()
	}
	if len(r.applied) > 0 {
		log.Printf("restored %d applied links from %s", len(r.applied), r.stateFile)
	}
}

// saveState persists the current applied set and link states to disk.
// Caller must hold r.mu.
func (r *Reconciler) saveState() {
	links := make([]Link, 0, len(r.applied))
	for _, l := range r.applied {
		links = append(links, l)
	}
	// Only persist non-trivial states (down links)
	states := make(map[string]bool)
	for k, v := range r.linkStates {
		if !v {
			states[k] = v
		}
	}
	s := struct {
		Applied    []Link          `json:"applied"`
		LinkStates map[string]bool `json:"link_states,omitempty"`
		WmNums     map[string]int  `json:"wm_nums,omitempty"`
	}{Applied: links, LinkStates: states, WmNums: r.wmNums}

	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(r.stateFile, data, 0o644)
}
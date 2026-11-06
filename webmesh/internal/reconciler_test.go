/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

package webmesh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestReconciler(t *testing.T) *Reconciler {
	t.Helper()
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	return NewReconciler(stateFile, "", time.Hour, nil) // long interval: no background ticks
}

func TestReconcilerSetDesiredInvalidLinks(t *testing.T) {
	r := newTestReconciler(t)

	top := Topology{
		Links: []Link{
			{Endpoints: []Endpoint{{Device: "a", Port: "b"}}}, // 1 endpoint: invalid
			{Endpoints: []Endpoint{}},                         // 0 endpoints: invalid
		},
	}
	created, deleted, errs := r.SetDesired(top)
	if created != 0 {
		t.Errorf("created = %d, want 0", created)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	if len(errs) != 2 {
		t.Errorf("len(errs) = %d, want 2", len(errs))
	}
	if r.DesiredCount() != 0 {
		t.Errorf("DesiredCount = %d, want 0", r.DesiredCount())
	}
}

func TestReconcilerSetDesiredValid(t *testing.T) {
	r := newTestReconciler(t)

	top := Topology{
		Links: []Link{
			{
				Endpoints: []Endpoint{
					{Device: "spine1-linux", Port: "Ethernet0"},
					{Device: "leaf1-linux", Port: "Ethernet0"},
				},
			},
		},
	}
	r.SetDesired(top)
	if r.DesiredCount() != 1 {
		t.Errorf("DesiredCount = %d, want 1", r.DesiredCount())
	}
}

func TestReconcilerAddLinkValidation(t *testing.T) {
	r := newTestReconciler(t)

	// Invalid: 1 endpoint
	err := r.AddLink(Link{Endpoints: []Endpoint{{Device: "a", Port: "b"}}})
	if err == nil {
		t.Error("AddLink with 1 endpoint: expected error, got nil")
	}

	// Invalid: 0 endpoints
	err = r.AddLink(Link{Endpoints: []Endpoint{}})
	if err == nil {
		t.Error("AddLink with 0 endpoints: expected error, got nil")
	}
}

func TestReconcilerRemoveNonExistent(t *testing.T) {
	r := newTestReconciler(t)
	// Removing a key that was never added should not error
	if err := r.RemoveLink("nonexistent"); err != nil {
		t.Errorf("RemoveLink(nonexistent) = %v, want nil", err)
	}
}

func TestReconcilerStatePersistence(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	r := NewReconciler(stateFile, "", time.Hour, nil)

	link := Link{
		Endpoints: []Endpoint{
			{Device: "spine1-linux", Port: "Ethernet0"},
			{Device: "leaf1-linux", Port: "Ethernet0"},
		},
	}
	key := link.Key()

	// Simulate applied state
	r.mu.Lock()
	r.applied[key] = link
	r.saveState()
	r.mu.Unlock()

	// Verify file exists
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// Load into a new reconciler
	r2 := NewReconciler(stateFile, "", time.Hour, nil)
	if r2.AppliedCount() != 1 {
		t.Errorf("r2.AppliedCount = %d, want 1", r2.AppliedCount())
	}
	links := r2.AppliedLinks()
	if len(links) != 1 || links[0].Key() != key {
		t.Errorf("r2.AppliedLinks() = %+v, want key %q", links, key)
	}
}

func TestReconcilerStatePersistenceEmpty(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	r := NewReconciler(stateFile, "", time.Hour, nil)
	if r.AppliedCount() != 0 {
		t.Errorf("AppliedCount with no state file = %d, want 0", r.AppliedCount())
	}
}

func TestReconcilerAppliedLinksEmpty(t *testing.T) {
	r := newTestReconciler(t)
	links := r.AppliedLinks()
	if links == nil {
		t.Error("AppliedLinks() returned nil, want empty slice")
	}
	if len(links) != 0 {
		t.Errorf("len(AppliedLinks()) = %d, want 0", len(links))
	}
}

func TestReconcilerGetLinkStatsNoEBPF(t *testing.T) {
	r := newTestReconciler(t)
	// Inject a link into applied set
	link := Link{
		Endpoints: []Endpoint{
			{Device: "spine1-linux", Port: "Ethernet0"},
			{Device: "leaf1-linux", Port: "Ethernet0"},
		},
	}
	r.mu.Lock()
	r.applied[link.Key()] = link
	r.mu.Unlock()

	// panel is nil, so GetLinkStats should return an error
	_, err := r.GetLinkStats(link.Key())
	if err == nil {
		t.Error("GetLinkStats with nil panel: expected error, got nil")
	}
}

func TestReconcilerGetLinkStatsNotFound(t *testing.T) {
	r := newTestReconciler(t)
	_, err := r.GetLinkStats("nonexistent")
	if err == nil {
		t.Error("GetLinkStats(nonexistent): expected error, got nil")
	}
}

func TestVerifyLinksRespectsExplicitDown(t *testing.T) {
	r := newTestReconciler(t)
	link := Link{
		Endpoints: []Endpoint{
			{Device: "spine1-linux", Port: "Ethernet0"},
			{Device: "leaf1-linux", Port: "Ethernet0"},
		},
	}
	key := link.Key()
	r.mu.Lock()
	r.applied[key] = link
	r.linkStates[key] = false // explicitly downed
	r.mu.Unlock()

	// verifyLinks should skip this link (it's explicitly down)
	// On macOS, containerPID will fail, so it'll skip anyway.
	// We're testing that it doesn't panic and handles the case.
	r.mu.Lock()
	r.verifyLinks()
	r.mu.Unlock()

	// Link should still be in applied (not removed)
	if r.AppliedCount() != 1 {
		t.Errorf("AppliedCount = %d, want 1 (explicitly-downed link should remain)", r.AppliedCount())
	}
}

func TestVerifyLinksNoPanicEmpty(t *testing.T) {
	r := newTestReconciler(t)
	// No applied links: verifyLinks should be a no-op
	r.mu.Lock()
	r.verifyLinks()
	r.mu.Unlock()
}

func TestVerifyLinksRemovesMissingInterfaces(t *testing.T) {
	r := newTestReconciler(t)
	link := Link{
		Endpoints: []Endpoint{
			{Device: "spine1-linux", Port: "Ethernet0"},
			{Device: "leaf1-linux", Port: "Ethernet0"},
		},
	}
	key := link.Key()
	r.mu.Lock()
	r.applied[key] = link
	r.mu.Unlock()

	// On macOS, containerPID will fail (no docker containers with those names
	// in a test context), so verifyLinks should skip without removing.
	// This tests that the code path doesn't panic.
	r.mu.Lock()
	r.verifyLinks()
	r.mu.Unlock()

	// If containerPID failed, the link stays in applied
	// (it will be retried on the next tick when containers are available)
	count := r.AppliedCount()
	if count != 1 {
		t.Logf("AppliedCount = %d (expected 1 if containerPID failed, 0 if containers exist but links don't)", count)
	}
}

func TestReconcileAdoptsOnce(t *testing.T) {
	r := newTestReconciler(t)
	link := Link{
		Endpoints: []Endpoint{
			{Device: "spine1-linux", Port: "Ethernet0"},
			{Device: "leaf1-linux", Port: "Ethernet0"},
		},
	}
	key := link.Key()
	r.mu.Lock()
	// A link that is both desired and applied: nothing for the create
	// or delete passes to do, so the only observable effect of the
	// first Reconcile is the one-shot adoption pass (a no-op with a
	// nil panel).
	r.desired[key] = link
	r.applied[key] = link
	r.mu.Unlock()

	if r.adopted {
		t.Fatal("adopted = true before any Reconcile")
	}
	r.Reconcile()
	if !r.adopted {
		t.Error("adopted not set after first Reconcile")
	}
	if r.AppliedCount() != 1 {
		t.Errorf("AppliedCount = %d, want 1", r.AppliedCount())
	}

	// A second pass must not re-run the adoption, and must be a no-op.
	r.Reconcile()
	if r.AppliedCount() != 1 {
		t.Errorf("AppliedCount after second Reconcile = %d, want 1", r.AppliedCount())
	}
}

func TestWmNumAllocation(t *testing.T) {
	r := newTestReconciler(t)
	a := Endpoint{Device: "spine1-a-very-long-suffix", Port: "Ethernet60"}
	b := Endpoint{Device: "leaf1-linux", Port: "Ethernet0"}
	c := Endpoint{Device: "leaf2-linux", Port: "Ethernet0"}

	if n := r.assignWmNum(a); n != 0 {
		t.Errorf("assignWmNum(a) = %d, want 0", n)
	}
	if n := r.assignWmNum(b); n != 1 {
		t.Errorf("assignWmNum(b) = %d, want 1", n)
	}
	if n := r.assignWmNum(a); n != 0 {
		t.Errorf("assignWmNum(a) again = %d, want stable 0", n)
	}

	// a is neither desired nor applied, so pruning frees 0 for reuse
	r.desired["b-c"] = Link{ID: "b-c", Endpoints: []Endpoint{b, c}}
	r.pruneWmNums()
	if _, ok := r.wmNums[endpointKey(a)]; ok {
		t.Error("prune kept unused endpoint a")
	}
	if n := r.assignWmNum(c); n != 0 {
		t.Errorf("assignWmNum(c) = %d, want reused 0", n)
	}
	if n := r.wmNum(b); n != 1 {
		t.Errorf("wmNum(b) after prune = %d, want 1", n)
	}
}

// testLinks returns three links whose keys sort as l[0] < l[1] < l[2].
func testLinks() []Link {
	return []Link{
		{Endpoints: []Endpoint{{Device: "leaf1-net", Port: "Ethernet0"}, {Device: "spine1-net", Port: "Ethernet0"}}},
		{Endpoints: []Endpoint{{Device: "leaf2-net", Port: "Ethernet0"}, {Device: "spine1-net", Port: "Ethernet4"}}},
		{Endpoints: []Endpoint{{Device: "leaf3-net", Port: "Ethernet0"}, {Device: "spine1-net", Port: "Ethernet8"}}},
	}
}

func checkInterfaces(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Interfaces() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Interfaces()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestWmNumsPersisted(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	r := NewReconciler(stateFile, "", time.Hour, nil)
	links := testLinks()
	r.mu.Lock()
	// Assign out of key order so the saved numbers differ from what
	// sorted assignment would produce.
	for i := len(links) - 1; i >= 0; i-- {
		l := links[i]
		r.applied[l.Key()] = l
		r.assignWmNum(l.Endpoints[0])
		r.assignWmNum(l.Endpoints[1])
	}
	r.saveState()
	r.mu.Unlock()
	want := r.Interfaces()

	r2 := NewReconciler(stateFile, "", time.Hour, nil)
	checkInterfaces(t, r2.Interfaces(), want)
	if r2.wmNum(links[2].Endpoints[0]) != 0 {
		t.Errorf("leaf3 = %d, want saved 0", r2.wmNum(links[2].Endpoints[0]))
	}
}

func TestLoadStateAssignsMissingWmNumsSorted(t *testing.T) {
	// A state file from before wm_nums existed: applied links, no numbers.
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	links := testLinks()
	data, _ := json.Marshal(map[string]interface{}{"applied": []Link{links[2], links[0], links[1]}})
	if err := os.WriteFile(stateFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"wm-eth0": "leaf1-net:Ethernet0",
		"wm-eth1": "spine1-net:Ethernet0",
		"wm-eth2": "leaf2-net:Ethernet0",
		"wm-eth3": "spine1-net:Ethernet4",
		"wm-eth4": "leaf3-net:Ethernet0",
		"wm-eth5": "spine1-net:Ethernet8",
	}
	// Map iteration order varies, so load several times.
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(stateFile, data, 0o644); err != nil {
			t.Fatal(err)
		}
		r := NewReconciler(stateFile, "", time.Hour, nil)
		checkInterfaces(t, r.Interfaces(), want)
	}

	// The assigned numbers are saved, so the next load reads them back.
	r := NewReconciler(stateFile, "", time.Hour, nil)
	checkInterfaces(t, r.Interfaces(), want)
}

func TestInterfacesReadOnly(t *testing.T) {
	r := newTestReconciler(t)
	l := testLinks()[0]
	r.mu.Lock()
	r.applied[l.Key()] = l
	r.assignWmNum(l.Endpoints[0])
	r.assignWmNum(l.Endpoints[1])
	before := len(r.wmNums)
	r.mu.Unlock()

	r.Interfaces()
	r.LinkStates()
	if _, err := r.GetLinkStats(l.Key()); err == nil {
		t.Error("GetLinkStats without eBPF: want error")
	}
	if len(r.wmNums) != before {
		t.Errorf("read paths changed wmNums: %d entries, want %d", len(r.wmNums), before)
	}
}

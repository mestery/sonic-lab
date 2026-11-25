/*
Copyright © 2026 Kyle Mestery All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file.
*/

// tgen (topology generator) generates docker-compose.yml and supporting
// scripts for a SONiC virtual switch lab. Topologies are specified via
// command-line flags rather than input files:
//
//
//	# Spine-leaf: 2 spines, 4 leaves
//	tgen -s 2 -l 4 -o ../mylab/
//
//	# Spine-leaf with ECMP: 2 spines, 4 leaves, 4 links per pair
//	tgen -s 2 -l 4 -links 4 -o ../ecmp/
//
//	# Full mesh: 4 nodes
//	tgen -m -n 4 -o ../mylab/
//
//	# Ring: 6 nodes
//	tgen -r -n 6 -o ../mylab/
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"sonic-lab/tgen/internal/topo"
	"sonic-lab/tgen/internal/writer"
)

func main() {
	fs := flag.NewFlagSet("tgen", flag.ExitOnError)

	outputDir := fs.String("o", ".", "output directory (must be a direct child of the repo root)")
	spines := fs.Int("s", 0, "number of spine switches (spine-leaf mode)")
	leaves := fs.Int("l", 0, "number of leaf switches (spine-leaf mode)")
	mesh := fs.Bool("m", false, "generate full mesh topology")
	ring := fs.Bool("r", false, "generate ring topology")
	nodes := fs.Int("n", 0, "number of nodes (for -m or -r)")
	links := fs.Int("links", 1, "parallel links per switch pair (for ECMP)")
	hosts := fs.Int("h", 0, "number of hosts attached to each leaf/node switch")
	ports := fs.Int("p", 1, "number of ports per host (each port maps to a different switch)")
	hostSpines := fs.Bool("host-spines", false, "also attach hosts to spines (spine-leaf mode)")
	noHostAddr := fs.Bool("no-host-addr", false, "don't generate default host addressing (addressing.txt)")
	srv6 := fs.Bool("srv6", false, "enable SRv6 overlay (locators, SIDs, seg6 routes)")
	netnsSuffix := fs.String("base", topo.DefaultNetnsSuffix, "suffix for each switch's backing netns container (spine1-<base>)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `tgen — SONiC virtual lab topology generator

Usage:
  tgen [flags]

Topology modes (pick one):
  -s <int>    Number of spine switches (spine-leaf mode, use with -l)
  -l <int>    Number of leaf switches (spine-leaf mode, use with -s)
  -m          Generate full mesh topology (use with -n)
  -r          Generate ring topology (use with -n)
  -n <int>    Number of nodes (for -m or -r)

Links (optional):
  -links <int>  Parallel links per switch pair (default 1; enables ECMP)

Hosts (optional):
  -h <int>    Number of hosts attached to each leaf (or ring/mesh node)
  -p <int>    Number of ports per host (default 1; each port maps to a
              different switch for redundancy)
  -host-spines
              Also attach hosts to spines (by default they attach to
              leaves only, as in a Clos fabric)
  -no-host-addr
              Don't generate addressing.txt. By default each host link
              gets a dual-stack subnet (10.S.H.0/24, fd00:0:S:H::/64),
              applied after boot by apply-addressing.sh (start.sh runs it)

Overlay (optional):
  -srv6       Enable SRv6 overlay (locators, SIDs, seg6 kernel routes)

Naming (optional):
  -base <str> Suffix for each switch's backing netns container (default
              "linux", giving spine1-linux, leaf1-linux, ...)

Output:
  -o <dir>    Output directory (default ".", must be a direct child of repo root)

Examples:
  tgen -s 1 -l 4 -o ../mylab/              # 1 spine, 4 leaves
  tgen -s 2 -l 4 -links 4 -o ../ecmp/     # 2x4 spine-leaf, 4 links per pair
  tgen -m -n 5 -links 2 -o ../mesh/       # Full mesh of 5, 2 links per pair
  tgen -r -n 8 -links 2 -o ../ring/       # Ring of 8, 2 links per neighbor
  tgen -s 2 -l 2 -h 2 -p 2 -o ../lab/    # 2x2 spine-leaf with hosts
`)
	}

	fs.Parse(os.Args[1:])

	// Validate mode selection
	opts := topo.Options{
		Spines:         *spines,
		Leaves:         *leaves,
		Mesh:           *mesh,
		Ring:           *ring,
		Nodes:          *nodes,
		Links:          *links,
		HostsPerSwitch: *hosts,
		PortsPerHost:   *ports,
		HostsOnSpines:  *hostSpines,
		SRv6:           *srv6,
		NetnsSuffix:    *netnsSuffix,
	}

	// The netns suffix becomes part of compose service and container names.
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(*netnsSuffix) {
		log.Fatalf("invalid -base %q: must start with a letter or digit and contain only letters, digits, '_', '.', or '-'", *netnsSuffix)
	}

	if opts.Mesh && opts.Ring {
		log.Fatal("cannot use -m and -r together")
	}
	if opts.Mesh && (*spines > 0 || *leaves > 0) {
		log.Fatal("cannot mix -m with -s/-l")
	}
	if opts.Ring && (*spines > 0 || *leaves > 0) {
		log.Fatal("cannot mix -r with -s/-l")
	}

	hasSpineLeaf := *spines > 0 || *leaves > 0
	if !opts.Mesh && !opts.Ring && !hasSpineLeaf {
		fs.Usage()
		log.Fatal("no topology mode specified (use -s/-l, -m, or -r)")
	}

	if opts.Mesh && *nodes < 2 {
		log.Fatal("-m requires -n >= 2")
	}
	if opts.Ring && *nodes < 3 {
		log.Fatal("-r requires -n >= 3")
	}

	// Resolve output directory to absolute path
	outDir, err := filepath.Abs(*outputDir)
	if err != nil {
		log.Fatalf("resolving output dir: %v", err)
	}

	// Verify output dir is a direct child of the repo root.
	// The generated compose file uses "context: .." to find Dockerfiles,
	// so the output dir must be one level below the repo root.
	repoRoot := filepath.Dir(outDir)
	if _, err := os.Stat(filepath.Join(repoRoot, "Dockerfile.webmesh")); err != nil {
		log.Fatalf("output dir %q does not appear to be a direct child of the repo root\n"+
			"(expected Dockerfile.webmesh in %q)\n"+
			"Use a path like '../mylab/' relative to this directory, or an absolute path.",
			*outputDir, repoRoot)
	}

	// Generate topology
	t, err := topo.Generate(opts)
	if err != nil {
		log.Fatalf("generating topology: %v", err)
	}
	for _, w := range t.Warnings {
		fmt.Fprintf(os.Stderr, "WARNING: %s\n", w)
	}

	// Write output files
	w := writer.New(outDir, t)
	w.SkipHostAddr = *noHostAddr
	if err := w.Run(); err != nil {
		log.Fatalf("writing output: %v", err)
	}

	if len(t.Hosts) > 0 {
		fmt.Printf("Generated %s topology (%d switches, %d hosts, %d links) → %s\n",
			t.Name, len(t.Switches), len(t.Hosts), len(t.Links), outDir)
	} else {
		fmt.Printf("Generated %s topology (%d switches, %d links) → %s\n",
			t.Name, len(t.Switches), len(t.Links), outDir)
	}
	fmt.Println("Run ./start.sh in the output directory to launch the lab.")
}

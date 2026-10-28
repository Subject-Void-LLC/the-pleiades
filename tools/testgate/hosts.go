// Sharing the container packages between this machine and a Podman machine
// (the Mac's), by time: which packages and tests may run on Podman (hosts.json),
// and the assignment both machines compute from the same list.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The hosts -host names. hostHere is the machine running the gate, which can
// run every container package; hostPodman is a Podman machine beside it,
// which runs only what hosts.json says it can.
const (
	hostHere   = "here"
	hostPodman = "podman"
)

// hostNames is the order the hosts are dealt in: on a tie the earlier one
// takes the unit, so here, which can run anything, is filled first.
var hostNames = []string{hostHere, hostPodman}

// hostTable is hosts.json: for every container package, whether it runs on a
// Podman machine, with the evidence, or only here, with the reason, and for a
// Podman package divided by test, the tests that stay here and why.
// tools/internal/flakegate's TestEveryContainerPackageIsClassified holds it to
// the Makefile's DOCKER_DEPENDENT_PACKAGES, so a new container package must be
// placed in one of the two lists.
type hostTable struct {
	// Podman maps a package's path in the module to the run that showed it
	// passing on the Mac's rootful Podman machine.
	Podman map[string]string `json:"podman"`
	// Here maps a package's path in the module to why it cannot run on
	// Podman, as measured there.
	Here map[string]string `json:"here"`
	// HereTests maps a Podman package that durations.json times test by test
	// to its tests that cannot run on Podman, each with why.
	HereTests map[string]map[string]string `json:"here_tests,omitempty"`
}

//go:embed hosts.json
var hostsJSON []byte

// hosts is hostsJSON read.
func hosts() hostTable {
	var t hostTable
	if err := json.Unmarshal(hostsJSON, &t); err != nil {
		panic(fmt.Sprintf("testgate: hosts.json: %v", err))
	}
	return t
}

// onPodman reports whether test (empty for the whole package) of pkg, an
// import path, may run on a Podman machine. A package hosts.json does not
// name runs here.
func (t hostTable) onPodman(pkg, test string) bool {
	rel := strings.TrimPrefix(pkg, modulePrefix)
	if _, ok := t.Podman[rel]; !ok {
		return false
	}
	_, stays := t.HereTests[rel][test]
	return test == "" || !stays
}

// hostPlan is how the packages are shared between the hosts: each host's
// pieces, sorted by package, and the seconds durations.json predicts for them.
type hostPlan struct {
	pieces  map[string][]piece
	seconds map[string]float64
}

// planHosts shares packages between hostHere and hostPodman by the times in
// seconds, so both finish together. Every machine given the same list computes
// the same plan, whatever order the list arrived in.
//
// The units are whole packages, and for a package seconds times test by test
// (splitTests, as -shard divides tests/e2e), each of its tests: dealing those
// one by one costs nothing more than dealing parts, since each host runs at
// most one piece of a package whichever of its tests it owns. A unit that
// cannot run on Podman (table.onPodman) is given to here first; the rest are
// dealt longest first, ties broken by name, each to the host with less time so
// far, here on a tie.
//
// A divided package both hosts own tests of runs on here with -skip naming
// Podman's tests, so a test added since durations.json was written runs once,
// on the host that can run anything, and on Podman with -run naming its own.
// One whose tests all fall to one host runs whole there. As with -shard, a
// divided package must have no statements of its own (wholeCoverageOnly).
func planHosts(packages []string, seconds map[string]float64, table hostTable) hostPlan {
	sorted := append([]string(nil), packages...)
	sort.Strings(sorted)
	sorted = compactStrings(sorted)
	split := splitTests(sorted, seconds)
	var whole []string
	for _, pkg := range sorted {
		if _, divided := split[pkg]; !divided {
			whole = append(whole, pkg)
		}
	}
	weight := weights(whole, seconds)

	type unit struct {
		pkg, test string
		weight    float64
	}
	var pinned, free []unit
	add := func(u unit) {
		if table.onPodman(u.pkg, u.test) {
			free = append(free, u)
			return
		}
		pinned = append(pinned, u)
	}
	for _, pkg := range sorted {
		tests, divided := split[pkg]
		if !divided {
			add(unit{pkg: pkg, weight: weight[pkg]})
			continue
		}
		for _, test := range tests {
			add(unit{pkg: pkg, test: test, weight: seconds[testKey(pkg, test)]})
		}
	}
	sort.SliceStable(free, func(i, j int) bool {
		if free[i].weight != free[j].weight {
			return free[i].weight > free[j].weight
		}
		if free[i].pkg != free[j].pkg {
			return free[i].pkg < free[j].pkg
		}
		return free[i].test < free[j].test
	})

	load := map[string]float64{}
	owner := map[string]map[string][]string{hostHere: {}, hostPodman: {}} // host -> package -> tests ("" for whole)
	give := func(host string, u unit) {
		load[host] += u.weight
		owner[host][u.pkg] = append(owner[host][u.pkg], u.test)
	}
	for _, u := range pinned {
		give(hostHere, u)
	}
	for _, u := range free {
		least := hostNames[0]
		for _, h := range hostNames[1:] {
			if load[h] < load[least] {
				least = h
			}
		}
		give(least, u)
	}

	plan := hostPlan{pieces: map[string][]piece{}, seconds: load}
	for _, pkg := range sorted {
		hereTests, podTests := owner[hostHere][pkg], owner[hostPodman][pkg]
		if _, divided := split[pkg]; !divided || len(hereTests) == 0 || len(podTests) == 0 {
			host := hostHere
			if len(hereTests) == 0 {
				host = hostPodman
			}
			plan.pieces[host] = append(plan.pieces[host], piece{pkg: pkg})
			continue
		}
		sort.Strings(podTests)
		plan.pieces[hostHere] = append(plan.pieces[hostHere], piece{pkg: pkg, skip: podTests})
		plan.pieces[hostPodman] = append(plan.pieces[hostPodman], piece{pkg: pkg, run: podTests})
	}
	return plan
}

// compactStrings drops adjacent duplicates from a sorted list, so a package
// named twice is dealt once.
func compactStrings(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// hostPieces is what host tests of packages (planHosts with durations.json and
// hosts.json): an error for a host this tool does not know, and for one that
// owns nothing, so a machine asked to run a share it does not have is told
// rather than reporting a pass that tested nothing.
func hostPieces(packages []string, host string, seconds map[string]float64, table hostTable) ([]piece, error) {
	if host != hostHere && host != hostPodman {
		return nil, fmt.Errorf("-host is %s or %s, not %q", hostHere, hostPodman, host)
	}
	pieces := planHosts(packages, seconds, table).pieces[host]
	if len(pieces) == 0 {
		return nil, fmt.Errorf("-host %s owns none of the %d packages", host, len(packages))
	}
	return pieces, nil
}

// printHostPlan writes the plan for packages, one line per host, which the
// Makefile reads: the host, the whole minutes durations.json predicts for its
// share, and each package it runs any of. A host that owns nothing is printed
// with no packages.
func printHostPlan(w io.Writer, packages []string, seconds map[string]float64, table hostTable) {
	plan := planHosts(packages, seconds, table)
	for _, h := range hostNames {
		fields := []string{h, fmt.Sprintf("%.0f", plan.seconds[h]/60)}
		for _, p := range plan.pieces[h] {
			fields = append(fields, p.pkg)
		}
		fmt.Fprintln(w, strings.Join(fields, " "))
	}
}

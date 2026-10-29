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
	// BudgetMB is the most memory one package's tests may take on this machine,
	// measured as its fall in MemAvailable while it runs alone (MemoryMB): a
	// package over it runs on Podman whenever Podman can run it. The container
	// pass runs one package at a time (DOCKER_TEST_PARALLELISM), so the budget
	// is per package; hosts.json says how it was derived.
	BudgetMB float64 `json:"budget_mb,omitempty"`
	// MemoryMB maps a package's path in the module, or a divided package's
	// test as durations.json names it ("tests/e2e TestX"), to the most memory
	// it took on this machine when run alone: the largest fall in MemAvailable
	// below where it began, its containers, test binaries and build together.
	MemoryMB map[string]float64 `json:"memory_mb,omitempty"`
	// BudgetNote says how BudgetMB was derived, from what measurements.
	BudgetNote string `json:"budget_note,omitempty"`
	// StartSeconds maps a host to how long after pr-check begins its container
	// share starts: here after the fast and repeat passes, the Mac after its own
	// mac and winarm legs, which pr-check lets finish first so the share does
	// not compete with them for the Mac's CPU and memory. Dealing starts each
	// host's load there, so the two shares end together, not merely last as
	// long. Measured from a gate's records; StartNote says which.
	StartSeconds map[string]float64 `json:"start_seconds,omitempty"`
	// StartNote says where StartSeconds came from.
	StartNote string `json:"start_note,omitempty"`
}

// memory is the measured peak of test (empty for the whole package) of pkg, an
// import path: the test's own, else its package's, else 0 when unmeasured.
func (t hostTable) memory(pkg, test string) float64 {
	rel := strings.TrimPrefix(pkg, modulePrefix)
	if test != "" {
		if m, ok := t.MemoryMB[rel+" "+test]; ok {
			return m
		}
	}
	return t.MemoryMB[rel]
}

// heavy reports whether a unit takes more than the budget here.
func (t hostTable) heavy(pkg, test string) bool {
	return t.BudgetMB > 0 && t.memory(pkg, test) > t.BudgetMB
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
// pieces, sorted by package, the seconds durations.json predicts for them
// (seconds, the share alone; ends, from pr-check's start, StartSeconds
// included), the
// largest measured memory of a unit each runs (peak, in MB), and the packages
// over the budget that must run here anyway (overHere).
type hostPlan struct {
	pieces   map[string][]piece
	seconds  map[string]float64
	ends     map[string]float64
	peak     map[string]float64
	overHere []string
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
// far (counted from when its share starts, table.StartSeconds), here on a
// tie. Memory comes before time: a unit Podman can run whose
// measured memory here is over table.BudgetMB goes to Podman before anything
// is dealt, since this machine's memory, shared with Docker Desktop's
// containers, is what a long container pass runs out of (its swap grew past
// 3 GB on 2026-10-29); one over the budget that only here can run stays here,
// and is named in overHere.
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
	var pinned, heavy, free []unit
	add := func(u unit) {
		switch {
		case !table.onPodman(u.pkg, u.test):
			pinned = append(pinned, u)
		case table.heavy(u.pkg, u.test):
			heavy = append(heavy, u)
		default:
			free = append(free, u)
		}
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

	load, peak := map[string]float64{}, map[string]float64{}
	for _, h := range hostNames {
		load[h] = table.StartSeconds[h]
	}
	owner := map[string]map[string][]string{hostHere: {}, hostPodman: {}} // host -> package -> tests ("" for whole)
	give := func(host string, u unit) {
		load[host] += u.weight
		peak[host] = max(peak[host], table.memory(u.pkg, u.test))
		owner[host][u.pkg] = append(owner[host][u.pkg], u.test)
	}
	var overHere []string
	for _, u := range pinned {
		give(hostHere, u)
		if table.heavy(u.pkg, u.test) && (len(overHere) == 0 || overHere[len(overHere)-1] != u.pkg) {
			overHere = append(overHere, u.pkg)
		}
	}
	for _, u := range heavy {
		give(hostPodman, u)
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

	share := map[string]float64{}
	for _, h := range hostNames {
		share[h] = load[h] - table.StartSeconds[h]
	}
	plan := hostPlan{pieces: map[string][]piece{}, seconds: share, ends: load, peak: peak, overHere: overHere}
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
// with no packages. The lines after, which the Makefile prints but does not
// parse, give when each share is predicted to end from pr-check's start, and
// the largest measured memory here against the budget with each package over
// it that only here can run.
func printHostPlan(w io.Writer, packages []string, seconds map[string]float64, table hostTable) {
	plan := planHosts(packages, seconds, table)
	for _, h := range hostNames {
		fields := []string{h, fmt.Sprintf("%.0f", plan.seconds[h]/60)}
		for _, p := range plan.pieces[h] {
			fields = append(fields, p.pkg)
		}
		fmt.Fprintln(w, strings.Join(fields, " "))
	}
	if len(table.StartSeconds) > 0 {
		fmt.Fprintf(w, "ends: here about %.0f min and %s about %.0f min after pr-check begins, each share starting at %.0f and %.0f min\n",
			plan.ends[hostHere]/60, hostPodman, plan.ends[hostPodman]/60, table.StartSeconds[hostHere]/60, table.StartSeconds[hostPodman]/60)
	}
	if table.BudgetMB > 0 {
		fmt.Fprintf(w, "memory here: the largest unit takes %.0f MB against a budget of %.0f MB", plan.peak[hostHere], table.BudgetMB)
		if len(plan.overHere) > 0 {
			fmt.Fprintf(w, "; over it, and only here can run them: %s", strings.Join(plan.overHere, " "))
		}
		fmt.Fprintln(w)
	}
}

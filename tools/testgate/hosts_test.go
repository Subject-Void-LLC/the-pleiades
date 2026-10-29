// Package main: sharing the container packages between this machine and a
// Podman machine by time, and hosts.json, which says what Podman can run.
package main

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// shareTable lets every package but "pinned" run on Podman, and keeps e2e's
// TestKind here.
var shareTable = hostTable{
	Podman:    map[string]string{"a": "ran there", "b": "ran there", "c": "ran there", "d": "ran there", "e2e": "ran there"},
	Here:      map[string]string{"pinned": "privileged"},
	HereTests: map[string]map[string]string{"e2e": {"TestKind": "kind"}},
}

// pkgs names packages by their path in the module.
func pkgs(rel ...string) []string {
	out := make([]string, len(rel))
	for i, r := range rel {
		out[i] = modulePrefix + r
	}
	return out
}

// owned is the packages of a host's pieces, by path in the module.
func owned(pieces []piece) []string {
	var out []string
	for _, p := range pieces {
		out = append(out, strings.TrimPrefix(p.pkg, modulePrefix))
	}
	return out
}

// TestPlanHosts_SharesWholePackagesEvenly deals four packages longest first to
// the host with less so far: 400 here, 300 to Podman, 200 to Podman (300 <
// 400), 100 here, so each host has 500 seconds.
func TestPlanHosts_SharesWholePackagesEvenly(t *testing.T) {
	seconds := map[string]float64{"a": 400, "b": 300, "c": 200, "d": 100}
	plan := planHosts(pkgs("d", "c", "b", "a"), seconds, shareTable)
	if got := strings.Join(owned(plan.pieces[hostHere]), " "); got != "a d" {
		t.Errorf("here runs %q, want \"a d\"", got)
	}
	if got := strings.Join(owned(plan.pieces[hostPodman]), " "); got != "b c" {
		t.Errorf("podman runs %q, want \"b c\"", got)
	}
	if plan.seconds[hostHere] != 500 || plan.seconds[hostPodman] != 500 {
		t.Errorf("loads are %v, want 500 each", plan.seconds)
	}
	for _, h := range hostNames {
		for _, p := range plan.pieces[h] {
			if p.filter() != nil {
				t.Errorf("%s narrows %s, which durations.json times whole", h, p.pkg)
			}
		}
	}
}

// TestPlanHosts_APackagePodmanCannotRunStaysHere puts the longest package on
// Podman's side of the scale were it free, and checks it runs here anyway,
// with Podman taking everything else, since here already carries more.
func TestPlanHosts_APackagePodmanCannotRunStaysHere(t *testing.T) {
	seconds := map[string]float64{"pinned": 900, "a": 400, "b": 300}
	plan := planHosts(pkgs("a", "b", "pinned"), seconds, shareTable)
	if got := strings.Join(owned(plan.pieces[hostHere]), " "); got != "pinned" {
		t.Errorf("here runs %q, want only the package Podman cannot run", got)
	}
	if got := strings.Join(owned(plan.pieces[hostPodman]), " "); got != "a b" {
		t.Errorf("podman runs %q, want \"a b\"", got)
	}
	// A package hosts.json does not name is treated as one Podman cannot run.
	plan = planHosts(pkgs("a", "unlisted"), map[string]float64{"a": 10, "unlisted": 1}, shareTable)
	if got := strings.Join(owned(plan.pieces[hostHere]), " "); got != "unlisted" {
		t.Errorf("here runs %q, want the unlisted package", got)
	}
}

// TestPlanHosts_DividesAPackageByTestAcrossBoth checks a package durations.json
// times test by test is shared test by test: here runs every test but
// Podman's (-skip), so a test added since runs once, here; Podman runs its own
// by name; a test hosts.json keeps here is never on Podman; and the two loads
// end close together.
func TestPlanHosts_DividesAPackageByTestAcrossBoth(t *testing.T) {
	seconds := map[string]float64{
		"a":            300,
		"e2e TestKind": 200, "e2e TestBig": 250, "e2e TestMid": 150, "e2e TestSmall": 60, "e2e TestTiny": 40,
	}
	plan := planHosts(pkgs("a", "e2e"), seconds, shareTable)
	universe := []string{"TestKind", "TestBig", "TestMid", "TestSmall", "TestTiny", "TestAddedSince"}
	runs := map[string][]string{}
	for _, h := range hostNames {
		for _, p := range plan.pieces[h] {
			if p.pkg != modulePrefix+"e2e" {
				continue
			}
			for _, test := range universe {
				if runsTest(t, p, test) {
					runs[test] = append(runs[test], h)
				}
			}
			if h == hostHere && len(p.skip) == 0 {
				t.Errorf("here's piece of e2e is %+v, want a -skip of Podman's tests", p)
			}
			if h == hostPodman && len(p.run) == 0 {
				t.Errorf("podman's piece of e2e is %+v, want a -run of its own tests", p)
			}
		}
	}
	for _, test := range universe {
		if len(runs[test]) != 1 {
			t.Errorf("e2e %s runs on %v, want exactly one host", test, runs[test])
		}
	}
	if got := runs["TestKind"]; len(got) != 1 || got[0] != hostHere {
		t.Errorf("TestKind runs on %v, want here only (hosts.json keeps it here)", got)
	}
	if got := runs["TestAddedSince"]; len(got) != 1 || got[0] != hostHere {
		t.Errorf("a test durations.json does not know runs on %v, want here", got)
	}
	total := 1000.0
	if diff := math.Abs(plan.seconds[hostHere] - plan.seconds[hostPodman]); diff > 60 {
		t.Errorf("loads %v differ by %.0f of %.0f seconds, want within the smallest test", plan.seconds, diff, total)
	}
}

// TestPlanHosts_ADividedPackageAllOnOneHostRunsWhole checks a divided package
// whose tests all fall to one host is run whole there, never narrowed to
// nothing on the other.
func TestPlanHosts_ADividedPackageAllOnOneHostRunsWhole(t *testing.T) {
	seconds := map[string]float64{"a": 1000, "e2e TestBig": 10, "e2e TestMid": 5}
	plan := planHosts(pkgs("a", "e2e"), seconds, shareTable)
	if got := strings.Join(owned(plan.pieces[hostPodman]), " "); got != "e2e" {
		t.Fatalf("podman runs %q, want e2e whole", got)
	}
	if f := plan.pieces[hostPodman][0].filter(); f != nil {
		t.Errorf("podman narrows e2e with %v, want it whole", f)
	}
}

// TestHostPieces_RefusesAnEmptyShareAndAnUnknownHost checks a host that owns
// nothing, and one this tool does not know, are errors rather than a pass
// that tested nothing.
func TestHostPieces_RefusesAnEmptyShareAndAnUnknownHost(t *testing.T) {
	if _, err := hostPieces(pkgs("pinned"), hostPodman, map[string]float64{"pinned": 5}, shareTable); err == nil {
		t.Error("podman's share of a package only here can run was accepted, want an error")
	}
	if _, err := hostPieces(pkgs("a"), "mac", map[string]float64{"a": 5}, shareTable); err == nil {
		t.Error("an unknown host was accepted, want an error")
	}
	pieces, err := hostPieces(pkgs("pinned", "a"), hostHere, map[string]float64{"pinned": 5, "a": 5}, shareTable)
	if err != nil || len(pieces) == 0 {
		t.Errorf("here's share: %v, %v", pieces, err)
	}
}

// TestPrintHostPlan_OneLinePerHost checks the lines pr-check reads: a host, its
// minutes, its packages, and a host with nothing printed bare.
func TestPrintHostPlan_OneLinePerHost(t *testing.T) {
	var out bytes.Buffer
	printHostPlan(&out, pkgs("pinned"), map[string]float64{"pinned": 600}, shareTable)
	want := "here 10 " + modulePrefix + "pinned\npodman 0\n"
	if out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
}

// TestHostsJSON_IsReadAndConsistent reads the real hosts.json: it parses, no
// package is in both lists, every entry has its reason, and every test kept
// here belongs to a Podman package durations.json times test by test.
func TestHostsJSON_IsReadAndConsistent(t *testing.T) {
	table := hosts()
	if len(table.Podman) == 0 || len(table.Here) == 0 {
		t.Fatalf("hosts.json has %d Podman and %d here packages, want both lists filled", len(table.Podman), len(table.Here))
	}
	for pkg, why := range table.Podman {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s runs on Podman with no evidence written", pkg)
		}
		if _, both := table.Here[pkg]; both {
			t.Errorf("%s is in both lists", pkg)
		}
	}
	for pkg, why := range table.Here {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s stays here with no reason written", pkg)
		}
	}
	d := durations()
	for pkg, tests := range table.HereTests {
		if _, ok := table.Podman[pkg]; !ok {
			t.Errorf("here_tests names %s, which is not a Podman package", pkg)
		}
		if _, divided := splitTests([]string{modulePrefix + pkg}, d)[modulePrefix+pkg]; !divided {
			t.Errorf("here_tests names %s, which durations.json does not time test by test, so its tests are never dealt", pkg)
		}
		for test, why := range tests {
			if _, timed := d[pkg+" "+test]; !timed {
				t.Errorf("here_tests keeps %s %s, which durations.json does not time", pkg, test)
			}
			if strings.TrimSpace(why) == "" {
				t.Errorf("%s %s stays here with no reason written", pkg, test)
			}
		}
	}
}

// TestPlanHosts_AHeavyPackageGoesToPodmanFirst checks memory comes before
// time: a package over the budget that Podman can run goes there even though
// time alone would put it here, and one over the budget that only here can
// run stays here and is named, with the largest memory each host runs.
func TestPlanHosts_AHeavyPackageGoesToPodmanFirst(t *testing.T) {
	table := shareTable
	table.BudgetMB = 1500
	table.MemoryMB = map[string]float64{"a": 2500, "b": 400, "c": 300, "pinned": 1800}
	seconds := map[string]float64{"a": 100, "b": 300, "c": 200, "pinned": 50}
	plan := planHosts(pkgs("a", "b", "c", "pinned"), seconds, table)
	if got := strings.Join(owned(plan.pieces[hostPodman]), " "); !strings.Contains(got, "a") {
		t.Errorf("podman runs %q, want the heavy package a", got)
	}
	if got := strings.Join(owned(plan.pieces[hostHere]), " "); strings.Contains(got, "a") {
		t.Errorf("here runs %q, which includes the heavy package Podman can run", got)
	}
	if len(plan.overHere) != 1 || plan.overHere[0] != modulePrefix+"pinned" {
		t.Errorf("over the budget here: %v, want only pinned", plan.overHere)
	}
	if plan.peak[hostHere] != 1800 || plan.peak[hostPodman] != 2500 {
		t.Errorf("peaks %v, want here 1800 and podman 2500", plan.peak)
	}
	var out bytes.Buffer
	printHostPlan(&out, pkgs("a", "b", "c", "pinned"), seconds, table)
	if !strings.Contains(out.String(), "memory here: the largest unit takes 1800 MB against a budget of 1500 MB; over it, and only here can run them: "+modulePrefix+"pinned") {
		t.Errorf("printed %q", out.String())
	}
	// Without a budget, memory decides nothing.
	plan = planHosts(pkgs("a", "b", "c"), seconds, shareTable)
	if got := strings.Join(owned(plan.pieces[hostHere]), " "); got != "b" {
		t.Errorf("with no budget here runs %q, want b, the longest, by time alone", got)
	}
}

// TestPlanHosts_StartTimesShiftTheBalance checks a host whose share starts
// later gets less of it, so the two end together: Podman starting 300 s late
// takes only what fits after that.
func TestPlanHosts_StartTimesShiftTheBalance(t *testing.T) {
	table := shareTable
	table.StartSeconds = map[string]float64{hostHere: 0, hostPodman: 300}
	seconds := map[string]float64{"a": 400, "b": 300, "c": 200, "d": 100}
	plan := planHosts(pkgs("a", "b", "c", "d"), seconds, table)
	// a goes here (0 < 300), b to Podman (300 < 400), c here (400 < 600), and d
	// here on the tie at 600.
	if plan.ends[hostHere] != 700 || plan.ends[hostPodman] != 600 {
		t.Errorf("ends %v, want here 700 and podman 600", plan.ends)
	}
	if plan.seconds[hostPodman] != 300 || plan.seconds[hostHere] != 700 {
		t.Errorf("shares %v, want podman 300 and here 700", plan.seconds)
	}
	var out bytes.Buffer
	printHostPlan(&out, pkgs("a", "b", "c", "d"), seconds, table)
	if !strings.Contains(out.String(), "ends: here about 12 min and podman about 10 min after pr-check begins") {
		t.Errorf("printed %q", out.String())
	}
}

// Package main: dividing a package by test across shards, so more shards
// shorten the slowest one past its longest package.
package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/headroom"
)

// runsTest reports whether a go test run narrowed by p's filter runs the
// top-level test named test, reading the pattern as go test does.
func runsTest(t *testing.T, p piece, test string) bool {
	t.Helper()
	f := p.filter()
	if f == nil {
		return true
	}
	re, err := regexp.Compile(f[1])
	if err != nil {
		t.Fatalf("%s %q does not compile: %v", f[0], f[1], err)
	}
	if f[0] == "-run" {
		return re.MatchString(test)
	}
	return !re.MatchString(test)
}

// dividedTimes is three whole packages and one, e2e, timed test by test.
var dividedTimes = map[string]float64{
	"a": 300, "b": 200, "c": 100,
	"e2e TestBig": 400, "e2e TestMid": 250, "e2e TestSmall": 50, "e2e TestTiny": 5,
}

var dividedPackages = []string{modulePrefix + "a", modulePrefix + "b", modulePrefix + "c", modulePrefix + "e2e"}

// TestPiecesOf_EveryTestRunsInExactlyOneShard is the property dividing must
// keep: across the shards, every whole package runs once and every test of
// a divided one runs once, including a test durations.json has never heard
// of, which the shard owning the longest test picks up through -skip.
func TestPiecesOf_EveryTestRunsInExactlyOneShard(t *testing.T) {
	universe := []string{"TestBig", "TestMid", "TestSmall", "TestTiny", "TestAddedSince"}
	for _, n := range []int{1, 2, 3, 4, 7} {
		wholes, tests := map[string]int{}, map[string]int{}
		for k := 1; k <= n; k++ {
			pieces, err := piecesOf(dividedPackages, fmt.Sprintf("%d/%d", k, n), dividedTimes)
			if err != nil {
				t.Fatalf("shard %d/%d: %v", k, n, err)
			}
			for _, p := range pieces {
				if p.pkg != modulePrefix+"e2e" {
					if p.filter() != nil {
						t.Errorf("shard %d/%d narrows %s, which durations.json times whole", k, n, p.pkg)
					}
					wholes[p.pkg]++
					continue
				}
				for _, test := range universe {
					if runsTest(t, p, test) {
						tests[test]++
					}
				}
			}
		}
		for _, pkg := range dividedPackages[:3] {
			if wholes[pkg] != 1 {
				t.Errorf("%d shards: %s ran in %d of them, want 1", n, pkg, wholes[pkg])
			}
		}
		for _, test := range universe {
			if tests[test] != 1 {
				t.Errorf("%d shards: e2e %s ran in %d of them, want 1", n, test, tests[test])
			}
		}
	}
}

// TestPiecesOf_CutsALongPackageIntoFewParts pins one assignment: e2e's 705
// seconds are more than two shards' fair share of 1305 (652.5), so it is cut
// into two parts, its longest test alone in one; the parts and the packages
// are then dealt longest first to the shard with less so far; the shard
// owning the longest test skips what the other owns, which runs its own by
// name.
func TestPiecesOf_CutsALongPackageIntoFewParts(t *testing.T) {
	first, err := piecesOf(dividedPackages, "1/2", dividedTimes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := piecesOf(dividedPackages, "2/2", dividedTimes)
	if err != nil {
		t.Fatal(err)
	}
	// Parts: TestBig (400); TestMid, TestSmall, TestTiny (305). Dealt: part
	// 1 to shard 1; part 2 to 2; a (300) to 2 (605); b (200) to 1 (600); c
	// (100) to 1 (700).
	rest := []string{"TestMid", "TestSmall", "TestTiny"}
	want1 := []piece{{pkg: modulePrefix + "b"}, {pkg: modulePrefix + "c"}, {pkg: modulePrefix + "e2e", skip: rest}}
	want2 := []piece{{pkg: modulePrefix + "a"}, {pkg: modulePrefix + "e2e", run: rest}}
	if fmt.Sprint(first) != fmt.Sprint(want1) {
		t.Errorf("shard 1/2 = %+v, want %+v", first, want1)
	}
	if fmt.Sprint(second) != fmt.Sprint(want2) {
		t.Errorf("shard 2/2 = %+v, want %+v", second, want2)
	}
	if got, err := shardBy(dividedPackages, "1/2", dividedTimes); err != nil || !slices.Equal(got, []string{modulePrefix + "b", modulePrefix + "c", modulePrefix + "e2e"}) {
		t.Errorf("shardBy 1/2 = %v, %v, want b, c and e2e, plain paths for -list", got, err)
	}
}

// TestPiecesOf_APackageWithinItsShareRunsWhole: timed test by test but no
// longer than a shard's fair share, a package is not cut, so no shard pays
// for it twice.
func TestPiecesOf_APackageWithinItsShareRunsWhole(t *testing.T) {
	for k := 1; k <= 2; k++ {
		pieces, err := piecesOf(dividedPackages, fmt.Sprintf("%d/2", k), map[string]float64{
			"a": 900, "b": 800, "c": 700, "e2e TestBig": 300, "e2e TestMid": 200,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pieces {
			if p.filter() != nil {
				t.Errorf("shard %d/2 narrowed %+v, a package within its share", k, p)
			}
		}
	}
}

// TestPiecesOf_ShardsHoldingADividedPackageAreFew: across six shards, e2e's
// forty equal tests go to as few shards as its time needs, not to every
// shard, since each pays for the package once.
func TestPiecesOf_ShardsHoldingADividedPackageAreFew(t *testing.T) {
	seconds := map[string]float64{"a": 600, "b": 600, "c": 500, "d": 400, "e": 300}
	for i := range 40 {
		seconds[fmt.Sprintf("e2e Test%02d", i)] = 20
	}
	packages := []string{modulePrefix + "a", modulePrefix + "b", modulePrefix + "c", modulePrefix + "d", modulePrefix + "e", modulePrefix + "e2e"}
	holding := 0
	for k := 1; k <= 6; k++ {
		pieces, err := piecesOf(packages, fmt.Sprintf("%d/6", k), seconds)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pieces {
			if p.pkg == modulePrefix+"e2e" {
				holding++
			}
		}
	}
	// 3200 seconds over six shards is 533 each; e2e's 800 needs two parts.
	if holding != 2 {
		t.Errorf("%d shards hold part of e2e, want the 2 its 800 seconds need", holding)
	}
}

// TestCutTests_LeavesNoPartEmpty: tests with no time fall together into
// the first empty group and can leave another with none, which a shard would
// run as the whole package; cutTests drops it, and keeps the longest test in
// the first group, which is the one whose shard skips rather than runs.
func TestCutTests_LeavesNoPartEmpty(t *testing.T) {
	times := map[string]float64{"TestLong": 100, "TestZero": 0, "TestNil": 0}
	got := cutTests([]string{"TestNil", "TestLong", "TestZero"}, func(test string) float64 { return times[test] }, 3)
	want := [][]string{{"TestLong"}, {"TestNil", "TestZero"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("cutTests = %v, want %v: no empty group, the longest test first", got, want)
	}
}

// TestPiecesOf_WithoutAShardEverythingIsWhole keeps an unsharded run, as
// pr-check and test-full make, exactly what it was: every package whole,
// in one go test.
func TestPiecesOf_WithoutAShardEverythingIsWhole(t *testing.T) {
	pieces, err := piecesOf(dividedPackages, "", dividedTimes)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pieces {
		if p.filter() != nil {
			t.Errorf("an unsharded run narrowed %+v", p)
		}
	}
	if len(pieces) != len(dividedPackages) {
		t.Errorf("%d pieces for %d packages", len(pieces), len(dividedPackages))
	}
}

// TestPiecesOf_MoreShardsThanUnitsIsRefused keeps the old refusal: a shard
// that owns nothing is told, not reported green.
func TestPiecesOf_MoreShardsThanUnitsIsRefused(t *testing.T) {
	if _, err := piecesOf(dividedPackages, "8/8", dividedTimes); err == nil || !strings.Contains(err.Error(), "owns none") {
		t.Errorf("shard 8/8 of seven units = %v, want a refusal", err)
	}
}

// TestPiecewise_NarrowsOnlyADividedPackage proves a re-run or re-measure of
// a divided package carries its piece's filter, and of a whole one carries
// the plain arguments, without either sharing the caller's slice.
func TestPiecewise_NarrowsOnlyADividedPackage(t *testing.T) {
	args := make([]string, 2, 8)
	copy(args, []string{"-race", "-count=1"})
	argsFor := piecewise(args, []piece{{pkg: "x"}, {pkg: "y", run: []string{"TestA", "TestB"}}})
	if got := argsFor("x"); !slices.Equal(got, args) {
		t.Errorf("whole package's arguments = %v, want %v", got, args)
	}
	got := argsFor("y")
	if want := []string{"-race", "-count=1", "-run", "^(TestA|TestB)$"}; !slices.Equal(got, want) {
		t.Errorf("divided package's arguments = %v, want %v", got, want)
	}
	if !slices.Equal(args, []string{"-race", "-count=1"}) || slices.Equal(args[:cap(args)][2:4], got[2:4]) {
		t.Errorf("piecewise wrote into the caller's arguments: %v", args[:cap(args)])
	}
}

// TestWholeCoverageOnly_RefusesAPartsCoverage proves a divided package that
// turns out to have statements is refused, naming the fix, rather than
// reaching coverage-check as two numbers for one package.
func TestWholeCoverageOnly_RefusesAPartsCoverage(t *testing.T) {
	pieces := []piece{{pkg: "whole"}, {pkg: "part", run: []string{"TestA"}}}
	if err := wholeCoverageOnly(pieces, map[string]float64{"whole": 80}); err != nil {
		t.Errorf("a whole package's coverage was refused: %v", err)
	}
	err := wholeCoverageOnly(pieces, map[string]float64{"whole": 80, "part": 40})
	if err == nil || !strings.Contains(err.Error(), "part is divided") || !strings.Contains(err.Error(), "time it as one package") {
		t.Errorf("a part's coverage = %v, want a refusal naming the package and the fix", err)
	}
}

// TestRunPieces_RunsEachPartAloneAndTheWholeTogether proves the pieces
// against real go test runs: the whole package runs, and the divided one
// runs exactly the test its piece names, in a go test of its own, since a
// -run on the shared one would have narrowed the whole package too. The
// guard watching the runs sees each package begin, so its account names
// every package that ran.
func TestRunPieces_RunsEachPartAloneAndTheWholeTogether(t *testing.T) {
	throwawayModule(t, true)
	still := headroom.Memory{TotalMB: 8000, AvailableMB: 4000}
	g := &guard{watch: headroom.StartWatch(still, func() (headroom.Memory, error) { return still, nil }, time.Hour, func(string) {})}
	defer g.finish()
	events, waitErr, err := runPieces([]string{"-count=1"}, []piece{
		{pkg: "gateproof/good"},
		{pkg: "gateproof/skipper", run: []string{"TestCloud"}},
		{pkg: "gateproof/bad", skip: []string{"TestBroken"}},
	}, g)
	if err != nil || waitErr != nil {
		t.Fatalf("runPieces = %v, %v", waitErr, err)
	}
	for _, pkg := range []string{"gateproof/good", "gateproof/skipper", "gateproof/bad"} {
		if _, ok := g.peaks()[pkg]; !ok {
			t.Errorf("the guard never saw %s begin: its account holds %v", pkg, g.peaks())
		}
	}
	ran := map[string]string{}
	for _, evt := range events {
		if evt.Test != "" && (evt.Action == "pass" || evt.Action == "fail" || evt.Action == "skip") {
			ran[strings.TrimPrefix(evt.Package, "gateproof/")+" "+evt.Test] = evt.Action
		}
	}
	want := map[string]string{"good TestOne": "pass", "skipper TestCloud": "skip"}
	if fmt.Sprint(ran) != fmt.Sprint(want) {
		t.Errorf("tests run = %v, want %v: the whole package, and of the others only what their pieces own", ran, want)
	}
}

// fills reports whether every one of n shards owns a piece of packages.
func fills(packages []string, n int, seconds map[string]float64) bool {
	for k := 1; k <= n; k++ {
		if _, err := piecesOf(packages, fmt.Sprintf("%d/%d", k, n), seconds); err != nil {
			return false
		}
	}
	return true
}

// TestShardsFor_AsksForNoShardItCannotFill: the count shardsFor gives fills
// every shard, and one more would leave a shard with nothing (or pass the
// cap), for small lists, a divided package beside whole ones, and the
// embedded times with tests/e2e and one other package, which counting e2e
// as six shards got wrong.
func TestShardsFor_AsksForNoShardItCannotFill(t *testing.T) {
	cases := []struct {
		name     string
		packages []string
		seconds  map[string]float64
	}{
		{"one package", []string{modulePrefix + "a"}, dividedTimes},
		{"divided beside whole", dividedPackages, dividedTimes},
		{"e2e and cmd/pleiades", []string{modulePrefix + "tests/e2e", modulePrefix + "cmd/pleiades"}, durations()},
		{"e2e, cmd/pleiades, cmd/runner", []string{modulePrefix + "tests/e2e", modulePrefix + "cmd/pleiades", modulePrefix + "cmd/runner"}, durations()},
	}
	for _, c := range cases {
		n, err := shardsFor(c.packages, 6, c.seconds)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n < 1 || n > 6 || !fills(c.packages, n, c.seconds) {
			t.Errorf("%s: %d shards, which do not all own a piece", c.name, n)
		}
		if n < 6 && fills(c.packages, n+1, c.seconds) {
			t.Errorf("%s: %d shards, but %d would all own a piece", c.name, n, n+1)
		}
	}
	if n, err := shardsFor(dividedPackages, 2, dividedTimes); err != nil || n != 2 {
		t.Errorf("capped at 2: %d, %v", n, err)
	}
	if _, err := shardsFor(nil, 6, dividedTimes); err == nil {
		t.Error("no packages: accepted")
	}
	if _, err := shardsFor(dividedPackages, 0, dividedTimes); err == nil {
		t.Error("zero shards: accepted")
	}
}

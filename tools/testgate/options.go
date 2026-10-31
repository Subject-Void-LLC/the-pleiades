// What one testgate run tests and how: the go test arguments a mode
// implies, and which packages a shard owns.
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// options is one run's configuration, parsed from the command line.
type options struct {
	// integration is the older -integration mode: the tagged suite with
	// no coverage, as test-integration runs it.
	integration bool
	// full is the one pass that replaces test-race, test-integration and
	// coverage's own run: -race, the integration tag, coverage, and a
	// fresh run of every test.
	full bool
	// repeat runs every test that many times in one process, with no race
	// detector, tag or coverage: test-repeat's pass, for state a test
	// leaves behind it. Zero is off.
	repeat int
	// strict fails on any failure, with no re-run: what `make ci` and the
	// nightly CI run judge by.
	strict bool
	// noRace leaves -race out of the passes that otherwise use it, for a
	// machine that cannot build with it: on Windows the race detector needs
	// cgo and a C toolchain, which tools/wincheck's box does not have.
	noRace bool
	// parallel is go test's -p, how many packages run at once; zero leaves
	// go test's default.
	parallel int
	// budgetMB is the memory one package's tests are charged for when -p is
	// lowered to what the machine fits (-budget-mb); zero keeps the policy's
	// own (headroom.BudgetMB, packed by resources.json). The container pass
	// sets it low: its test processes are light and its containers are the
	// engine's.
	budgetMB int
	// shard is "k/n": this run owns the k-th of n shards, balanced by how
	// long each package's tests take (shardOf). Empty means every package.
	shard string
	// coverageOut is where the run's per-package coverage is written, as
	// JSON, for coverage-check -measured. Empty writes nothing.
	coverageOut string
	// reportOut is where the run's record is written (flakegate.Run),
	// whatever the outcome. Empty writes nothing.
	reportOut string
	// packages are the import paths to test; empty means ./...
	packages []string
	// commit and clean are the record's stamp as given on the command line
	// (-commit, -clean): the commit pr-check began at and yes or no for
	// whether its tree was clean then. Both empty stamps nothing.
	commit, clean string
}

// stamp is the stamp o's record carries, or why the values given for it
// cannot be one (flakegate.ParseStamp).
func (o options) stamp() (flakegate.Stamp, error) {
	return flakegate.ParseStamp(o.commit, o.clean)
}

// goTestArgs is the go test argument list for o's mode, before -json and
// the packages.
//
// -count=1 in both tagged modes is not belt and braces: tests/e2e builds
// cmd/controller and cmd/runner as subprocesses, so the test cache cannot
// see a change to either and would replay a stale pass, and a CI runner's
// restored build cache holds test results too.
func (o options) goTestArgs() []string {
	if o.repeat > 0 {
		args := []string{"-count=" + strconv.Itoa(o.repeat), "-timeout", goTestTimeout}
		if o.parallel > 0 {
			args = append(args, "-p", strconv.Itoa(o.parallel))
		}
		return args
	}
	args := []string{"-race", "-timeout", goTestTimeout}
	if o.noRace {
		args = args[1:]
	}
	switch {
	case o.full:
		args = append(args, "-tags", "integration", "-cover", "-count=1")
	case o.integration:
		args = append(args, "-tags", "integration", "-count=1")
	}
	if o.parallel > 0 {
		args = append(args, "-p", strconv.Itoa(o.parallel))
	}
	return args
}

// validate refuses a combination with no single meaning.
func (o options) validate() error {
	if o.full && o.integration {
		return fmt.Errorf("-full already runs the integration tag; pass one of -full and -integration")
	}
	if o.budgetMB < 0 {
		return fmt.Errorf("-budget-mb must be zero (the policy's own) or a number of megabytes")
	}
	if o.repeat < 0 || (o.repeat > 0 && (o.full || o.integration)) {
		return fmt.Errorf("-repeat is its own pass, untagged and without -race; pass it alone, with a count of at least 1")
	}
	if o.coverageOut != "" && !o.full {
		return fmt.Errorf("-coverage-out needs -full, the only mode that measures coverage")
	}
	if o.shard != "" && len(o.packages) == 0 {
		return fmt.Errorf("-shard needs an explicit package list: ./... cannot be divided without listing it")
	}
	if _, err := o.stamp(); err != nil {
		return err
	}
	return nil
}

// selected is the package list this run tests: o.packages narrowed to its
// shard, or ./... when none were named.
func (o options) selected() ([]string, error) {
	if len(o.packages) == 0 {
		return []string{"./..."}, nil
	}
	return shardOf(o.packages, o.shard)
}

// pieces is what this run tests of each package it owns: piecesOf with the
// times in durations.json, or ./... whole when no package was named.
func (o options) pieces() ([]piece, error) {
	if len(o.packages) == 0 {
		return []piece{{pkg: "./..."}}, nil
	}
	return piecesOf(o.packages, o.shard, durations())
}

// piece is what one run tests of one package: all of it, or, for a package
// durations.json times test by test, the tests named in run, or every test
// but those named in skip.
type piece struct {
	pkg  string
	run  []string
	skip []string
}

// filter is the go test flag that narrows a run of the package to p, or
// nothing for the whole package. The names are test function names, which
// need no quoting in a pattern.
func (p piece) filter() []string {
	switch {
	case len(p.run) > 0:
		return []string{"-run", "^(" + strings.Join(p.run, "|") + ")$"}
	case len(p.skip) > 0:
		return []string{"-skip", "^(" + strings.Join(p.skip, "|") + ")$"}
	}
	return nil
}

// testKey is how durations.json names one top-level test of a package: the
// package's path in the module, a space, and the test.
func testKey(pkg, test string) string {
	return strings.TrimPrefix(pkg, modulePrefix) + " " + test
}

// splitTests returns the tests seconds times one by one for each of the
// packages, by package. A package it names no test of is absent: it is
// balanced, and run, whole.
func splitTests(packages []string, seconds map[string]float64) map[string][]string {
	out := map[string][]string{}
	for _, pkg := range packages {
		prefix := strings.TrimPrefix(pkg, modulePrefix) + " "
		for key := range seconds {
			if test, ok := strings.CutPrefix(key, prefix); ok && test != "" {
				out[pkg] = append(out[pkg], test)
			}
		}
		sort.Strings(out[pkg])
	}
	for pkg, tests := range out {
		if len(tests) == 0 {
			delete(out, pkg)
		}
	}
	return out
}

// durationsJSON is how long each container package's tests took in CI, in
// seconds, by its path inside the module: go test's own elapsed time for
// the package, which each containers job records in the measurement it
// uploads (flakegate.Measurement.Seconds), the same number -durations-check
// compares with. A shard's time is the sum of its packages', and a pull
// request waits for its slowest shard, so this is what shardOf balances on.
// Refresh it from a recent run's measurements when -durations-check warns;
// a package missing from it counts as the median.
//
//go:embed durations.json
var durationsJSON []byte

// modulePrefix is how a package's import path starts, before its path in
// the module.
const modulePrefix = "github.com/Subject-Void-LLC/the-pleiades/"

// durations is durationsJSON read, by path in the module.
func durations() map[string]float64 {
	var d map[string]float64
	if err := json.Unmarshal(durationsJSON, &d); err != nil {
		panic(fmt.Sprintf("testgate: durations.json: %v", err))
	}
	return d
}

// shardOf returns the packages shard ("k/n") owns, balanced by the times in
// durations.json: shardBy with them.
func shardOf(packages []string, shard string) ([]string, error) {
	return shardBy(packages, shard, durations())
}

// shardBy returns the packages shard ("k/n") owns, all of them or a piece:
// the packages of piecesOf, sorted.
func shardBy(packages []string, shard string, seconds map[string]float64) ([]string, error) {
	pieces, err := piecesOf(packages, shard, seconds)
	if err != nil {
		return nil, err
	}
	owned := make([]string, 0, len(pieces))
	for _, p := range pieces {
		owned = append(owned, p.pkg)
	}
	return owned, nil
}

// piecesOf returns what shard ("k/n") tests of the packages, one piece per
// package it owns any of, sorted by package. Without a shard every package
// is one whole piece.
//
// The units dealt are packages, taken longest first (by seconds; a package
// it lacks counts as the median of the packages it has), each given to the
// shard with the least time so far, the lower-numbered on a tie. Every
// runner computes the same assignment whatever order the list arrived in,
// since ties are broken by name. With every time equal this deals the sorted
// list round-robin.
//
// Dealing by position alone put the three longest packages in one shard,
// which then took three times as long as the others (41 minutes against 9
// to 16), and a pull request waits for its slowest shard. Dealing whole
// packages then left the slowest shard no faster than the longest package,
// tests/e2e's fourteen minutes, however many shards there were.
//
// So a package seconds times test by test (splitTests) and that takes longer
// than a shard's fair share (the total over n) is cut into as few parts as
// bring each under it, its tests dealt among the parts the same way, and the
// parts are dealt with the packages. As few as possible, not one test per
// unit, because a shard pays for a divided package once whichever of its
// tests it runs: its test binary is built, its TestMain runs (tests/e2e's
// builds the Controller and the Runner), and the shard loads every image the
// package's directory names, several gigabytes for tests/e2e, which also
// fills the image cache once per shard. Dealt one test at a time, forty tests
// went to every shard, and every shard paid. A package within its share runs
// whole.
//
// A divided package's shard that owns its longest test runs every test but
// the ones other shards own (-skip), so a test added since durations.json
// was written still runs exactly once; every other shard runs the tests it
// owns by name (-run). A divided package must have no statements of its own,
// as tests/e2e has none: a piece's coverage is a percentage of a part, which
// no merge can turn back into the whole's.
//
// A shard that owns nothing is an error rather than an empty success, so a
// workflow asking for more shards than there are units is told, instead of
// reporting a green job that tested nothing.
func piecesOf(packages []string, shard string, seconds map[string]float64) ([]piece, error) {
	sorted := append([]string(nil), packages...)
	sort.Strings(sorted)
	if shard == "" {
		pieces := make([]piece, 0, len(sorted))
		for _, pkg := range sorted {
			pieces = append(pieces, piece{pkg: pkg})
		}
		return pieces, nil
	}
	kText, nText, ok := strings.Cut(shard, "/")
	k, errK := strconv.Atoi(kText)
	n, errN := strconv.Atoi(nText)
	if !ok || errK != nil || errN != nil || n < 1 || k < 1 || k > n {
		return nil, fmt.Errorf("-shard %q is not k/n with 1 <= k <= n", shard)
	}

	split := splitTests(sorted, seconds)
	var whole []string
	for _, pkg := range sorted {
		if _, divided := split[pkg]; !divided {
			whole = append(whole, pkg)
		}
	}
	weight := weights(whole, seconds)
	total := 0.0
	for _, pkg := range whole {
		total += weight[pkg]
	}
	testTime := func(pkg, test string) float64 { return seconds[testKey(pkg, test)] }
	for pkg, tests := range split {
		for _, test := range tests {
			total += testTime(pkg, test)
		}
	}
	fair := total / float64(n)

	// A unit is a whole package, or one part of a divided one: the tests
	// dealt to it, the first of them that package's longest.
	type unit struct {
		pkg    string
		part   int
		tests  []string
		weight float64
	}
	var units []unit
	for _, pkg := range sorted {
		tests, divided := split[pkg]
		if !divided {
			units = append(units, unit{pkg: pkg, weight: weight[pkg]})
			continue
		}
		sum := 0.0
		for _, test := range tests {
			sum += testTime(pkg, test)
		}
		parts := 1
		if fair > 0 {
			parts = min(len(tests), max(1, int(math.Ceil(sum/fair))))
		}
		if parts == 1 {
			units = append(units, unit{pkg: pkg, weight: sum})
			continue
		}
		for i, part := range cutTests(tests, func(test string) float64 { return testTime(pkg, test) }, parts) {
			w := 0.0
			for _, test := range part {
				w += testTime(pkg, test)
			}
			units = append(units, unit{pkg: pkg, part: i + 1, tests: part, weight: w})
		}
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].weight > units[j].weight })

	load := make([]float64, n)
	mine := map[string][]string{} // a divided package -> the tests this shard owns
	theirs := map[string][]string{}
	skipper := map[string]bool{} // a divided package whose longest test this shard owns
	var owned []string
	for _, u := range units {
		least := 0
		for i := 1; i < n; i++ {
			if load[i] < load[least] {
				least = i
			}
		}
		load[least] += u.weight
		switch {
		case u.part == 0 && least == k-1:
			owned = append(owned, u.pkg)
		case u.part == 0:
		case least == k-1:
			mine[u.pkg] = append(mine[u.pkg], u.tests...)
			skipper[u.pkg] = skipper[u.pkg] || u.part == 1
		default:
			theirs[u.pkg] = append(theirs[u.pkg], u.tests...)
		}
	}

	pieces := make([]piece, 0, len(owned)+len(mine))
	for _, pkg := range owned {
		pieces = append(pieces, piece{pkg: pkg})
	}
	for pkg, tests := range mine {
		sort.Strings(tests)
		other := theirs[pkg]
		sort.Strings(other)
		if skipper[pkg] {
			pieces = append(pieces, piece{pkg: pkg, skip: other})
			continue
		}
		pieces = append(pieces, piece{pkg: pkg, run: tests})
	}
	if len(pieces) == 0 {
		return nil, fmt.Errorf("shard %s owns none of the %d packages; use fewer shards", shard, len(sorted))
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].pkg < pieces[j].pkg })
	return pieces, nil
}

// cutTests deals tests among at most parts groups, longest first to the
// group with the least time so far, so the first group holds the longest
// test. A group left with no test is dropped rather than returned: it would
// reach a shard as a piece with no filter, which runs the whole package.
func cutTests(tests []string, time func(string) float64, parts int) [][]string {
	byTime := append([]string(nil), tests...)
	sort.SliceStable(byTime, func(i, j int) bool { return time(byTime[i]) > time(byTime[j]) })
	groups := make([][]string, max(1, parts))
	load := make([]float64, len(groups))
	for _, test := range byTime {
		least := 0
		for i := 1; i < len(groups); i++ {
			if load[i] < load[least] {
				least = i
			}
		}
		groups[least] = append(groups[least], test)
		load[least] += time(test)
	}
	kept := groups[:0]
	for _, g := range groups {
		if len(g) > 0 {
			kept = append(kept, g)
		}
	}
	return kept
}

// shardsFor is the most shards, up to most, that each own a piece of the
// packages as piecesOf deals them, so a workflow asks for exactly as many
// as testgate can fill. Counting packages cannot answer it: a divided
// package is cut into as few parts as its time needs, not one per shard,
// and a shard left with nothing is refused.
func shardsFor(packages []string, most int, seconds map[string]float64) (int, error) {
	if len(packages) == 0 {
		return 0, errors.New("no packages to divide")
	}
	if most < 1 {
		return 0, fmt.Errorf("-shards-for %d: ask for at least one shard", most)
	}
	for n := min(most, maxUnits(packages, seconds)); n > 1; n-- {
		filled := true
		for k := 1; k <= n && filled; k++ {
			_, err := piecesOf(packages, fmt.Sprintf("%d/%d", k, n), seconds)
			filled = err == nil
		}
		if filled {
			return n, nil
		}
	}
	return 1, nil
}

// maxUnits bounds shardsFor's search: no count can be filled past one shard
// per whole package plus one per test of a divided one.
func maxUnits(packages []string, seconds map[string]float64) int {
	split := splitTests(packages, seconds)
	n := 0
	for _, pkg := range packages {
		if tests, divided := split[pkg]; divided {
			n += len(tests)
			continue
		}
		n++
	}
	return n
}

// weights is each package's time: its own from seconds, or, for one seconds
// does not hold, the median of the times of those it does (1 when it holds
// none, so every package weighs the same).
func weights(packages []string, seconds map[string]float64) map[string]float64 {
	out := map[string]float64{}
	var known []float64
	for _, p := range packages {
		if t, ok := seconds[strings.TrimPrefix(p, modulePrefix)]; ok && t > 0 {
			out[p] = t
			known = append(known, t)
		}
	}
	median := 1.0
	if len(known) > 0 {
		sort.Float64s(known)
		median = known[len(known)/2]
	}
	for _, p := range packages {
		if _, ok := out[p]; !ok {
			out[p] = median
		}
	}
	return out
}

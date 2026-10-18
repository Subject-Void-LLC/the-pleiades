// What one testgate run tests and how: the go test arguments a mode
// implies, and which packages a shard owns.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	// parallel is go test's -p, how many packages run at once; zero leaves
	// go test's default.
	parallel int
	// shard is "k/n": this run owns the k-th of n shards, balanced by how
	// long each package's tests take (shardOf). Empty means every package.
	shard string
	// coverageOut is where the run's per-package coverage is written, as
	// JSON, for coverage-check -measured. Empty writes nothing.
	coverageOut string
	// packages are the import paths to test; empty means ./...
	packages []string
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
	if o.repeat < 0 || (o.repeat > 0 && (o.full || o.integration)) {
		return fmt.Errorf("-repeat is its own pass, untagged and without -race; pass it alone, with a count of at least 1")
	}
	if o.coverageOut != "" && !o.full {
		return fmt.Errorf("-coverage-out needs -full, the only mode that measures coverage")
	}
	if o.shard != "" && len(o.packages) == 0 {
		return fmt.Errorf("-shard needs an explicit package list: ./... cannot be divided without listing it")
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

// durationsJSON is how long each container package's tests took in CI, in
// seconds, by its path inside the module: measured from the containers
// jobs' logs (the time from one package's result to the next, packages
// running one at a time), averaged over runs. A shard's time is the sum of
// its packages', and a pull request waits for its slowest shard, so this is
// what shardOf balances on. Refresh it from a recent run when a package's
// time moves a lot; one missing from it counts as the median.
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

// shardBy returns the packages shard ("k/n") owns. The packages are taken
// longest first (by seconds, a package's path in the module mapping to its
// time; one it lacks counts as the median of those it has), each given to
// the shard with the least time so far, the lower-numbered on a tie. Every
// runner computes the same assignment whatever order the list arrived in,
// since ties are broken by name. With every time equal this deals the
// sorted list round-robin. The shard's packages come back sorted.
//
// Dealing by position alone put the three longest packages in one shard,
// which then took three times as long as the others (41 minutes against
// 9 to 16), and a pull request waits for its slowest shard.
//
// A shard that owns nothing is an error rather than an empty success, so a
// workflow asking for more shards than there are packages is told, instead
// of reporting a green job that tested nothing.
func shardBy(packages []string, shard string, seconds map[string]float64) ([]string, error) {
	sorted := append([]string(nil), packages...)
	sort.Strings(sorted)
	if shard == "" {
		return sorted, nil
	}
	kText, nText, ok := strings.Cut(shard, "/")
	k, errK := strconv.Atoi(kText)
	n, errN := strconv.Atoi(nText)
	if !ok || errK != nil || errN != nil || n < 1 || k < 1 || k > n {
		return nil, fmt.Errorf("-shard %q is not k/n with 1 <= k <= n", shard)
	}
	weight := weights(sorted, seconds)
	byTime := append([]string(nil), sorted...)
	sort.SliceStable(byTime, func(i, j int) bool { return weight[byTime[i]] > weight[byTime[j]] })
	load := make([]float64, n)
	var owned []string
	for _, pkg := range byTime {
		least := 0
		for i := 1; i < n; i++ {
			if load[i] < load[least] {
				least = i
			}
		}
		load[least] += weight[pkg]
		if least == k-1 {
			owned = append(owned, pkg)
		}
	}
	if len(owned) == 0 {
		return nil, fmt.Errorf("shard %s owns none of the %d packages; use fewer shards", shard, len(sorted))
	}
	sort.Strings(owned)
	return owned, nil
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

// What one testgate run tests and how: the go test arguments a mode
// implies, and which packages a shard owns.
package main

import (
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
	// strict fails on any failure, with no re-run: what `make ci` and the
	// nightly CI run judge by.
	strict bool
	// parallel is go test's -p, how many packages run at once; zero leaves
	// go test's default.
	parallel int
	// shard is "k/n": this run owns every package whose position in the
	// sorted list is k-1 modulo n. Empty means every package.
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

// shardOf returns the packages shard ("k/n") owns: after sorting, every
// package whose index is k-1 modulo n. Sorting first is what makes the
// assignment the same on every runner whatever order the list arrived in,
// and dealing round-robin keeps neighbouring packages apart. An empty shard
// owns everything.
//
// A shard that owns nothing is an error rather than an empty success, so a
// workflow asking for more shards than there are packages is told, instead
// of reporting a green job that tested nothing.
func shardOf(packages []string, shard string) ([]string, error) {
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
	var owned []string
	for i, pkg := range sorted {
		if i%n == k-1 {
			owned = append(owned, pkg)
		}
	}
	if len(owned) == 0 {
		return nil, fmt.Errorf("shard %s owns none of the %d packages; use fewer shards", shard, len(sorted))
	}
	return owned, nil
}

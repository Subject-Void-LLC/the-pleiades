// Running a shard's pieces: its whole packages in one go test, and each
// package it owns only part of in its own, narrowed by -run or -skip.
package main

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// runPieces runs every piece: the whole ones together in one go test, as a
// run without pieces always did, then each part of a package alone, since a
// -run or -skip applies to every package of the invocation it is on. The
// events come back together, with the first exit error (waitErr), which a
// failing test also causes. A go test that produced nothing at all, which no
// later step can read anything from, is err.
func runPieces(args []string, pieces []piece) (events []flakegate.Event, waitErr, err error) {
	var whole []string
	for _, p := range pieces {
		if p.filter() == nil {
			whole = append(whole, p.pkg)
		}
	}
	run := func(args, packages []string) error {
		got, err := flakegate.RunGoTestJSONPackages(args, packages, testOutput)
		if err != nil && len(got) == 0 {
			return fmt.Errorf("go test produced no output at all for %v: %w", packages, err)
		}
		if waitErr == nil {
			waitErr = err
		}
		events = append(events, got...)
		return nil
	}
	if len(whole) > 0 {
		if err := run(args, whole); err != nil {
			return events, waitErr, err
		}
	}
	for _, p := range pieces {
		if p.filter() == nil {
			continue
		}
		if err := run(append(append([]string(nil), args...), p.filter()...), []string{p.pkg}); err != nil {
			return events, waitErr, err
		}
	}
	return events, waitErr, nil
}

// piecewise returns each package's go test arguments in this run: args, and
// for a package the run owns part of, the -run or -skip that narrows it, so a
// re-run or re-measurement of the package covers the same tests the run did
// rather than the whole package, other shards' tests included.
func piecewise(args []string, pieces []piece) func(pkg string) []string {
	narrowed := map[string][]string{}
	for _, p := range pieces {
		if f := p.filter(); f != nil {
			narrowed[p.pkg] = append(append([]string(nil), args...), f...)
		}
	}
	return func(pkg string) []string {
		if a, ok := narrowed[pkg]; ok {
			return a
		}
		return args
	}
}

// divided reports, for an import path among packages, whether
// durations.json times it test by test, so its tests' own times are
// recorded for -durations-check (flakegate.Measurement.TestSeconds) whether
// or not this run was a shard.
func divided(packages []string) func(pkg string) bool {
	split := splitTests(packages, durations())
	return func(pkg string) bool {
		_, ok := split[pkg]
		return ok
	}
}

// wholeCoverageOnly refuses a coverage number from part of a package: a
// percentage of some of its tests' reach is not the package's, and two
// shards' parts cannot be merged back into it (coverage-check refuses a
// package measured twice). Only a package with no statements of its own,
// which reports none, may be divided.
func wholeCoverageOnly(pieces []piece, coverage map[string]float64) error {
	for _, p := range pieces {
		if pct, ok := coverage[p.pkg]; ok && p.filter() != nil {
			return fmt.Errorf("%s is divided by test across shards, since tools/testgate/durations.json times it test by test, "+
				"but it has statements of its own (%.1f%% covered by this part), and a part's coverage cannot be merged into the whole's; "+
				"time it as one package there instead", p.pkg, pct)
		}
	}
	return nil
}

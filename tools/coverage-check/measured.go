// Reading coverage a test run already measured, instead of running the
// suite again to measure it (Phase 118).
//
// testgate -full records each package's coverage from the one test pass a
// gate makes, and CI splits that pass across machines, so the numbers for
// one run arrive as several files. This merges them, refuses a package
// measured twice, and names every package that has a floor and no number,
// which is how a package whose tests failed or never ran used to pass the
// floor check unexamined.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// readMeasured merges the measurements testgate -coverage-out wrote.
// A package in two files is an error: the tiers and shards partition the
// packages, so a duplicate means two runs disagree about who owns it, and
// picking one number would hide that. A file in any other shape is refused
// rather than read as measuring nothing.
func readMeasured(files []string) (flakegate.Measurement, error) {
	merged := flakegate.Measurement{Coverage: map[string]float64{}, Missing: map[string][]string{}}
	if len(files) == 0 {
		return merged, errors.New("-measured names no coverage files; a gate that measured nothing has nothing to check")
	}
	from := map[string]string{}
	for _, file := range files {
		raw, err := os.ReadFile(file) // #nosec G304 -- a path the Makefile or the workflow names
		if err != nil {
			return merged, fmt.Errorf("reading %s: %w", file, err)
		}
		var m flakegate.Measurement
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return merged, fmt.Errorf("reading %s: %w (written by an older testgate? run the gate again)", file, err)
		}
		if m.Coverage == nil {
			return merged, fmt.Errorf("reading %s: it holds no \"coverage\" object", file)
		}
		for pkg, pct := range m.Coverage {
			if earlier, dup := from[pkg]; dup {
				return merged, fmt.Errorf("%s is measured in both %s and %s", pkg, earlier, file)
			}
			merged.Coverage[pkg] = pct
			from[pkg] = file
		}
		for pkg, needs := range m.Missing {
			merged.Missing[pkg] = append(merged.Missing[pkg], needs...)
		}
	}
	return merged, nil
}

// unmeasured returns every package with a floor and no number, sorted.
// Absent is not a pass. Its tests failed before go test printed a
// percentage, or it was never run, or it no longer exists and its floor is
// stale; each of those is something to act on, and the old check read
// none of them (FAILURE_PATTERNS 397).
func unmeasured(floors *floorFile, measured map[string]float64) []string {
	var missing []string
	for pkg, floor := range floors.Floors {
		if _, excluded := floors.Excluded[pkg]; excluded {
			continue
		}
		if _, ok := measured[pkg]; !ok {
			missing = append(missing, fmt.Sprintf("%s: floor %.1f%%, no coverage number in this run (its tests failed or did not run, or the package is gone and its floor is stale)", pkg, floor))
		}
	}
	sort.Strings(missing)
	return missing
}

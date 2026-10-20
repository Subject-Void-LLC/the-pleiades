// Holding durations.json to what the containers jobs measured, so the shards
// stay balanced as packages are added and their tests grow.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// A recorded time is warned about when the measured one is more than
// driftFactor times it, or less than its 1/driftFactor, and the two are
// also more than driftFloor seconds apart, so a package that took 2 seconds
// instead of 1 is not news.
const (
	driftFactor = 2.0
	driftFloor  = 60.0
)

// durationsCheck reads the measurements files name (-coverage-out files of
// the container packages) and holds each package they timed to recorded,
// durations.json's times by path in the module. A package durations.json
// times test by test is held test by test instead, from the measurements'
// TestSeconds, since each shard timed only its own part of it. A package or
// test with no recorded time fails the check, naming the line to add: -shard
// would otherwise balance it as the median, which a fifteen-minute package
// is not. One whose time moved past the drift bounds is printed as a warning
// with its new time, so the table is refreshed before the shards drift apart
// again; that is not a failure, since one slow run on a busy runner is not a
// trend. So is a recorded test of a measured package that no measurement
// timed, which is a test since renamed or removed.
func durationsCheck(w io.Writer, files []string, recorded map[string]float64) error {
	if len(files) == 0 {
		return fmt.Errorf("-durations-check needs the measurement files to read")
	}
	measured := map[string]float64{}
	tests := map[string]float64{}
	for _, f := range files {
		data, err := os.ReadFile(f) // #nosec G304 -- a measurement file the Makefile names
		if err != nil {
			return err
		}
		var m flakegate.Measurement
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		for pkg, s := range m.Seconds {
			measured[strings.TrimPrefix(pkg, modulePrefix)] = s
		}
		for key, s := range m.TestSeconds {
			tests[strings.TrimPrefix(key, modulePrefix)] = s
		}
	}
	split := map[string]bool{}
	for key := range recorded {
		if pkg, _, ok := strings.Cut(key, " "); ok {
			split[pkg] = true
		}
	}
	timed := map[string]float64{}
	for p, s := range measured {
		if !split[p] {
			timed[p] = s
		}
	}
	for key, s := range tests {
		timed[key] = s
	}
	keys := make([]string, 0, len(timed))
	for k := range timed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var missing, moved, gone []string
	for _, p := range keys {
		got := math.Round(timed[p])
		want, ok := recorded[p]
		switch {
		case !ok:
			missing = append(missing, fmt.Sprintf("  %q: %.0f,", p, got))
		case math.Abs(got-want) > driftFloor && (got > want*driftFactor || got < want/driftFactor):
			moved = append(moved, fmt.Sprintf("  %s: recorded %.0fs, measured %.0fs", p, want, got))
		}
	}
	for key := range recorded {
		if pkg, _, ok := strings.Cut(key, " "); ok {
			if _, ran := measured[pkg]; ran {
				if _, timedIt := tests[key]; !timedIt {
					gone = append(gone, "  "+key)
				}
			}
		}
	}
	sort.Strings(gone)
	if len(moved) > 0 {
		fmt.Fprintf(w, "testgate: %d package(s) or test(s) took far from their time in tools/testgate/durations.json; refresh them so -shard stays balanced:\n%s\n", len(moved), strings.Join(moved, "\n"))
	}
	if len(gone) > 0 {
		fmt.Fprintf(w, "testgate: %d test(s) in tools/testgate/durations.json were not run by a package that was; remove them:\n%s\n", len(gone), strings.Join(gone, "\n"))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d measured package(s) or test(s) have no time in tools/testgate/durations.json, so -shard cannot balance them; add these lines:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	fmt.Fprintf(w, "testgate: durations.json holds a time for each of the %d measured package(s) and test(s)\n", len(keys))
	return nil
}

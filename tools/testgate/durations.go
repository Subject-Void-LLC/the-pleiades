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
// durations.json's times by path in the module. A package with no recorded
// time fails the check, naming the line to add: -shard would otherwise
// balance it as the median, which a fifteen-minute package is not. A
// package whose time moved past the drift bounds is printed as a warning
// with its new time, so the table is refreshed before the shards drift
// apart again; that is not a failure, since one slow run on a busy runner
// is not a trend.
func durationsCheck(w io.Writer, files []string, recorded map[string]float64) error {
	if len(files) == 0 {
		return fmt.Errorf("-durations-check needs the measurement files to read")
	}
	measured := map[string]float64{}
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
	}
	paths := make([]string, 0, len(measured))
	for p := range measured {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var missing, moved []string
	for _, p := range paths {
		got := math.Round(measured[p])
		want, ok := recorded[p]
		switch {
		case !ok:
			missing = append(missing, fmt.Sprintf("  %q: %.0f,", p, got))
		case math.Abs(got-want) > driftFloor && (got > want*driftFactor || got < want/driftFactor):
			moved = append(moved, fmt.Sprintf("  %s: recorded %.0fs, measured %.0fs", p, want, got))
		}
	}
	if len(moved) > 0 {
		fmt.Fprintf(w, "testgate: %d package(s) took far from their time in tools/testgate/durations.json; refresh them so -shard stays balanced:\n%s\n", len(moved), strings.Join(moved, "\n"))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d measured package(s) have no time in tools/testgate/durations.json, so -shard cannot balance them; add these lines:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	fmt.Fprintf(w, "testgate: durations.json holds a time for each of the %d measured package(s)\n", len(paths))
	return nil
}

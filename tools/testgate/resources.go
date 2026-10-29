// resources.json: what each unit the gate deals (a package, or a test of a
// package durations.json divides) has needed, measured, run after run: the
// seconds it took, the memory it cost and the disk it wrote, each as the
// least and the most ever recorded, with how many runs and which hosts
// measured it. It rides with the tests so a shard, a share or a local pass
// can be balanced by what the units really need, and tightened as the
// records say. -resources-record folds a run's measurements in;
// -resources-check holds a run's measurements to it.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// resourcesPath is where the manifest lives, from the repository root.
const resourcesPath = "tools/testgate/resources.json"

// resourcesSchema is the manifest's version. A reader refuses another.
const resourcesSchema = 1

// Resources is the manifest.
type Resources struct {
	// Schema is resourcesSchema.
	Schema int `json:"schema"`
	// Complete says every unit the gate runs is in Units, so a measured unit
	// that is not is a failure of -resources-check rather than a note; false
	// while the manifest is still being seeded from runs.
	Complete bool `json:"complete"`
	// Units are the measurements by unit: a package's path in the module,
	// or that path, a space and a test's name for a divided package's test.
	Units map[string]Unit `json:"units"`
}

// Unit is what one unit has needed over every run that measured it.
type Unit struct {
	// Seconds, MemoryMB and DiskMB are the least and the most recorded;
	// one never measured is absent.
	Seconds  *Range `json:"seconds,omitempty"`
	MemoryMB *Range `json:"memory_mb,omitempty"`
	DiskMB   *Range `json:"disk_mb,omitempty"`
	// Runs is how many measurements have been folded in.
	Runs int `json:"runs"`
	// Last is the date of the newest measurement, YYYY-MM-DD.
	Last string `json:"last,omitempty"`
	// Hosts are the GOOS/GOARCH pairs that measured it, sorted.
	Hosts []string `json:"hosts,omitempty"`
}

// Range is the least and the most of a quantity ever recorded.
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// widen folds v into r, which may be nil, and returns the range.
func (r *Range) widen(v float64) *Range {
	v = math.Round(v)
	if r == nil {
		return &Range{Min: v, Max: v}
	}
	r.Min, r.Max = min(r.Min, v), max(r.Max, v)
	return r
}

//go:embed resources.json
var resourcesJSON []byte

// resources is the embedded manifest read.
func resources() Resources {
	r, err := parseResources(resourcesJSON)
	if err != nil {
		panic(fmt.Sprintf("testgate: %s: %v", resourcesPath, err))
	}
	return r
}

// parseResources reads a manifest, refusing another schema.
func parseResources(data []byte) (Resources, error) {
	var r Resources
	if err := json.Unmarshal(data, &r); err != nil {
		return Resources{}, err
	}
	if r.Schema != resourcesSchema {
		return Resources{}, fmt.Errorf("schema %d, where this reader knows %d", r.Schema, resourcesSchema)
	}
	if r.Units == nil {
		r.Units = map[string]Unit{}
	}
	return r, nil
}

// measured is one run's numbers by unit, read from its measurement files
// (-coverage-out), the keys trimmed of the module prefix.
type measured struct {
	seconds, memory, disk map[string]float64
}

// readMeasurements reads the measurement files: a package's own numbers,
// and for a package the manifest or durations.json divides by test, its
// tests' numbers in place of the package's, since each shard measured only
// its own part.
func readMeasurements(files []string, divided map[string]bool) (measured, error) {
	m := measured{seconds: map[string]float64{}, memory: map[string]float64{}, disk: map[string]float64{}}
	for _, f := range files {
		data, err := os.ReadFile(f) // #nosec G304 -- a measurement file the Makefile names
		if err != nil {
			return m, err
		}
		var one flakegate.Measurement
		if err := json.Unmarshal(data, &one); err != nil {
			return m, fmt.Errorf("%s: %w", f, err)
		}
		fold := func(dst map[string]float64, src map[string]float64, tests bool) {
			for key, v := range src {
				key = strings.TrimPrefix(key, modulePrefix)
				pkg, _, isTest := strings.Cut(key, " ")
				if tests != isTest || (!isTest && divided[pkg]) {
					continue
				}
				dst[key] = v
			}
		}
		fold(m.seconds, one.Seconds, false)
		fold(m.seconds, one.TestSeconds, true)
		fold(m.memory, one.MemoryMB, false)
		fold(m.memory, one.TestMemoryMB, true)
		fold(m.disk, one.DiskMB, false)
		fold(m.disk, one.TestDiskMB, true)
	}
	return m, nil
}

// dividedUnits are the packages a manifest or durations.json names a test
// of, so the package's own number stands for nothing.
func dividedUnits(r Resources, durations map[string]float64) map[string]bool {
	out := map[string]bool{}
	for key := range r.Units {
		if pkg, _, ok := strings.Cut(key, " "); ok {
			out[pkg] = true
		}
	}
	for key := range durations {
		if pkg, _, ok := strings.Cut(key, " "); ok {
			out[pkg] = true
		}
	}
	return out
}

// record folds one run's measurements into r for host (GOOS/GOARCH) on
// date, widening each unit's ranges and counting the run, and returns the
// units it changed, sorted: added for a new one, widened for a range that
// moved, counted for one measured within its ranges.
func record(r *Resources, m measured, host, date string) (added, widened, counted []string) {
	keys := map[string]bool{}
	for _, src := range []map[string]float64{m.seconds, m.memory, m.disk} {
		for k := range src {
			keys[k] = true
		}
	}
	for key := range keys {
		u, known := r.Units[key]
		before := fmt.Sprint(u.Seconds, u.MemoryMB, u.DiskMB)
		if v, ok := m.seconds[key]; ok {
			u.Seconds = u.Seconds.widen(v)
		}
		if v, ok := m.memory[key]; ok {
			u.MemoryMB = u.MemoryMB.widen(v)
		}
		if v, ok := m.disk[key]; ok {
			u.DiskMB = u.DiskMB.widen(v)
		}
		u.Runs++
		u.Last = date
		if !contains(u.Hosts, host) {
			u.Hosts = append(u.Hosts, host)
			sort.Strings(u.Hosts)
		}
		r.Units[key] = u
		switch {
		case !known:
			added = append(added, key)
		case fmt.Sprint(u.Seconds, u.MemoryMB, u.DiskMB) != before:
			widened = append(widened, key)
		default:
			counted = append(counted, key)
		}
	}
	sort.Strings(added)
	sort.Strings(widened)
	sort.Strings(counted)
	return added, widened, counted
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// writeResources writes r to path, units sorted, whole or not at all. It
// writes through a handle rather than os.WriteFile because gosec's taint
// analysis (G703) reads the manifest's own bytes, built from the files named
// on the command line, as a path to traverse with, whatever the path is.
func writeResources(path string, r Resources) error {
	r.Schema = resourcesSchema
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp) // #nosec G304 -- the manifest's own path, plus a literal
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// resourcesRecord is -resources-record: it reads the measurement files,
// folds them into the manifest at path for this host today, writes it back,
// and says what changed.
func resourcesRecord(w io.Writer, path string, files []string, host string, now time.Time) error {
	if len(files) == 0 {
		return fmt.Errorf("-resources-record needs the measurement files to read")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- the manifest's own path
	if err != nil {
		return err
	}
	r, err := parseResources(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	m, err := readMeasurements(files, dividedUnits(r, durations()))
	if err != nil {
		return err
	}
	added, widened, counted := record(&r, m, host, now.UTC().Format("2006-01-02"))
	if err := writeResources(path, r); err != nil {
		return err
	}
	fmt.Fprintf(w, "testgate: %s now holds %d unit(s): %d added, %d with a range widened, %d measured within their ranges\n", path, len(r.Units), len(added), len(widened), len(counted))
	for _, key := range added {
		fmt.Fprintf(w, "  added %s: %s\n", key, describe(r.Units[key]))
	}
	for _, key := range widened {
		fmt.Fprintf(w, "  widened %s: %s\n", key, describe(r.Units[key]))
	}
	return nil
}

// describe says a unit's ranges in a line.
func describe(u Unit) string {
	var parts []string
	for _, q := range []struct {
		name string
		r    *Range
		unit string
	}{{"seconds", u.Seconds, "s"}, {"memory", u.MemoryMB, " MB"}, {"disk", u.DiskMB, " MB"}} {
		if q.r != nil {
			parts = append(parts, fmt.Sprintf("%s %.0f to %.0f%s", q.name, q.r.Min, q.r.Max, q.unit))
		}
	}
	if len(parts) == 0 {
		return "nothing measured"
	}
	return strings.Join(parts, ", ")
}

// resourcesCheck is -resources-check: it holds the measurement files' units
// to the manifest. A measured unit the manifest does not know is a failure
// when the manifest says it is complete, and named otherwise; one whose
// memory or disk went past its recorded most by more than driftFactor, or
// whose time did past the durations drift bounds, is named as moved, so the
// manifest is refreshed with -resources-record before a shard or a share
// is balanced on numbers that are no longer true. A move is never a
// failure: one run on a busy machine is not a trend.
func resourcesCheck(w io.Writer, files []string, r Resources) error {
	if len(files) == 0 {
		return fmt.Errorf("-resources-check needs the measurement files to read")
	}
	m, err := readMeasurements(files, dividedUnits(r, durations()))
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for _, src := range []map[string]float64{m.seconds, m.memory, m.disk} {
		for k := range src {
			keys[k] = true
		}
	}
	var unknown, moved []string
	for key := range keys {
		u, ok := r.Units[key]
		if !ok {
			unknown = append(unknown, key)
			continue
		}
		if v, ok := m.seconds[key]; ok && u.Seconds != nil && math.Abs(v-u.Seconds.Max) > driftFloor && v > u.Seconds.Max*driftFactor {
			moved = append(moved, fmt.Sprintf("  %s: recorded up to %.0fs, measured %.0fs", key, u.Seconds.Max, v))
		}
		if v, ok := m.memory[key]; ok && u.MemoryMB != nil && v > u.MemoryMB.Max*driftFactor {
			moved = append(moved, fmt.Sprintf("  %s: recorded up to %.0f MB of memory, measured %.0f MB", key, u.MemoryMB.Max, v))
		}
		if v, ok := m.disk[key]; ok && u.DiskMB != nil && v > u.DiskMB.Max*driftFactor {
			moved = append(moved, fmt.Sprintf("  %s: recorded up to %.0f MB of disk, measured %.0f MB", key, u.DiskMB.Max, v))
		}
	}
	sort.Strings(unknown)
	sort.Strings(moved)
	if len(moved) > 0 {
		fmt.Fprintf(w, "testgate: %d unit(s) needed far more than %s records; fold this run in with -resources-record:\n%s\n", len(moved), resourcesPath, strings.Join(moved, "\n"))
	}
	if len(unknown) > 0 {
		lines := "  " + strings.Join(unknown, "\n  ")
		if r.Complete {
			return fmt.Errorf("%d measured unit(s) are not in %s, which says it is complete; fold this run in with -resources-record:\n%s", len(unknown), resourcesPath, lines)
		}
		fmt.Fprintf(w, "testgate: %d measured unit(s) are not in %s yet (it is still being seeded); fold this run in with -resources-record:\n%s\n", len(unknown), resourcesPath, lines)
	}
	fmt.Fprintf(w, "testgate: %s holds %d unit(s); this run measured %d\n", resourcesPath, len(r.Units), len(keys))
	return nil
}

// packedParallelism is how many of packages may run at once on a machine
// with availableMB free, by what the manifest says each needs: the most
// memory it ever cost, or headroom's budget for one the manifest has no
// memory for. It is the largest count whose costliest members fit together
// after the reserve, never under 1 and never over want (or cpus, go test's
// default, for 0). The costliest members are the bound because go test picks
// its own order, so any count of them may run together.
func packedParallelism(r Resources, packages []string, availableMB float64, want, cpus, budgetMB, reserveMB int) int {
	if want <= 0 {
		want = max(cpus, 1)
	}
	if availableMB <= 0 || len(packages) == 0 {
		return want
	}
	costs := make([]float64, 0, len(packages))
	for _, p := range packages {
		cost := float64(budgetMB)
		if u, ok := r.Units[strings.TrimPrefix(p, modulePrefix)]; ok && u.MemoryMB != nil && u.MemoryMB.Max > 0 {
			cost = u.MemoryMB.Max
		}
		costs = append(costs, cost)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(costs)))
	room := availableMB - float64(reserveMB)
	fit := 0
	for _, c := range costs {
		if room < c || fit == want {
			break
		}
		room -= c
		fit++
	}
	return max(1, fit)
}

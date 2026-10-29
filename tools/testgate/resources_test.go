// Tests for the resources manifest: folding runs in, holding a run to it,
// and packing a pass by what its packages need.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// writeMeasurement writes m as a measurement file in dir and returns its
// path.
func writeMeasurement(t *testing.T, dir, name string, m flakegate.Measurement) string {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResources_RecordWidensAndCounts: a first run adds each unit with its
// numbers as both ends of its ranges; a second widens a range that moved and
// counts one within its ranges; a divided package's tests are recorded, not
// the package; the host and the date are kept; the file is written sorted.
func TestResources_RecordWidensAndCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resources.json")
	if err := os.WriteFile(path, []byte(`{"schema": 1, "complete": false, "units": {"tests/e2e TestOld": {"seconds": {"min": 5, "max": 5}, "runs": 1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first := writeMeasurement(t, dir, "first.json", flakegate.Measurement{
		Seconds:      map[string]float64{modulePrefix + "x/a": 12.4, modulePrefix + "tests/e2e": 900},
		MemoryMB:     map[string]float64{modulePrefix + "x/a": 300.6},
		DiskMB:       map[string]float64{modulePrefix + "x/a": 40},
		TestSeconds:  map[string]float64{modulePrefix + "tests/e2e TestGate": 200},
		TestMemoryMB: map[string]float64{modulePrefix + "tests/e2e TestGate": 6000},
	})
	var out bytes.Buffer
	if err := resourcesRecord(&out, path, []string{first}, "linux/amd64", time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	r, err := parseResources(data)
	if err != nil {
		t.Fatal(err)
	}
	a := r.Units["x/a"]
	if a.Seconds == nil || a.Seconds.Min != 12 || a.Seconds.Max != 12 || a.MemoryMB.Max != 301 || a.DiskMB.Min != 40 || a.Runs != 1 || a.Last != "2026-10-29" || strings.Join(a.Hosts, ",") != "linux/amd64" {
		t.Errorf("x/a after the first run = %+v", a)
	}
	if _, ok := r.Units["tests/e2e"]; ok {
		t.Error("a divided package was recorded whole")
	}
	if g := r.Units["tests/e2e TestGate"]; g.Seconds.Max != 200 || g.MemoryMB.Max != 6000 || g.DiskMB != nil {
		t.Errorf("TestGate = %+v", g)
	}
	if !strings.Contains(out.String(), "2 added, 0 with a range widened") || !strings.Contains(out.String(), "added x/a: seconds 12 to 12s, memory 301 to 301 MB, disk 40 to 40 MB") {
		t.Errorf("the first record said:\n%s", out.String())
	}

	second := writeMeasurement(t, dir, "second.json", flakegate.Measurement{
		Seconds:     map[string]float64{modulePrefix + "x/a": 9},
		MemoryMB:    map[string]float64{modulePrefix + "x/a": 450},
		TestSeconds: map[string]float64{modulePrefix + "tests/e2e TestGate": 190},
	})
	out.Reset()
	if err := resourcesRecord(&out, path, []string{second}, "darwin/arm64", time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	r, _ = parseResources(data)
	a = r.Units["x/a"]
	if a.Seconds.Min != 9 || a.Seconds.Max != 12 || a.MemoryMB.Min != 301 || a.MemoryMB.Max != 450 || a.DiskMB.Max != 40 || a.Runs != 2 || a.Last != "2026-10-30" || strings.Join(a.Hosts, ",") != "darwin/arm64,linux/amd64" {
		t.Errorf("x/a after the second run = %+v", a)
	}
	if !strings.Contains(out.String(), "0 added, 2 with a range widened") {
		t.Errorf("the second record said:\n%s", out.String())
	}
	if strings.Index(string(data), `"tests/e2e TestGate"`) > strings.Index(string(data), `"x/a"`) {
		t.Error("the file is not written with its units sorted")
	}
	if err := resourcesRecord(&out, path, nil, "linux/amd64", time.Now()); err == nil {
		t.Error("no files were accepted")
	}
}

// TestResources_Check: a unit the manifest knows within its ranges passes
// quietly; one far past its recorded most is named as moved, not failed; an
// unknown unit is named while the manifest is being seeded and fails it
// once it says it is complete.
func TestResources_Check(t *testing.T) {
	dir := t.TempDir()
	r := Resources{Schema: 1, Units: map[string]Unit{
		"x/a": {Seconds: &Range{Min: 10, Max: 20}, MemoryMB: &Range{Min: 200, Max: 400}, DiskMB: &Range{Min: 10, Max: 50}},
	}}
	file := writeMeasurement(t, dir, "m.json", flakegate.Measurement{
		Seconds:  map[string]float64{modulePrefix + "x/a": 19, modulePrefix + "x/new": 3},
		MemoryMB: map[string]float64{modulePrefix + "x/a": 900},
		DiskMB:   map[string]float64{modulePrefix + "x/a": 60},
	})
	var out bytes.Buffer
	if err := resourcesCheck(&out, []string{file}, r); err != nil {
		t.Fatalf("while seeding: %v", err)
	}
	for _, w := range []string{"x/a: recorded up to 400 MB of memory, measured 900 MB", "not in tools/testgate/resources.json yet", "  x/new", "holds 1 unit(s); this run measured 2"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("the check does not say %q:\n%s", w, out.String())
		}
	}
	if strings.Contains(out.String(), "disk") {
		t.Errorf("60 MB of disk against 50 recorded is within drift, yet it was named:\n%s", out.String())
	}
	r.Complete = true
	if err := resourcesCheck(&out, []string{file}, r); err == nil || !strings.Contains(err.Error(), "x/new") || !strings.Contains(err.Error(), "says it is complete") {
		t.Errorf("a complete manifest with an unknown unit: %v", err)
	}
	if err := resourcesCheck(&out, nil, r); err == nil {
		t.Error("no files were accepted")
	}
}

// TestPackedParallelism: the costliest packages are packed into the room
// after the reserve, an unknown package costs the budget, and the result is
// never under 1 nor over what was asked (or the CPU count for 0).
func TestPackedParallelism(t *testing.T) {
	r := Resources{Schema: 1, Units: map[string]Unit{
		"x/big":   {MemoryMB: &Range{Min: 900, Max: 1000}},
		"x/small": {MemoryMB: &Range{Min: 50, Max: 100}},
		"x/tiny":  {MemoryMB: &Range{Min: 10, Max: 20}},
	}}
	pkgs := []string{modulePrefix + "x/big", modulePrefix + "x/small", modulePrefix + "x/tiny", modulePrefix + "x/unknown"}
	for _, tc := range []struct {
		name       string
		available  float64
		want, cpus int
		expect     int
	}{
		{"room for all", 10000, 0, 8, 4},
		{"room for the two costliest (1000 + 1200 budget)", 1000 + 2200, 0, 8, 2},
		{"room for one", 1000 + 1500, 0, 8, 1},
		{"short still runs one", 500, 0, 8, 1},
		{"the ask bounds it", 10000, 2, 8, 2},
		{"the CPU count bounds a default ask", 10000, 0, 3, 3},
		{"no reading keeps the ask", 0, 5, 8, 5},
	} {
		if got := packedParallelism(r, pkgs, tc.available, tc.want, tc.cpus, 1200, 1000); got != tc.expect {
			t.Errorf("%s: packedParallelism = %d, want %d", tc.name, got, tc.expect)
		}
	}
}

// TestResources_EmbeddedManifestReads: the manifest shipped with the tool
// parses, is this schema, and each of its units has a measurement.
func TestResources_EmbeddedManifestReads(t *testing.T) {
	r := resources()
	if r.Schema != resourcesSchema {
		t.Fatalf("schema %d", r.Schema)
	}
	for key, u := range r.Units {
		if u.Seconds == nil && u.MemoryMB == nil && u.DiskMB == nil {
			t.Errorf("%s has no measurement", key)
		}
		if u.Runs < 1 {
			t.Errorf("%s was measured by no run", key)
		}
	}
	if _, err := parseResources([]byte(`{"schema": 2}`)); err == nil || !strings.Contains(err.Error(), "schema 2") {
		t.Errorf("another schema was read: %v", err)
	}
}

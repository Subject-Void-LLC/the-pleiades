// Tests for folding a lab leg's record into the resources manifest.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// TestResources_RecordFoldsALegUnderItsMachine: a leg's runs are folded in
// under the machine's own GOOS/GOARCH, beside this host's measurement, a run
// with no headroom adds nothing, a divided package's own number is left
// out, and the packer then sizes a pass on that machine by what it cost
// there: a package the lab measured past the room runs the pass one at a
// time.
func TestResources_RecordFoldsALegUnderItsMachine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resources.json")
	if err := os.WriteFile(path, []byte(`{"schema": 1, "complete": false, "units": {"tests/e2e TestOld": {"seconds": {"min": 5, "max": 5}, "runs": 1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	own := writeMeasurement(t, dir, "fast.json", flakegate.Measurement{
		MemoryMB: map[string]float64{modulePrefix + "tools/release": 670},
	})
	leg := filepath.Join(dir, "bsd.json")
	if err := oscheck.WriteLegRecord(leg, oscheck.LegRecord{Tool: "bsdcheck", OS: "FreeBSD", Target: "bsd-check", Runs: []flakegate.Run{
		{OS: "freebsd", Arch: "amd64", Headroom: &flakegate.Headroom{
			PackageMB:     map[string]float64{modulePrefix + "tools/release": 3111, modulePrefix + "tests/e2e": 9000},
			PackageDiskMB: map[string]float64{modulePrefix + "tools/release": 2876},
		}},
		{OS: "freebsd", Arch: "amd64"},
	}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := resourcesRecord(&out, path, []string{own, leg}, "linux/amd64", time.Date(2026, 10, 29, 17, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	r, err := parseResources(data)
	if err != nil {
		t.Fatal(err)
	}
	u := r.Units["tools/release"]
	if u.MemoryMB == nil || u.MemoryMB.Min != 670 || u.MemoryMB.Max != 3111 || u.DiskMB == nil || u.DiskMB.Max != 2876 || u.Runs != 2 || strings.Join(u.Hosts, ",") != "freebsd/amd64,linux/amd64" {
		t.Errorf("tools/release = %+v", u)
	}
	if _, ok := r.Units["tests/e2e"]; ok {
		t.Error("a divided package's own number was recorded from the leg")
	}
	if !strings.Contains(out.String(), "1 added, 0 with a range widened") {
		t.Errorf("the record said:\n%s", out.String())
	}
	if p := packedParallelism(r, []string{modulePrefix + "tools/release", modulePrefix + "x/a"}, 3534, 0, 4, 1200, 1024); p != 1 {
		t.Errorf("a 3534 MB machine runs %d at once beside a 3111 MB package, want 1", p)
	}

	// A leg run naming no machine, a leg of another schema, and a file that
	// is not JSON are refused, and the manifest is left as it was.
	before, _ := os.ReadFile(path)
	nameless := filepath.Join(dir, "nameless.json")
	if err := oscheck.WriteLegRecord(nameless, oscheck.LegRecord{Tool: "wincheck", Runs: []flakegate.Run{{Headroom: &flakegate.Headroom{}}}}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.json")
	broken := filepath.Join(dir, "broken.json")
	for f, body := range map[string]string{other: `{"schema": 9, "kind": "leg"}`, broken: `{not json`} {
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, f := range map[string]string{"a run naming no machine": nameless, "another schema": other, "not JSON": broken} {
		if err := resourcesRecord(&out, path, []string{f}, "linux/amd64", time.Now()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("a refused file changed the manifest")
	}
	if err := resourcesRecord(&out, path, []string{filepath.Join(dir, "missing.json")}, "linux/amd64", time.Now()); err == nil {
		t.Error("a missing file was accepted")
	}
}

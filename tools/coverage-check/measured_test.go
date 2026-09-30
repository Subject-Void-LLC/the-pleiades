// Tests for checking floors against coverage a gate already measured.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inDir writes files into a fresh directory and changes into it, as run
// reads coverage-floor.json from the working directory.
func inDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })
}

// floors is a floor file with two floored packages and one excluded.
const floors = `{"floors": {"example/a": 90.0, "example/b": 80.0}, "excluded": {"example/gen": "generated"}}`

// TestMeasured_MergesShardsAndChecksFloors is the ordinary case: two shard
// files, together covering every floored package, each at or above its
// floor.
func TestMeasured_MergesShardsAndChecksFloors(t *testing.T) {
	inDir(t, map[string]string{
		"coverage-floor.json": floors,
		"fast.json":           `{"example/a": 91.0}`,
		"shard1.json":         `{"example/b": 80.0, "example/new": 12.0}`,
	})
	if err := run(false, []string{"fast.json", "shard1.json"}); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestMeasured_Refusals covers every way -measured must fail.
func TestMeasured_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		args    []string
		wantErr string
	}{
		{"below its floor", map[string]string{"c.json": `{"example/a": 85.0, "example/b": 80.0}`}, []string{"c.json"}, "regressed"},
		{"a floored package with no number", map[string]string{"c.json": `{"example/a": 95.0}`}, []string{"c.json"}, "regressed"},
		{"a package measured twice", map[string]string{"x.json": `{"example/a": 95.0}`, "y.json": `{"example/a": 96.0, "example/b": 90.0}`}, []string{"x.json", "y.json"}, "measured in both"},
		{"a file that is not there", map[string]string{}, []string{"gone.json"}, "reading gone.json"},
		{"a file that is not JSON", map[string]string{"c.json": `ok github.com/x 91%`}, []string{"c.json"}, "reading c.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"coverage-floor.json": floors}
			for k, v := range tt.files {
				files[k] = v
			}
			inDir(t, files)
			err := run(false, tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestUnmeasured_NamesEveryMissingFloorAndSkipsExclusions proves the
// missing list names the package and its floor, and leaves an excluded
// package alone even when it has a floor.
func TestUnmeasured_NamesEveryMissingFloorAndSkipsExclusions(t *testing.T) {
	f := &floorFile{
		Floors:   map[string]float64{"example/a": 90, "example/b": 80, "example/gen": 10},
		Excluded: map[string]string{"example/gen": "generated"},
	}
	missing := unmeasured(f, map[string]float64{"example/a": 91})
	if len(missing) != 1 || !strings.HasPrefix(missing[0], "example/b: floor 80.0%") {
		t.Fatalf("missing = %v, want only example/b", missing)
	}
}

// TestReadMeasured_RefusesNoFiles proves an empty list is not a pass.
func TestReadMeasured_RefusesNoFiles(t *testing.T) {
	if _, err := readMeasured(nil); err == nil {
		t.Fatal("no files was accepted")
	}
}

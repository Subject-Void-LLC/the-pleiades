// Tests for checking floors against coverage a gate already measured.
package main

import (
	"io"
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
		"fast.json":           `{"coverage": {"example/a": 91.0}}`,
		"shard1.json":         `{"coverage": {"example/b": 80.0, "example/new": 12.0}}`,
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
		{"below its floor", map[string]string{"c.json": `{"coverage": {"example/a": 85.0, "example/b": 80.0}}`}, []string{"c.json"}, "regressed"},
		{"a floored package with no number", map[string]string{"c.json": `{"coverage": {"example/a": 95.0}}`}, []string{"c.json"}, "regressed"},
		{"a package measured twice", map[string]string{"x.json": `{"coverage": {"example/a": 95.0}}`, "y.json": `{"coverage": {"example/a": 96.0, "example/b": 90.0}}`}, []string{"x.json", "y.json"}, "measured in both"},
		{"a file that is not there", map[string]string{}, []string{"gone.json"}, "reading gone.json"},
		{"a file that is not JSON", map[string]string{"c.json": `ok github.com/x 91%`}, []string{"c.json"}, "reading c.json"},
		{"a file in the old shape", map[string]string{"c.json": `{"example/a": 95.0, "example/b": 90.0}`}, []string{"c.json"}, "older testgate"},
		{"a file with no coverage", map[string]string{"c.json": `{"missing": {}}`}, []string{"c.json"}, "no \"coverage\""},
		{"below its floor, another package lacking something", map[string]string{"c.json": `{"coverage": {"example/a": 85.0, "example/b": 80.0}, "missing": {"example/b": ["localstack"]}}`}, []string{"c.json"}, "regressed"},
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

// TestMeasured_AFloorMeasuredWithoutItsRequirementIsNamedNotFailed is the
// LocalStack case: a floor recorded where LocalStack ran cannot be checked
// by a run without it, which says so rather than failing a change that
// never touched the package, or passing it silently.
func TestMeasured_AFloorMeasuredWithoutItsRequirementIsNamedNotFailed(t *testing.T) {
	inDir(t, map[string]string{
		"coverage-floor.json": floors,
		"c.json":              `{"coverage": {"example/a": 37.6, "example/b": 80.0}, "missing": {"example/a": ["localstack"]}}`,
	})
	out := captureStdout(t, func() {
		if err := run(false, []string{"c.json"}); err != nil {
			t.Fatalf("run: %v", err)
		}
	})
	if !strings.Contains(out, "example/a: 37.6%, below its floor of 90.0%, measured without localstack") {
		t.Fatalf("the uncheckable floor is not named:\n%s", out)
	}
}

// captureStdout returns what fn printed to standard output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	_ = w.Close()
	return <-done
}

// Tests of adhoc's --timeout, which takes whole seconds or a duration, and
// of init --dir, which makes the directory it is given.
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestSecondsFlag: 40, 40s and 5m are seconds; a fraction of a second and
// text that is neither are refused.
func TestSecondsFlag(t *testing.T) {
	for text, want := range map[string]int{"40": 40, "40s": 40, "5m": 300, "0": 0} {
		var s secondsFlag
		if err := s.Set(text); err != nil || int(s) != want || s.String() != strconv.Itoa(want) {
			t.Errorf("Set(%q) = %d, %v; want %d", text, s, err, want)
		}
	}
	for _, text := range []string{"1.5s", "forty", "500ms"} {
		var s secondsFlag
		if err := s.Set(text); err == nil {
			t.Errorf("Set(%q) was accepted as %d", text, s)
		}
	}
}

// TestInit_MakesItsDirectory: init --dir into a directory that does not
// exist makes it and scaffolds the project there.
func TestInit_MakesItsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "project")
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "inventory.yaml")); err != nil {
		t.Errorf("no inventory.yaml in the new directory: %v", err)
	}
}

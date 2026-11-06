// Package main: tests that --setting parses each run setting once.
package main

import (
	"strings"
	"testing"
)

// TestSettingFlag_ParsesEachOnce proves --setting takes
// <method>.<parameter>=<value>, keeps every one given, and refuses one with no
// value or given twice, so a run never quietly takes the last of two.
func TestSettingFlag_ParsesEachOnce(t *testing.T) {
	fs := newFlagSetForTest()
	f := addRunFlags(fs)
	if err := fs.Parse([]string{"--setting", "file.remove.check_forbidden=warn", "--setting", "a.b.c=d=e"}); err != nil {
		t.Fatal(err)
	}
	opts, err := f.options()
	if err != nil {
		t.Fatal(err)
	}
	if opts.settings["file.remove.check_forbidden"] != "warn" || opts.settings["a.b.c"] != "d=e" {
		t.Errorf("settings = %v, want both, the second split at its first =", opts.settings)
	}
	for _, args := range [][]string{
		{"--setting", "file.remove.check_forbidden"},
		{"--setting", "=warn"},
		{"--setting", "a.b=c", "--setting", "a.b=d"},
	} {
		fs := newFlagSetForTest()
		addRunFlags(fs)
		if err := fs.Parse(args); err == nil || !strings.Contains(err.Error(), "-setting") {
			t.Errorf("%v: %v, want --setting refused", args, err)
		}
	}
}

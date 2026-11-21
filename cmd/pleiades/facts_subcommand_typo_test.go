// Tests through the real binary that a misspelled `pleiades facts`
// subcommand is refused naming the subcommand, rather than read as a host.
package main_test

import (
	"strings"
	"testing"
)

// TestCLI_FactsMisspelledSubcommandIsNotReadAsAHost: a first word one edit
// from baseline or drift that no host is named is refused naming the
// subcommand, with or without the subcommand's own flags after it, while a
// host of that name is still read.
func TestCLI_FactsMisspelledSubcommandIsNotReadAsAHost(t *testing.T) {
	dir, _ := baselineProject(t, "drfit")
	out, err := runPleiades(t, dir, "facts", "baselin", "web1", "--name", "x", "--reason", "r")
	if err == nil || !strings.Contains(out, "did you mean the subcommand: pleiades facts baseline") {
		t.Errorf("facts baselin: %v\n%s", err, out)
	}
	out, err = runPleiades(t, dir, "facts", "dirft")
	if err == nil || !strings.Contains(out, "did you mean the subcommand: pleiades facts drift") {
		t.Errorf("facts dirft: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "facts", "drfit"); err != nil || !strings.Contains(out, "has no facts") {
		t.Errorf("a host named drfit: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "facts", "nosuchhost"); err == nil || strings.Contains(out, "did you mean") {
		t.Errorf("a host near no subcommand: %v\n%s", err, out)
	}
}

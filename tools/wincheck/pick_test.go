// Tests that -arm64 selects the Windows ARM machine's check and leaves the
// rest of the command line to it.
package main

import (
	"reflect"
	"testing"
)

func TestPick(t *testing.T) {
	c, rest := pick([]string{"-arm64", "-optional", "a/b"})
	if c.Tool != "winarmcheck" || c.EnvFile != "tests/winarm/.env" || c.Box.Info != "virt.fusion.vm.info" || !reflect.DeepEqual(rest, []string{"-optional", "a/b"}) {
		t.Errorf("-arm64: %+v %q", c, rest)
	}
	c, rest = pick([]string{"-optional", "a/b"})
	if c.Tool != "wincheck" || c.EnvFile != "tests/win/.env" || c.Box.Info != "" || len(rest) != 2 {
		t.Errorf("default: %+v %q", c, rest)
	}
	// Only first: anywhere else it is an unknown flag the check refuses.
	if c, _ := pick([]string{"-optional", "-arm64"}); c.Tool != "wincheck" {
		t.Errorf("-arm64 after another flag selected %s", c.Tool)
	}
}

// Tests that asking any command for help is not a failure: every command in
// the table, and journal's own subcommands, exits 0 for --help and -h, and a
// command missing its argument without asking still fails.
package main

import (
	"os"
	"sort"
	"testing"
)

func TestEveryCommandsHelpExitsZero(t *testing.T) {
	t.Chdir(t.TempDir())
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	stdin, stderr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = devNull, devNull
	defer func() { os.Stdin, os.Stderr = stdin, stderr }()

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	asked := [][]string{{"journal", "list", "--help"}, {"journal", "show", "-h"}}
	for _, name := range names {
		asked = append(asked, []string{name, "--help"}, []string{name, "-h"})
	}
	for _, args := range asked {
		var code int
		captureStdout(t, func() { code = run(args) })
		if code != 0 {
			t.Errorf("pleiades %v exits %d", args, code)
		}
	}
	for _, args := range [][]string{{"add-host"}, {"adhoc"}, {"journal"}, {"remove-host"}} {
		var code int
		captureStdout(t, func() { code = run(args) })
		if code == 0 {
			t.Errorf("pleiades %v, missing its argument, exits 0", args)
		}
	}
}

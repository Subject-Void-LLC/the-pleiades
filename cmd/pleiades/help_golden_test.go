// Tests that asking a command for help prints exactly what it printed when
// the golden file was written: testdata/help_golden.json holds, for each
// command line, its exit status and every byte it wrote to stdout and
// stderr. The owner decided (2026-10-22) that a bare --help stays as
// designed, so a change to how arguments are parsed must not change it.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateHelpGolden rewrites testdata/help_golden.json from this build
// instead of checking against it.
var updateHelpGolden = flag.Bool("update-help-golden", false, "rewrite testdata/help_golden.json from this build")

// helpGoldenPath is the golden file, relative to this package.
const helpGoldenPath = "testdata/help_golden.json"

// helpAnswer is what one command line printed and exited with.
type helpAnswer struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	original := os.Stderr
	os.Stderr = w
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	func() {
		defer func() {
			os.Stderr = original
			_ = w.Close()
		}()
		fn()
	}()
	out := <-done
	_ = r.Close()
	return string(out)
}

// helpCommandLines is every top-level command asked with --help and with
// -h, the nested commands that split their own arguments, and commands
// asked for help after their positionals were given.
func helpCommandLines() [][]string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines [][]string
	for _, name := range names {
		lines = append(lines, []string{name, "--help"}, []string{name, "-h"})
	}
	return append(lines,
		[]string{"journal", "list", "--help"},
		[]string{"journal", "show", "-h"},
		[]string{"ai", "review", "show", "--help"},
		[]string{"ai", "review", "approve", "-h"},
		[]string{"ai", "prompt", "sign", "--help"},
		[]string{"collection", "approve", "--help"},
		[]string{"collection", "revoke", "-h"},
		[]string{"forge", "new-collection", "--help"},
		[]string{"forge", "new-device", "-h"},
		[]string{"forge", "migrate-playbook", "--help"},
		[]string{"add-host", "web1", "--help"},
		[]string{"add-host", "--help", "web1"},
		[]string{"remove-host", "web1", "-h"},
		[]string{"adhoc", "web1", "exec.command", "--help"},
		[]string{"adhoc", "web1", "--help"},
		[]string{"rollback", "run-1", "--help"},
		[]string{"grant-capability", "web1", "acme.thing", "-h"},
		[]string{"grant-capability", "web1", "--help"},
		[]string{"facts", "web1", "--help"},
		[]string{"doc", "exec.command", "--help"},
		[]string{"validate", "a.yaml", "-h"},
		[]string{"import", "awx-credential-types", "--help"},
		[]string{"import", "awx-credential-types", "-h"},
	)
}

// TestHelpOutputIsUnchanged runs every line in helpCommandLines through
// the CLI's own run, in an empty directory, and compares what it printed
// and its exit status with the golden file.
func TestHelpOutputIsUnchanged(t *testing.T) {
	golden, err := filepath.Abs(helpGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	stdin := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = stdin }()

	got := map[string]helpAnswer{}
	for _, args := range helpCommandLines() {
		var a helpAnswer
		a.Stderr = captureStderr(t, func() {
			a.Stdout = captureStdout(t, func() { a.Code = run(args) })
		})
		got[strings.Join(args, " ")] = a
	}

	if *updateHelpGolden {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the golden file (go test -run TestHelpOutputIsUnchanged -update-help-golden writes it): %v", err)
	}
	var want map[string]helpAnswer
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for line, w := range want {
		g, ok := got[line]
		switch {
		case !ok:
			t.Errorf("pleiades %s is in the golden file but was not run", line)
		case g != w:
			t.Errorf("pleiades %s changed:\n got code %d, stdout %q, stderr %q\nwant code %d, stdout %q, stderr %q", line, g.Code, g.Stdout, g.Stderr, w.Code, w.Stdout, w.Stderr)
		}
	}
	for line := range got {
		if _, ok := want[line]; !ok {
			t.Errorf("pleiades %s is not in the golden file; rewrite it with -update-help-golden", line)
		}
	}
}

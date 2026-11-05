// Tests that a family's source is read or the run says it was not (sweep item
// 1, FAILURE_PATTERNS 925): through the real SSH harness and a real /bin/sh,
// with PATH narrowed to a directory of small read-only stand-ins for the
// tools the probes call. No stand-in changes anything on this machine.
package facts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// toolsOnly sets PATH to a fresh directory holding sh and only tools, each
// a script with the given body, and returns it.
func toolsOnly(t *testing.T, tools map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	// Every probe runs as sh -c, found on PATH.
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("this machine has no sh on PATH: %v", err)
	}
	if err := os.Symlink(sh, filepath.Join(dir, "sh")); err != nil {
		t.Skipf("cannot link sh here: %v", err)
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { // #nosec G306 -- a test's stand-in tool must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// TestGather_AFamilyIsReadOrTheRunSaysItWasNot covers each way a family's
// source can answer: read (its items are facts), absent from the device (a
// task naming the subset fails, one reaching it through all records a
// warning), failed (the task fails whatever else answered, since a failed
// listing read as every item removed), and an answer without the structure
// its source always prints (the same).
func TestGather_AFamilyIsReadOrTheRunSaysItWasNot(t *testing.T) {
	cases := map[string]struct {
		tools    map[string]string
		params   map[string]any
		wantErr  string
		wantFact string
		warns    []string
		noWarn   string
	}{
		"a source that answers": {
			tools:    map[string]string{"dpkg-query": `printf 'ii \thello\t1.0\tamd64\n'`},
			params:   map[string]any{"gather_subset": []any{"packages"}},
			wantFact: "package.hello",
		},
		"a source whose listing fails": {
			tools:   map[string]string{"dpkg-query": `printf 'ii \thello\t1.0\tamd64\n'`, "rpm": `echo 'error: rpmdb: lock held' >&2; exit 1`},
			params:  map[string]any{"gather_subset": []any{"packages"}},
			wantErr: "could not read package facts",
		},
		"no source, the subset named": {
			params:  map[string]any{"gather_subset": []any{"packages"}},
			wantErr: "no source this method reads for package facts",
		},
		"no source, reached through all": {
			params: map[string]any{"gather_subset": []any{"all", "!users", "!ports"}},
			warns:  []string{"package facts", "service facts"},
		},
		"no source, all with one left out": {
			params: map[string]any{"gather_subset": []any{"!packages", "all", "!users", "!ports"}},
			// packages is left out whatever its place, so only services warns.
			warns:  []string{"service facts"},
			noWarn: "package facts",
		},
		"a filter matching the key a second database's item is stored under": {
			tools: map[string]string{
				"dpkg-query": `printf 'ii \thello\t1.0\tamd64\n'`,
				"rpm":        `printf 'hello\t(none)\t2.0-1\tx86_64\n'`,
			},
			params:   map[string]any{"gather_subset": []any{"packages"}, "filter": []any{"package.hello:*"}},
			wantFact: "package.hello:x86_64",
		},
		"a filter naming a family alone": {
			params:  map[string]any{"gather_subset": []any{"packages"}, "filter": []any{"package"}},
			wantErr: "matches none of the facts",
		},
		"an answer without its structure": {
			tools:   map[string]string{"cut": `exit 0`},
			params:  map[string]any{"gather_subset": []any{"users"}},
			wantErr: "listed nothing",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := startGatherServer(t)
			rc := newGatherContext(server)
			toolsOnly(t, tc.tools)
			_, err := gatherRun(t, server, rc, tc.params)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Gather = %v, want an error naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Gather: %v", err)
			}
			if tc.wantFact != "" {
				if _, ok := rc.facts[tc.wantFact]; !ok {
					t.Errorf("facts = %v, want %s", rc.facts, tc.wantFact)
				}
			}
			warnings, _ := rc.stats[sdk.StatWarnings].([]string)
			for _, want := range tc.warns {
				found := false
				for _, w := range warnings {
					found = found || strings.Contains(w, want)
				}
				if !found {
					t.Errorf("warnings = %q, want one naming %q", warnings, want)
				}
			}
			for _, w := range warnings {
				if tc.noWarn != "" && strings.Contains(w, tc.noWarn) {
					t.Errorf("warnings = %q, want none naming %q", warnings, tc.noWarn)
				}
			}
			if len(tc.warns) == 0 && len(warnings) > 0 {
				t.Errorf("warnings = %q, want none", warnings)
			}
		})
	}
}

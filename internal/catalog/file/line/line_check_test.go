// Package line_test: tests of file.line.set's and file.line.remove's checks.
package line_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file/line"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestLineChecks_PredictWhatARealRunLeaves runs each line method's
// registered check on a recording SSH harness, with the real run from the
// same file as its control: only reads are sent (stat, and the file's
// text) and the file is untouched; the change prediction matches the run
// (an edit that renders back to the same bytes predicts none); the
// predicted diff, whole new text included, matches what the run leaves on
// every key it states; the stats match; and no undo is recorded.
func TestLineChecks_PredictWhatARealRunLeaves(t *testing.T) {
	const start = "deb http://keep.example.com main\ndeb http://old.example.com main\n"
	for _, tc := range []struct {
		name        string
		fqcn        string
		params      map[string]any
		run         collection.Method
		wantChanged bool
	}{
		{"set replaces a line", "file.line.set", map[string]any{"regexp": `^deb http://(old|new)\.example`, "line": "deb http://new.example.com main"}, line.Set, true},
		{"set adds a line", "file.line.set", map[string]any{"line": "deb http://added.example.com main"}, line.Set, true},
		{"set, already there", "file.line.set", map[string]any{"line": "deb http://keep.example.com main"}, line.Set, false},
		{"remove takes a line out", "file.line.remove", map[string]any{"regexp": `old\.example`}, line.Remove, true},
		{"remove, nothing matches", "file.line.remove", map[string]any{"regexp": `nowhere\.example`}, line.Remove, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			srv, err := remoteexectest.Start(remoteexectest.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			server := lineServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
			params := func(path string) map[string]any {
				p := map[string]any{"path": path}
				for k, v := range tc.params {
					p[k] = v
				}
				return lineParams(p)
			}

			checkPath := lineWriteFile(t, start, 0o640)
			checkRC := newLineContext(server)
			checked, err := d.Check(context.Background(), checkRC, newLineTarget(server), params(checkPath))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			for _, c := range srv.Commands() {
				if !strings.HasPrefix(c, "if [ -e ") && !strings.HasPrefix(c, "if [ -f ") {
					t.Errorf("the check sent %q, which is not a read", c)
				}
			}
			if got := lineOnDisk(t, checkPath); got != start {
				t.Errorf("the check changed the file to %q", got)
			}
			if checked.Changed != tc.wantChanged {
				t.Errorf("the check predicts changed %v, want %v", checked.Changed, tc.wantChanged)
			}
			if _, recorded := checkRC.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}

			runPath := lineWriteFile(t, start, 0o640)
			runRC := newLineContext(server)
			ran, err := tc.run(context.Background(), runRC, newLineTarget(server), params(runPath))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if ran.Changed != checked.Changed {
				t.Errorf("the run changed %v, the check predicted %v", ran.Changed, checked.Changed)
			}
			predicted, actual := lineDiffHalf(t, checkRC, "after"), lineDiffHalf(t, runRC, "after")
			if len(predicted) == 0 {
				t.Fatal("the check predicted nothing")
			}
			for key, want := range predicted {
				if actual[key] != want {
					t.Errorf("predicted %s = %v, the run left %v", key, want, actual[key])
				}
			}
			for _, key := range []string{"msg", "found"} {
				if checkRC.stats[key] != runRC.stats[key] {
					t.Errorf("stat %s: check %v, run %v", key, checkRC.stats[key], runRC.stats[key])
				}
			}
		})
	}
}

// TestLineChecks_FailWhenTheyCannotRecord covers a check that has decided
// on a change and cannot record it: the diff, or the stats a later task
// reads. Either fails the check as it fails the real run, rather than
// reporting a change with nothing behind it, and the file is left as it
// was.
func TestLineChecks_FailWhenTheyCannotRecord(t *testing.T) {
	const start = "deb http://keep.example.com main\ndeb http://old.example.com main\n"
	for _, tc := range []struct {
		name   string
		fqcn   string
		params map[string]any
		failOn string
	}{
		{"set, the diff", "file.line.set", map[string]any{"line": "deb http://added.example.com main"}, sdk.StatDiff},
		{"set, the stats", "file.line.set", map[string]any{"line": "deb http://added.example.com main"}, "msg"},
		{"remove, the diff", "file.line.remove", map[string]any{"regexp": `old\.example`}, sdk.StatDiff},
		{"remove, the stats", "file.line.remove", map[string]any{"regexp": `old\.example`}, "found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := collection.Lookup(tc.fqcn)
			server := startLineServer(t)
			path := lineWriteFile(t, start, 0o640)
			p := map[string]any{"path": path}
			for k, v := range tc.params {
				p[k] = v
			}
			rc := newLineContext(server)
			rc.failOn = tc.failOn
			_, err := d.Check(context.Background(), rc, newLineTarget(server), lineParams(p))
			if err == nil || !strings.Contains(err.Error(), tc.fqcn) {
				t.Errorf("check error = %v, want one naming %s", err, tc.fqcn)
			}
			if got := lineOnDisk(t, path); got != start {
				t.Errorf("the check changed the file to %q", got)
			}
		})
	}
}

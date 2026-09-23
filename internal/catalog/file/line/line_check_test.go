// Package line_test: tests of file.line.set's and file.line.remove's checks.
package line_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

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
			beforeRun := lineMtime(t, runPath)
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
				// mtime is a clock, and it is the clock of a DIFFERENT
				// file. The check acts on checkPath and the run on runPath,
				// written a moment apart by lineWriteFile, so when nothing
				// changes the prediction reports the check file's own mtime
				// and the run leaves the run file's own. Equality held only
				// when both writes landed in the same second, which is
				// FAILURE_PATTERNS.md #294 in facts.gather and #301 here,
				// where it was recorded and left because the branch that
				// found it did not own this package.
				//
				// Compared as a bound instead, the way #294 was fixed: the
				// run's file is written after the check's, so its mtime is
				// no earlier, and a minute is far more than the gap between
				// two local writes. For a CHANGE, mtime never reaches this
				// loop at all, because PredictApply leaves it out of the
				// prediction rather than guessing a future write time.
				if key == "mtime" {
					assertMtimeFollows(t, want, actual[key])
					continue
				}
				if actual[key] != want {
					t.Errorf("predicted %s = %v, the run left %v", key, want, actual[key])
				}
			}

			// The guarantee the bound above would otherwise give away. A
			// relaxed comparison is only safe while something still holds
			// the property equality was standing in for, which is that a
			// run that changes nothing does not touch the file.
			//
			// READ FROM THE DISK, NOT FROM THE DIFF, and the difference is
			// not a matter of taste. The first version of this compared
			// the run's own recorded before and after, and a deliberate
			// fault walked straight past it: for a run that changes
			// nothing, sdk.Unchanged records Diff{Before: state, After:
			// state}, the SAME map for both halves. The "after" of a no-op
			// is not observed at all, it is the "before" reused, so that
			// comparison set a map beside itself and could never fail. The
			// diff therefore cannot say whether a no-op touched the file.
			// Only the file can.
			if !ran.Changed {
				if afterRun := lineMtime(t, runPath); !afterRun.Equal(beforeRun) {
					t.Errorf("the run reported no change but the file's mtime moved from %v to %v", beforeRun, afterRun)
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

// assertMtimeFollows checks that the run's mtime is no earlier than the
// check's and at most a minute later.
//
// Both values arrive as whatever the diff map holds, which is a whole
// number of Unix seconds, so each is read through asUnixSeconds rather
// than asserted to one Go type: the diff is built for humans and JSON, and
// a test pinned to int64 would break the first time it round trips.
func assertMtimeFollows(t *testing.T, checked, ran any) {
	t.Helper()
	c, okC := asUnixSeconds(checked)
	r, okR := asUnixSeconds(ran)
	if !okC || !okR {
		t.Errorf("mtime is not a whole number of seconds: check %v (%T), run %v (%T)", checked, checked, ran, ran)
		return
	}
	if r < c {
		t.Errorf("the run left mtime %d, earlier than the check's %d, though its file was written second", r, c)
	}
	if r-c > 60 {
		t.Errorf("the run left mtime %d, %d seconds after the check's %d; two local writes a moment apart cannot be that far apart", r, r-c, c)
	}
}

// asUnixSeconds reads a diff field as a whole number of seconds.
func asUnixSeconds(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), n == float64(int64(n))
	default:
		return 0, false
	}
}

// lineMtime reads a file's modification time from the disk it is on.
//
// It exists so a test can observe whether a run touched a file without
// trusting the run's own record of that, which for a no-op is the prior
// state repeated rather than anything read afterwards.
func lineMtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

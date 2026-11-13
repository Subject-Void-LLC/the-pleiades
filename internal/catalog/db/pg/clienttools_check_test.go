// The control for the harness's client check: requireTools acts on
// UsePostgresClientTools' answer and on the versions PATH really finds,
// and each stop is the one clienttools_test.go names.
package pg_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// stopRecorder is a testing.TB that records the stop a helper chose
// instead of making it, so the failing case can be checked without
// failing this test. Everything else is the real test's.
type stopRecorder struct {
	*testing.T
	failed, skipped string
}

// Helper implements testing.TB.
func (r *stopRecorder) Helper() {}

// Fatal records a failure.
func (r *stopRecorder) Fatal(args ...any) { r.failed = fmt.Sprint(args...) }

// Fatalf records a failure.
func (r *stopRecorder) Fatalf(format string, args ...any) { r.failed = fmt.Sprintf(format, args...) }

// Skipf records a skip.
func (r *stopRecorder) Skipf(format string, args ...any) { r.skipped = fmt.Sprintf(format, args...) }

// TestRequireTools_ActsOnTheAnswer: a client at the fixture's version goes
// on, pinned or not; a pinned directory whose programs are another version
// or do not answer is a broken harness, which fails; an unpinned client the
// fixture cannot take, or none, is a need, which skips naming it, and
// fails in a run that requires it. The versions are read from real
// programs on PATH, as the device's shell finds them, and PATH holds
// nothing else, so a real client elsewhere cannot answer for one left off.
func TestRequireTools_ActsOnTheAnswer(t *testing.T) {
	need.POSIXShell(t)
	testsupport.Require(t, need.Timeout, hostTimeoutTakesKill(), "requireTools asks this machine's timeout first, and it takes no -k")
	// The probe runs timeout and the true it times, so each is linked
	// beside the stand-ins.
	probe := map[string]string{}
	for _, name := range []string{"timeout", "true"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		probe[name] = path
	}
	server, err := strconv.Atoi(testsupport.PostgresMajor())
	if err != nil {
		t.Fatal(err)
	}
	v := func(major int) string { return strconv.Itoa(major) + ".1" }
	for _, tc := range []struct {
		name           string
		pinned         bool
		dump, restore  string // what each program's --version names; "" leaves it off PATH
		broken, reason string // a substring of the failure, or of the skip's reason
	}{
		{name: "the fixture's version, pinned", pinned: true, dump: v(server), restore: v(server)},
		{name: "the fixture's version, unpinned", dump: v(server), restore: v(server)},
		{name: "pinned, yet PATH finds another version", pinned: true, dump: v(server + 1), restore: v(server + 1), broken: "the pg_dump PATH finds"},
		{name: "pinned, yet pg_restore is missing", pinned: true, dump: v(server), broken: "pg_restore did not answer"},
		{name: "a newer one sending transaction_timeout, unpinned", dump: v(transactionTimeoutMajor + 1), restore: v(transactionTimeoutMajor + 1), reason: "sends transaction_timeout"},
		{name: "only pg_restore sends it, unpinned", dump: v(server), restore: v(transactionTimeoutMajor), reason: "the pg_restore on PATH"},
		{name: "an older one, unpinned", dump: v(server - 1), restore: v(server - 1), reason: "older than the PostgreSQL " + strconv.Itoa(server) + " fixture"},
		{name: "none, unpinned", reason: "pg_dump: not on PATH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.reason, "transaction_timeout") && server >= transactionTimeoutMajor {
				t.Skipf("the fixture is %d, which has transaction_timeout", server)
			}
			dir := t.TempDir()
			for name, path := range probe {
				if err := os.Symlink(path, filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			for program, version := range map[string]string{"pg_dump": tc.dump, "pg_restore": tc.restore} {
				if version == "" {
					continue
				}
				script := fmt.Sprintf("#!/bin/sh\necho '%s (PostgreSQL) %s'\n", program, version)
				if err := os.WriteFile(filepath.Join(dir, program), []byte(script), 0o700); err != nil { // #nosec G306 -- a test's own stand-in, run by its owner
					t.Fatal(err)
				}
			}
			// PATH holds only the stand-ins. Pinned, UsePostgresClientTools
			// puts its directory first on PATH and says so; the stand-in for
			// it does the same with the stand-ins' directory.
			t.Setenv("PATH", dir)
			pin := func(tb testing.TB) bool {
				if tc.pinned {
					tb.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				}
				return tc.pinned
			}
			for _, require := range []string{"", need.PostgresClient} {
				t.Setenv(need.Env, require)
				r := &stopRecorder{T: t}
				requireToolsFrom(r, pin)
				switch {
				case tc.broken != "":
					if !strings.Contains(r.failed, tc.broken) || r.skipped != "" {
						t.Errorf("%s=%q: failed %q, skipped %q; want the harness failed with %q", need.Env, require, r.failed, r.skipped, tc.broken)
					}
				case tc.reason != "" && require == "":
					if !strings.HasPrefix(r.skipped, "needs "+need.PostgresClient+": ") || !strings.Contains(r.skipped, tc.reason) || r.failed != "" {
						t.Errorf("failed %q, skipped %q; want a skip naming the need and %q", r.failed, r.skipped, tc.reason)
					}
				case tc.reason != "":
					if !strings.Contains(r.failed, "requires "+need.PostgresClient) || !strings.Contains(r.failed, tc.reason) || r.skipped != "" {
						t.Errorf("%s=%q: failed %q, skipped %q; want a failure naming %q", need.Env, require, r.failed, r.skipped, tc.reason)
					}
				default:
					if r.failed != "" || r.skipped != "" {
						t.Errorf("failed %q, skipped %q; want the test to go on", r.failed, r.skipped)
					}
				}
			}
		})
	}
}

// TestRequireTools_ReadsThePathThatRuns: majorOnPath asks the program PATH
// finds first, not any other one installed, and refuses an answer naming
// no version rather than guessing one.
func TestRequireTools_ReadsThePathThatRuns(t *testing.T) {
	need.POSIXShell(t)
	first, second := t.TempDir(), t.TempDir()
	for dir, answer := range map[string]string{first: "pg_dump (PostgreSQL) 16.4", second: "pg_dump (PostgreSQL) 15.19"} {
		if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte("#!/bin/sh\necho '"+answer+"'\n"), 0o700); err != nil { // #nosec G306 -- a test's own stand-in, run by its owner
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	if major, path, err := majorOnPath("pg_dump"); err != nil || major != 16 || path != filepath.Join(first, "pg_dump") {
		t.Errorf("majorOnPath = %d, %s, %v; want 16 from %s", major, path, err, first)
	}
	if err := os.WriteFile(filepath.Join(first, "pg_dump"), []byte("#!/bin/sh\necho 'pg_dump 16'\n"), 0o700); err != nil { // #nosec G306 -- a test's own stand-in, run by its owner
		t.Fatal(err)
	}
	if _, _, err := majorOnPath("pg_dump"); err == nil || !strings.Contains(err.Error(), "names no PostgreSQL version") {
		t.Errorf("an answer naming no version = %v", err)
	}
}

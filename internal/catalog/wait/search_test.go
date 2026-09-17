package wait_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// searchLog is the shape of the file this method is really pointed at: a
// service's log, where the line that matters is never the first one.
//
// It exists so several tests share one body of text whose interesting
// line is in the middle, which is what makes a multiline assertion mean
// something.
const searchLog = "starting up\nloading configuration\n"

// TestSearch_Registered proves the method registered itself as
// implemented and answers the reversibility question the way a reader
// must.
func TestSearch_Registered(t *testing.T) {
	d, ok := collection.Lookup("wait.search")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "wait.search")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible is true, but this method reads a file and emits no inverse")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says why there is nothing to undo")
	}
}

// TestSearch_RefusesTheSameBadParametersWaitPathDoes proves this method
// checks the shared parameters too, rather than only its own pattern.
//
// The two methods read them through one helper, so what this pins is
// that the helper is really called here: a copy that had drifted, or a
// call that had been left out, would let a task with no path reach the
// dial and fail against the device instead of against the runbook.
func TestSearch_RefusesTheSameBadParametersWaitPathDoes(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "no path", params: map[string]any{"search_regex": "ready"}, want: "path is required"},
		{name: "zero sleep", params: map[string]any{"path": "/tmp/x", "search_regex": "ready", "sleep": 0}, want: "sleep must be more than 0 seconds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wait.Search(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestSearch_RefusesAMissingRegex proves the pattern is required, and
// that the refusal points at the method that wants no pattern.
//
// "search_regex is required" on its own reads as a limitation. Naming
// wait.path turns it into an answer: the author wanted the other method.
func TestSearch_RefusesAMissingRegex(t *testing.T) {
	for _, params := range []map[string]any{
		{"path": "/tmp/x"},
		{"path": "/tmp/x", "search_regex": ""},
		{"path": "/tmp/x", "search_regex": nil},
	} {
		_, err := wait.Search(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "search_regex is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
		if !strings.Contains(err.Error(), "wait.path") {
			t.Errorf("params %v: error = %q, want it to name the method that needs no pattern", params, err)
		}
	}
}

// TestSearch_RefusesARegexThatIsNotText covers a pattern YAML turned
// into something else, which a reader treating a non-string as absent
// would report as a missing parameter the author plainly wrote.
func TestSearch_RefusesARegexThatIsNotText(t *testing.T) {
	_, err := wait.Search(context.Background(), nil, nil, map[string]any{"path": "/tmp/x", "search_regex": 42})
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "search_regex is int, not text") {
		t.Errorf("error = %q, want it to name the parameter and its type", err)
	}
}

// TestSearch_RefusesAPatternItCannotCompile proves a bad pattern is
// caught when the task is read rather than at the end of a timeout.
//
// The lookahead is the case worth having. It is valid in Python, which
// is what wait_for compiles with, so a converted playbook can carry one,
// and left to run time it would look like a pattern that simply never
// matched. The message has to say RE2, or the author reads a correct
// pattern and concludes the file is wrong.
func TestSearch_RefusesAPatternItCannotCompile(t *testing.T) {
	for _, pattern := range []string{"(unclosed", "a(?=b)", "*nope", `a\1`} {
		_, err := wait.Search(context.Background(), nil, nil, map[string]any{
			"path":         "/tmp/x",
			"search_regex": pattern,
		})
		if err == nil {
			t.Fatalf("pattern %q: expected a refusal, got nil", pattern)
		}
		if !strings.Contains(err.Error(), "cannot be compiled") {
			t.Errorf("pattern %q: error = %q, want it to say the pattern is the problem", pattern, err)
		}
		if !strings.Contains(err.Error(), "RE2") {
			t.Errorf("pattern %q: error = %q, want it to name the flavor that accepts it", pattern, err)
		}
	}
}

// TestSearch_WaitsForAPatternThatAppears is the case the method exists
// for, and it proves three things at once.
//
// The line is appended 400ms in, so a method that read once could not
// pass. It is appended at the END of a file that already has two lines,
// so a pattern anchored with ^ only matches if the pattern is compiled
// in multiline mode the way wait_for compiles it. And the capture is
// read back from the stats, which is how a later task uses this.
func TestSearch_WaitsForAPatternThatAppears(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitExistingFile(t, searchLog)

	const appearsAfter = 400 * time.Millisecond
	waitAfter(t, appearsAfter, func() {
		waitReplace(t, path, searchLog+"listening on port 8080\n")
	})

	start := time.Now()
	result, err := wait.Search(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"search_regex":                  "^listening on port (?P<port>[0-9]+)$",
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if waited < appearsAfter {
		t.Errorf("the call returned after %s, before the line was written at %s: it cannot have waited", waited, appearsAfter)
	}
	if result.Changed {
		t.Error("a wait reported a change, but it sends no command that alters anything")
	}

	if got := rc.stats["search_regex"]; got != "^listening on port (?P<port>[0-9]+)$" {
		t.Errorf("search_regex stat = %v, want the pattern exactly as the task wrote it", got)
	}
	if got := rc.stats["match_groups"]; !reflect.DeepEqual(got, []string{"8080"}) {
		t.Errorf("match_groups = %#v, want just the captured value: the whole match does not belong in it", got)
	}
	if got := rc.stats["match_groupdict"]; !reflect.DeepEqual(got, map[string]string{"port": "8080"}) {
		t.Errorf("match_groupdict = %#v, want the named group keyed by its name", got)
	}

	before := waitDiffHalf(t, rc, "before")
	after := waitDiffHalf(t, rc, "after")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("diff before %v and after %v differ, but this method changes nothing", before, after)
	}
	if got := before["matched"]; got != true {
		t.Errorf("diff matched = %v, want true", got)
	}
	if got := before["exists"]; got != true {
		t.Errorf("diff exists = %v, want true", got)
	}
	if got := before["path"]; got != path {
		t.Errorf("diff path = %v, want %q", got, path)
	}
	// A run that changed nothing must emit no inverse: an undo of it
	// would perform work the forward run never did.
	if _, recorded := rc.stats["inverse"]; recorded {
		t.Error("an inverse was recorded for a run that changed nothing")
	}
}

// TestSearch_ReportsTheCaptureGroups pins what a later task reads out of
// a match, across the shapes a pattern can have.
//
// The unnamed cases are what stop an empty key appearing in the
// dictionary: a group with no name has an empty one here, and storing it
// under "" would put a value in there that no runbook could ask for.
func TestSearch_ReportsTheCaptureGroups(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		groups  []string
		named   map[string]string
	}{
		{
			name:    "no groups",
			pattern: "ready",
			groups:  []string{},
			named:   map[string]string{},
		},
		{
			name:    "one unnamed group",
			pattern: "ready (v[0-9]+)",
			groups:  []string{"v2"},
			named:   map[string]string{},
		},
		{
			name:    "named and unnamed together",
			pattern: "ready (v[0-9]+) build (?P<build>[a-z]+)",
			groups:  []string{"v2", "beta"},
			named:   map[string]string{"build": "beta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startWaitServer(t)
			rc := newWaitContext(server)

			_, err := wait.Search(context.Background(), rc, newWaitTarget(server), map[string]any{
				"path":                          waitExistingFile(t, searchLog+"ready v2 build beta\n"),
				"search_regex":                  tt.pattern,
				"timeout":                       20,
				"insecure_skip_host_key_verify": true,
			})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := rc.stats["match_groups"]; !reflect.DeepEqual(got, tt.groups) {
				t.Errorf("match_groups = %#v, want %#v", got, tt.groups)
			}
			if got := rc.stats["match_groupdict"]; !reflect.DeepEqual(got, tt.named) {
				t.Errorf("match_groupdict = %#v, want %#v", got, tt.named)
			}
		})
	}
}

// TestSearch_WaitsForAPatternToDisappear covers state: absent, the shape
// a task takes when it waits for an error line to be rotated away.
//
// No capture groups are reported, because nothing matched at the end.
// Reporting empty ones would look like a match that captured nothing,
// which is a different thing entirely.
func TestSearch_WaitsForAPatternToDisappear(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitExistingFile(t, searchLog+"FATAL cannot bind\n")

	const clearedAfter = 400 * time.Millisecond
	waitAfter(t, clearedAfter, func() { waitReplace(t, path, searchLog) })

	start := time.Now()
	_, err := wait.Search(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"search_regex":                  "FATAL",
		"state":                         "absent",
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if waited < clearedAfter {
		t.Errorf("the call returned after %s, before the line was removed at %s", waited, clearedAfter)
	}
	if got := waitDiffHalf(t, rc, "before")["matched"]; got != false {
		t.Errorf("diff matched = %v, want false", got)
	}
	if _, recorded := rc.stats["match_groups"]; recorded {
		t.Error("match_groups was recorded for a run that ended with nothing matching")
	}
	if _, recorded := rc.stats["match_groupdict"]; recorded {
		t.Error("match_groupdict was recorded for a run that ended with nothing matching")
	}
	// The file still holds what the test left in it: this method reads
	// and never writes.
	contents, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s back: %v", path, readErr)
	}
	if string(contents) != searchLog {
		t.Errorf("the file holds %q, want %q untouched", contents, searchLog)
	}
}

// TestSearch_AMissingFileNeverMatches proves absence is an answer rather
// than an error, in both directions.
//
// A file that is not there contains no match, so a task waiting for a
// pattern keeps waiting (which is what watching a log that has not been
// created yet needs) and a task waiting for one to go away is satisfied
// at once (which is correct, since a deleted log cannot still say it).
func TestSearch_AMissingFileNeverMatches(t *testing.T) {
	t.Run("absent is satisfied at once", func(t *testing.T) {
		server := startWaitServer(t)
		rc := newWaitContext(server)

		_, err := wait.Search(context.Background(), rc, newWaitTarget(server), map[string]any{
			"path":                          waitMissingPath(t),
			"search_regex":                  "FATAL",
			"state":                         "absent",
			"timeout":                       20,
			"sleep":                         10,
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if got := waitDiffHalf(t, rc, "before")["exists"]; got != false {
			t.Errorf("diff exists = %v, want false", got)
		}
		if got := waitDiffHalf(t, rc, "before")["matched"]; got != false {
			t.Errorf("diff matched = %v, want false", got)
		}
	})

	t.Run("present keeps waiting", func(t *testing.T) {
		server := startWaitServer(t)

		_, err := wait.Search(context.Background(), newWaitContext(server), newWaitTarget(server), map[string]any{
			"path":                          waitMissingPath(t),
			"search_regex":                  "ready",
			"timeout":                       1,
			"insecure_skip_host_key_verify": true,
		})
		if err == nil {
			t.Fatal("a pattern that could never match reported success")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Errorf("error = %q, want a timeout", err)
		}
	})
}

// TestSearch_ADirectoryNeverMatches pins the documented consequence of
// reading with cat: only a regular file has contents to search.
//
// It is worth a test rather than only a sentence, because the behavior
// is surprising: the path exists, and the task still times out. A reader
// who has been told this happens can find the mistake; one who has not
// goes looking for the pattern.
func TestSearch_ADirectoryNeverMatches(t *testing.T) {
	server := startWaitServer(t)

	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}

	_, err := wait.Search(context.Background(), newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          dir,
		"search_regex":                  "ready",
		"timeout":                       1,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a directory was searched successfully, which cat cannot do")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a timeout rather than a read failure", err)
	}
}

// TestSearch_TimesOut proves a wait that gives up is an error naming
// both the pattern and the file.
//
// Both halves matter: the two mistakes behind a timeout here are a file
// nobody is writing and a pattern that does not say what its author
// thought, and a message with only one of them cannot tell them apart.
func TestSearch_TimesOut(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitExistingFile(t, searchLog)

	_, err := wait.Search(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"search_regex":                  "never appears",
		"timeout":                       1,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a pattern that never matched reported success")
	}
	if !strings.Contains(err.Error(), `wait.search: timed out after 1s waiting for "never appears" to match `+path) {
		t.Errorf("error = %q, want it to name the timeout, the pattern and the file", err)
	}
	if len(rc.stats) != 0 {
		t.Errorf("a timed-out run recorded %v, want nothing", rc.stats)
	}
}

// TestSearch_TimesOutWaitingForAPatternToGoAway is the same failure from
// the other state, and exists because the two conditions are two
// sentences an operator reads differently.
func TestSearch_TimesOutWaitingForAPatternToGoAway(t *testing.T) {
	server := startWaitServer(t)
	path := waitExistingFile(t, searchLog+"FATAL cannot bind\n")

	_, err := wait.Search(context.Background(), newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          path,
		"search_regex":                  "FATAL",
		"state":                         "stopped",
		"timeout":                       1,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a pattern that never went away reported success")
	}
	if !strings.Contains(err.Error(), `waiting for "FATAL" to stop matching `+path) {
		t.Errorf("error = %q, want it to say the pattern was supposed to go away", err)
	}
}

// TestSearch_RefusesAnUnreachableDevice covers the connect failure path,
// and proves the pattern is compiled before the dial: a device that
// cannot be reached must still be reported as the device's problem.
func TestSearch_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := wait.Search(context.Background(), newWaitContext(waitServer{}), newWaitUnreachable(), map[string]any{
		"path":         "/tmp/x",
		"search_regex": "ready",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestSearch_ProbeFailureIsReported covers the branch where the
// connection authenticates and then cannot carry the read.
//
// A read that FAILED is never mistaken for a file that does not match,
// which would spend the whole timeout on a device that already said no
// and then blame the pattern.
func TestSearch_ProbeFailureIsReported(t *testing.T) {
	server := startWaitServerWithSessionBudget(t, 0)
	path := waitExistingFile(t, searchLog+"ready\n")

	start := time.Now()
	_, err := wait.Search(context.Background(), newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          path,
		"search_regex":                  "ready",
		"timeout":                       60,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err == nil {
		t.Fatal("a failed read was reported as success")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
	if !strings.Contains(err.Error(), "read "+path) {
		t.Errorf("error = %q, want it to name the read that failed", err)
	}
	if waited > 30*time.Second {
		t.Errorf("the call took %s, so a failing read was retried rather than reported", waited)
	}
}

// TestSearch_StatRecordFailureIsReported covers every recording call
// site this method has, the shared one included, one test each.
//
// They need separating because each is reached only when the ones
// before it succeeded, so a single context that failed on everything
// would prove only the first. The path case is the shared recorder's,
// and it is here as well as in wait.path's tests because each method
// wraps that helper's error itself.
func TestSearch_StatRecordFailureIsReported(t *testing.T) {
	for _, key := range []string{"path", "search_regex", "match_groups", "match_groupdict"} {
		t.Run(key, func(t *testing.T) {
			server := startWaitServer(t)
			rc := newWaitContext(server)
			rc.failOn = key

			_, err := wait.Search(context.Background(), rc, newWaitTarget(server), map[string]any{
				"path":                          waitExistingFile(t, searchLog+"ready v2\n"),
				"search_regex":                  "ready (v[0-9]+)",
				"timeout":                       20,
				"insecure_skip_host_key_verify": true,
			})
			if err == nil {
				t.Fatalf("a failure to record %q was swallowed", key)
			}
			if !errors.Is(err, errWaitStat) {
				t.Errorf("error = %q, want it to carry what recording returned", err)
			}
			if _, recorded := rc.stats["diff"]; !recorded {
				t.Error("the diff was not recorded first, so the call sites ran in the wrong order")
			}
		})
	}
}

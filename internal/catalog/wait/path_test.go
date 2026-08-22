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

// TestPath_Registered proves the method registered itself as implemented
// and answers the reversibility question the way an observer must.
//
// Reversible: false is pinned rather than merely present. Flipping it to
// true would tell a rollback engine to expect an instruction this method
// never emits, and the Notes are what an operator reads to find out why
// there is nothing to undo.
func TestPath_Registered(t *testing.T) {
	d, ok := collection.Lookup("wait.path")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "wait.path")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible is true, but this method changes nothing and emits no inverse")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says why there is nothing to undo")
	}
}

// TestPath_RefusesAMissingPath proves the one required parameter is
// really required, and that the refusal happens before any connection.
//
// A nil device would fail in connect, so reaching the end of this test
// with the expected message is itself the proof that nothing dialed.
func TestPath_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"timeout": 5},
		{"path": ""},
		{"path": nil},
	} {
		_, err := wait.Path(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestPath_RefusesAParameterThatIsNotText covers the mistake YAML makes
// quietly: a path or a state written as something other than text.
//
// The refusal has to name the parameter. Treating a non-string as absent
// the way sdk.StringParam does would answer "path is required" for a
// task that plainly wrote one, and send the author hunting.
func TestPath_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "path", params: map[string]any{"path": 42}},
		{name: "state", params: map[string]any{"path": "/tmp/x", "state": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wait.Path(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.name+" is int, not text") {
				t.Errorf("error = %q, want it to name %q and its type", err, tt.name)
			}
		})
	}
}

// TestPath_RefusesAStateItCannotWaitFor proves the state parameter is
// checked against the values that mean something for a file.
//
// drained is the case that matters: it is a real wait_for state, so a
// converted playbook can carry it, and it asks about a socket's send
// queue. Accepting it silently as "present" would make the task wait for
// the wrong thing and report success.
func TestPath_RefusesAStateItCannotWaitFor(t *testing.T) {
	for _, state := range []string{"drained", "exists", "Present", "gone"} {
		_, err := wait.Path(context.Background(), nil, nil, map[string]any{"path": "/tmp/x", "state": state})
		if err == nil {
			t.Fatalf("state %q: expected a refusal, got nil", state)
		}
		if !strings.Contains(err.Error(), "is not one of present, absent, started or stopped") {
			t.Errorf("state %q: error = %q, want it to list what it accepts", state, err)
		}
	}
}

// TestPath_RefusesTimingItCannotUse covers every way a timing parameter
// can be wrong, including the three Go types one YAML number arrives as.
//
// The type cases are not padding. A runbook's `timeout: 30` decodes to
// an int on the Crawl tier and to a float64 across the Runner's per-task
// subprocess boundary on the Walk tier, and a reader that understood
// only one of them would silently use the default on the other tier.
// Each case below carries a value the message has to echo back, so a
// reader that fell through to the wrong branch fails here.
func TestPath_RefusesTimingItCannotUse(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "text", params: map[string]any{"timeout": "30"}, want: "timeout is string, not a number of seconds"},
		{name: "fraction", params: map[string]any{"timeout": 0.5}, want: "timeout 0.5 is not a whole number of seconds"},
		{name: "negative int", params: map[string]any{"timeout": -7}, want: "timeout -7 is negative"},
		{name: "negative int64", params: map[string]any{"delay": int64(-8)}, want: "delay -8 is negative"},
		{name: "negative float64", params: map[string]any{"sleep": float64(-9)}, want: "sleep -9 is negative"},
		{name: "zero timeout", params: map[string]any{"timeout": 0}, want: "timeout must be more than 0 seconds"},
		{name: "zero sleep", params: map[string]any{"sleep": 0}, want: "sleep must be more than 0 seconds"},
		{name: "delay past the timeout", params: map[string]any{"timeout": 5, "delay": 5}, want: "delay (5s) must be shorter than timeout (5s)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"path": "/tmp/x"}
			for k, v := range tt.params {
				params[k] = v
			}

			_, err := wait.Path(context.Background(), nil, nil, params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestPath_WaitsForAPathThatAppears is the case the whole method exists
// for: the condition is false when the task starts and becomes true
// while it is watching.
//
// The path is proven absent before the call and is created 400ms in, so
// a method that probed once and returned could not pass this. The wall
// clock assertion is what separates "waited" from "got lucky".
func TestPath_WaitsForAPathThatAppears(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitMissingPath(t)

	const appearsAfter = 400 * time.Millisecond
	waitAfter(t, appearsAfter, func() { _ = os.WriteFile(path, []byte("here\n"), 0o600) })

	start := time.Now()
	result, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if waited < appearsAfter {
		t.Errorf("the call returned after %s, before the path was created at %s: it cannot have waited", waited, appearsAfter)
	}
	if result.Changed {
		t.Error("a wait reported a change, but it sends no command that alters anything")
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Errorf("os.Lstat(%s) = %v, want the file the test created to be there", path, statErr)
	}

	if got := rc.stats["path"]; got != path {
		t.Errorf("path stat = %v, want %q", got, path)
	}
	// The diff describes what the last look found, and both halves are
	// the same observation. A journal reading it has to see a real file
	// rather than an empty record.
	before := waitDiffHalf(t, rc, "before")
	after := waitDiffHalf(t, rc, "after")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("diff before %v and after %v differ, but this method changes nothing", before, after)
	}
	if got := before["kind"]; got != "file" {
		t.Errorf("diff kind = %v, want file read back from the device", got)
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

// TestPath_ReturnsAtOnceWhenThePathIsAlreadyThere proves a condition
// that already holds costs no sleep, and that a directory counts as
// existing.
//
// The directory is the point of the second half. Ansible's wait_for asks
// os.path.exists, which says yes for a directory, and a method that
// looked only for a regular file would wait out its whole timeout
// against a path that is plainly there.
func TestPath_ReturnsAtOnceWhenThePathIsAlreadyThere(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)

	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}

	start := time.Now()
	result, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          dir,
		"timeout":                       20,
		"sleep":                         10,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if result.Changed {
		t.Error("a wait reported a change")
	}
	// A sleep of ten seconds is set deliberately: a method that slept
	// before its first look, or that looked again after finding what it
	// wanted, would take at least that long.
	if waited > 5*time.Second {
		t.Errorf("the call took %s against a path that was already there, so it slept when it had no reason to", waited)
	}
	if got := waitDiffHalf(t, rc, "before")["kind"]; got != "directory" {
		t.Errorf("diff kind = %v, want directory: a directory has to count as existing", got)
	}
	if got := waitElapsed(t, rc); got != 0 {
		t.Errorf("elapsed stat = %d, want 0 whole seconds for a condition that already held", got)
	}
}

// TestPath_WaitsForAPathToDisappear covers state: absent, which waits
// for something else to remove the path and never removes it itself.
func TestPath_WaitsForAPathToDisappear(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitExistingFile(t, "lock\n")

	const removedAfter = 400 * time.Millisecond
	waitAfter(t, removedAfter, func() { _ = os.Remove(path) })

	start := time.Now()
	result, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"state":                         "absent",
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if waited < removedAfter {
		t.Errorf("the call returned after %s, before the path was removed at %s", waited, removedAfter)
	}
	if result.Changed {
		t.Error("a wait reported a change")
	}
	if got := waitDiffHalf(t, rc, "before")["exists"]; got != false {
		t.Errorf("diff exists = %v, want false: the last look found the path gone", got)
	}
	if got := waitDiffHalf(t, rc, "before")["kind"]; got != "absent" {
		t.Errorf("diff kind = %v, want absent", got)
	}
}

// TestPath_AcceptsWaitForsOwnStateSpellings proves started and stopped
// are honored, which is what lets a wait_for task land unchanged.
//
// Each case pairs a spelling with a device state that satisfies it, so a
// method that mapped one of them to the wrong answer times out here
// instead of passing.
func TestPath_AcceptsWaitForsOwnStateSpellings(t *testing.T) {
	tests := []struct {
		state   string
		present bool
	}{
		{state: "started", present: true},
		{state: "stopped", present: false},
		{state: "present", present: true},
		{state: "absent", present: false},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			server := startWaitServer(t)
			rc := newWaitContext(server)

			path := waitMissingPath(t)
			if tt.present {
				waitWrite(t, path, "here\n")
			}

			_, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
				"path":                          path,
				"state":                         tt.state,
				"timeout":                       10,
				"insecure_skip_host_key_verify": true,
			})
			if err != nil {
				t.Fatalf("Path with state %q: %v", tt.state, err)
			}
			if got := waitDiffHalf(t, rc, "before")["exists"]; got != tt.present {
				t.Errorf("diff exists = %v, want %v", got, tt.present)
			}
		})
	}
}

// TestPath_HonoursTheDelay proves the delay parameter is really waited
// out, using a path that is already there.
//
// Already there is what makes this a proof. The condition holds from the
// first look, so the only thing that can account for the elapsed second
// is the delay, and a method that ignored it would return at once.
func TestPath_HonoursTheDelay(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)

	start := time.Now()
	_, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          waitExistingFile(t, "here\n"),
		"delay":                         int64(1),
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if waited < time.Second {
		t.Errorf("the call returned after %s, want at least the 1s delay it was given", waited)
	}
	// The elapsed stat has to include the delay, which is what wait_for
	// reports too: an operator reading 0 for a task that took a second
	// would not believe either number again.
	if got := waitElapsed(t, rc); got < 1 {
		t.Errorf("elapsed stat = %d, want at least 1 second including the delay", got)
	}
}

// TestPath_HonoursTheSleepInterval proves the interval between looks is
// the one the task asked for rather than the default.
//
// The path appears almost immediately, so the only thing that can hold
// the call open for two seconds is the sleep between the first look and
// the second. With the default of one second this returns in about half
// the time, which is what makes the assertion able to fail.
func TestPath_HonoursTheSleepInterval(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitMissingPath(t)

	waitAfter(t, 100*time.Millisecond, func() { _ = os.WriteFile(path, []byte("here\n"), 0o600) })

	start := time.Now()
	_, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"sleep":                         float64(2),
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if waited < 2*time.Second {
		t.Errorf("the call returned after %s, want at least the 2s sleep between looks", waited)
	}
}

// TestPath_TimesOut proves a wait that gives up is an error, and that
// the error says what was being waited for.
//
// A task that waited for something that never happened has failed, so
// reporting success with a flag for the caller to inspect would let a
// runbook carry on against a device that is not ready. The sleep is
// longer than the timeout on purpose: the last look has to land on the
// deadline rather than well past it.
func TestPath_TimesOut(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	path := waitMissingPath(t)

	start := time.Now()
	_, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          path,
		"timeout":                       1,
		"sleep":                         30,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err == nil {
		t.Fatal("a wait that never saw the path reported success")
	}
	if !strings.Contains(err.Error(), "wait.path: timed out after 1s waiting for "+path+" to exist") {
		t.Errorf("error = %q, want it to name the timeout and what was being waited for", err)
	}
	if waited > 10*time.Second {
		t.Errorf("the call took %s for a 1s timeout: the sleep was not capped at what was left", waited)
	}
	// A failed run records nothing. The engine discards a Result that
	// arrives with an error, so a stat left behind would describe an
	// observation the task never got to complete.
	if len(rc.stats) != 0 {
		t.Errorf("a timed-out run recorded %v, want nothing", rc.stats)
	}
}

// TestPath_TimesOutWaitingForAPathToGoAway is the same failure from the
// other state, and exists because the two conditions are two sentences.
//
// "to exist" against a path that is plainly there would send an operator
// looking at the wrong thing entirely.
func TestPath_TimesOutWaitingForAPathToGoAway(t *testing.T) {
	server := startWaitServer(t)
	path := waitExistingFile(t, "lock\n")

	_, err := wait.Path(context.Background(), newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          path,
		"state":                         "absent",
		"timeout":                       1,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a wait for a path that never went away reported success")
	}
	if !strings.Contains(err.Error(), "waiting for "+path+" to stop existing") {
		t.Errorf("error = %q, want it to say the path was supposed to go away", err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Errorf("os.Lstat(%s) = %v, want the path untouched: this method removes nothing", path, statErr)
	}
}

// TestPath_StopsWaitingWhenTheRunIsCancelled covers the delay, which is
// the longest a wait can sit before it looks at anything.
//
// A run being torn down has to stop now. A plain time.Sleep here would
// hold this task's device lock for the rest of a five minute delay while
// nothing was left to use the answer, so the cancellation has to reach
// the sleep itself rather than only the next probe.
func TestPath_StopsWaitingWhenTheRunIsCancelled(t *testing.T) {
	server := startWaitServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	waitAfter(t, 200*time.Millisecond, cancel)

	start := time.Now()
	_, err := wait.Path(ctx, newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          waitMissingPath(t),
		"delay":                         15,
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %q, want it to carry the cancellation", err)
	}
	if waited > 10*time.Second {
		t.Errorf("the call took %s to notice a cancellation 200ms in, so it slept through it", waited)
	}
}

// TestPath_StopsWaitingBetweenLooksWhenCancelled is the same property at
// the other sleep, the interval between probes, which a run spends
// almost all of its time in.
func TestPath_StopsWaitingBetweenLooksWhenCancelled(t *testing.T) {
	server := startWaitServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	waitAfter(t, 300*time.Millisecond, cancel)

	start := time.Now()
	_, err := wait.Path(ctx, newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          waitMissingPath(t),
		"sleep":                         15,
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %q, want it to carry the cancellation", err)
	}
	if waited > 10*time.Second {
		t.Errorf("the call took %s to notice a cancellation 300ms in, so it slept through it", waited)
	}
}

// TestPath_RefusesAnUnreachableDevice covers the connect failure path,
// which is what a device with no SSH transport produces.
func TestPath_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := wait.Path(context.Background(), newWaitContext(waitServer{}), newWaitUnreachable(), map[string]any{
		"path": "/tmp/x",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestPath_ProbeFailureIsReported covers the branch where the connection
// authenticates and then cannot carry the look.
//
// A session budget of zero is what that looks like from this side, and
// it is a real protocol-level refusal rather than an injected Go error.
// The point is that a look which FAILED is never mistaken for a path
// that is absent: a method that retried it would spend the whole timeout
// on a device that already said no, then blame the file.
func TestPath_ProbeFailureIsReported(t *testing.T) {
	server := startWaitServerWithSessionBudget(t, 0)
	path := waitExistingFile(t, "here\n")

	start := time.Now()
	_, err := wait.Path(context.Background(), newWaitContext(server), newWaitTarget(server), map[string]any{
		"path":                          path,
		"timeout":                       60,
		"insecure_skip_host_key_verify": true,
	})
	waited := time.Since(start)
	if err == nil {
		t.Fatal("a failed look was reported as success")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
	if !strings.Contains(err.Error(), "stat "+path) {
		t.Errorf("error = %q, want it to name the read that failed", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a transport failure not to be reported as a timeout", err)
	}
	if waited > 30*time.Second {
		t.Errorf("the call took %s, so a failing probe was retried rather than reported", waited)
	}
}

// TestPath_DiffRecordFailureIsReported covers the branch where the wait
// finished and recording the observation did not.
//
// Swallowing it would leave a journal unable to tell this task's
// observation from a task that never ran, which is the one thing
// recording a diff for an unchanged run exists to prevent.
func TestPath_DiffRecordFailureIsReported(t *testing.T) {
	server := startWaitServer(t)
	rc := newWaitContext(server)
	rc.failOn = "diff"

	_, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
		"path":                          waitExistingFile(t, "here\n"),
		"timeout":                       20,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
	if !errors.Is(err, errWaitStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestPath_StatRecordFailureIsReported covers the same swallowing
// question at the two later call sites, which the diff test above can
// never reach because it fails before them.
func TestPath_StatRecordFailureIsReported(t *testing.T) {
	for _, key := range []string{"path", "elapsed"} {
		t.Run(key, func(t *testing.T) {
			server := startWaitServer(t)
			rc := newWaitContext(server)
			rc.failOn = key

			_, err := wait.Path(context.Background(), rc, newWaitTarget(server), map[string]any{
				"path":                          waitExistingFile(t, "here\n"),
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

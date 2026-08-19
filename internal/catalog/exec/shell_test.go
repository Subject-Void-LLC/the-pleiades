package exec_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/exec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// These tests deliberately do NOT re-prove what exec.command already
// proves. The two methods share their connection, their creates and
// removes guards, their chdir handling, their stats and their error
// rules, because they share the code: command_test.go covers all of it
// against the same real-shell SSH harness, and a second copy here would
// double the runtime while testing the same lines twice.
//
// What is tested here is the difference, which is entirely about
// quoting: that a shell really does interpret the line, that the line
// reaches exactly one shell rather than being parsed twice, and that
// choosing the shell works. Plus the two guards that share code but have
// their own call site in this method, since a copied function can be
// wired up wrongly even when the function it calls is correct.

// TestShell_Registered proves the method registered itself as
// implemented, with an inverse declared.
func TestShell_Registered(t *testing.T) {
	d, ok := collection.Lookup("exec.shell")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "exec.shell")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	// A shell command's effect cannot be derived, so the honest answer is
	// that it cannot be undone. Registration already refuses a false with
	// no reason; this pins the answer itself, since silently becoming
	// reversible later would tell a rollback engine it may act on a
	// recording this method never makes.
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = true, want false: a shell command's effect cannot be reconstructed")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says why this cannot be undone")
	}
}

// TestShell_RefusesAnEmptyCommand proves the one required parameter is
// really required, and that the refusal happens before any connection.
//
// A nil device would panic in connect, so reaching the end of this test
// is itself the proof that nothing tried to dial.
func TestShell_RefusesAnEmptyCommand(t *testing.T) {
	for _, params := range []map[string]any{nil, {"cmd": ""}, {"chdir": "/tmp"}} {
		_, err := exec.Shell(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "cmd is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestShell_TheShellInterpretsTheLine is the whole point of this method,
// and it is exactly the assertion exec.command's equivalent test inverts.
//
// The same line handed to exec.command comes back with its metacharacters
// intact as text, because that method quotes every word. Here the pipe
// must actually pipe, the redirect must actually redirect, and the
// variable must actually expand. Asserting the OUTPUT rather than the
// sent command line is what makes this real: a method that merely stopped
// quoting would pass a test that only checked the string it sent.
func TestShell_TheShellInterpretsTheLine(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	result, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "printf 'a\\nb\\nc\\n' | wc -l | tr -d ' '",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if !result.Changed {
		t.Error("a command that ran reported no change")
	}
	// 3 only if the pipeline really ran. Unquoted-but-unpiped would print
	// the whole line; quoted would print the literal text.
	if got := rc.stats["stdout"]; got != "3" {
		t.Errorf("stdout = %q, want %q: the pipeline did not run as a pipeline", got, "3")
	}
}

// TestShell_ExpandsAndRedirects proves the other two things a caller
// reaches for this method to get, in one round trip.
func TestShell_ExpandsAndRedirects(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)
	dir := t.TempDir()

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "echo \"$((6*7))\" > " + dir + "/answer && cat " + dir + "/answer",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := rc.stats["stdout"]; got != "42" {
		t.Errorf("stdout = %q, want %q: arithmetic expansion or the redirect did not happen", got, "42")
	}
}

// TestShell_TheLineReachesExactlyOneShell is the quoting assertion that a
// naive implementation gets wrong, and it is the reason this method
// quotes at all.
//
// SSH hands its exec request to a login shell on the far side, and this
// method then names a shell of its own. If the line were concatenated
// unquoted, the login shell would parse it FIRST, and the inner shell
// would receive whatever survived: a single-quoted string would be
// stripped of its quotes, a semicolon would split the invocation, and
// "$0" would be expanded by the wrong shell.
//
// Two shells parsing one line is the defect. Here the line is a single
// quoted argument to the inner shell, so a literal that only survives one
// round of parsing comes back intact.
func TestShell_TheLineReachesExactlyOneShell(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	// Single quotes inside the line. Parsed once, they are quoting and
	// echo prints the contents. Parsed twice, the outer shell consumes
	// them and the inner shell re-splits on the semicolon, so the marker
	// never appears whole.
	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "echo 'one; two' && echo done",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	got, _ := rc.stats["stdout"].(string)
	if !strings.Contains(got, "one; two") {
		t.Errorf("stdout = %q, want it to contain %q: the line was parsed by more than one shell", got, "one; two")
	}
	if !strings.Contains(got, "done") {
		t.Errorf("stdout = %q, want the second command to have run too", got)
	}
}

// TestShell_HonorsTheExecutableParameter proves the task can choose the
// shell, by asking the shell to identify itself.
//
// $0 is what a shell calls itself, and it is set by the shell that ran
// the command rather than by the caller, so it cannot be faked by the
// command line.
func TestShell_HonorsTheExecutableParameter(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	// /bin/bash deliberately, NOT /bin/sh. /bin/sh is this method's own
	// default, so asserting it would pass just as happily against an
	// implementation that ignored the parameter entirely.
	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "echo \"$0\"",
		"executable":                    "/bin/bash",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := rc.stats["stdout"]; got != "/bin/bash" {
		t.Errorf("stdout = %q, want the shell named by the task rather than the default", got)
	}
}

// TestShell_UsesTheDevicesShell proves the device's own declared shell is
// used when the task names none, which is what makes the
// ShellExecCapable requirement on this manifest mean something.
func TestShell_UsesTheDevicesShell(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	// A device declaring a shell, through the same accessor a real
	// linux.Server reads its "shell" property with.
	// Again not /bin/sh, so this distinguishes "read the device" from
	// "fell through to the default".
	device := newShellDevice(server, "/bin/bash")

	_, err := exec.Shell(context.Background(), rc, device, map[string]any{
		"cmd":                           "echo \"$0\"",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := rc.stats["stdout"]; got != "/bin/bash" {
		t.Errorf("stdout = %q, want the device's declared shell rather than the default", got)
	}
}

// TestShell_RunsWithoutAShellAccessor proves a device that cannot answer
// still works, on the default.
//
// The Runner's own device adapter is exactly this shape: it carries a
// declared capability list with no accessors behind it, so a hard type
// assertion here would refuse a dispatch at the far end of a network hop
// with a message about a Go interface.
func TestShell_RunsWithoutAShellAccessor(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Shell(context.Background(), rc, newSSHOnlyDevice(server), map[string]any{
		"cmd":                           "echo \"$0\"",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := rc.stats["stdout"]; got != "/bin/sh" {
		t.Errorf("stdout = %q, want the /bin/sh default", got)
	}
}

// TestShell_NonZeroExitIsAFailure proves a failing pipeline fails the
// task, and reports the last command's status the way a shell does.
func TestShell_NonZeroExitIsAFailure(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "echo why-it-failed >&2; exit 3",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("expected a non-zero exit to fail the task")
	}
	if !strings.Contains(err.Error(), "exited 3") {
		t.Errorf("error = %q, want it to carry the exit status", err)
	}
	if !strings.Contains(err.Error(), "why-it-failed") {
		t.Errorf("error = %q, want it to carry what the command said", err)
	}
	if got := rc.stats["rc"]; got != 3 {
		t.Errorf("rc = %v, want 3 recorded before the error", got)
	}
}

// TestShell_IsIdempotentWithCreates proves this method's own call site
// for the shared guard is wired up, and reports no change on the second
// run without opening a session for the command at all.
//
// The guard's own behavior is command_test.go's to prove. What this
// covers is that Shell calls it, resolves it from the same chdir, and
// returns before running anything, which is a per-method wiring question
// that a shared helper cannot answer for itself.
func TestShell_IsIdempotentWithCreates(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)
	marker := t.TempDir() + "/marker"

	params := map[string]any{
		"cmd":                           "echo made > " + marker,
		"creates":                       marker,
		"insecure_skip_host_key_verify": true,
	}

	first, err := exec.Shell(context.Background(), rc, newDevice(server, ""), params)
	if err != nil {
		t.Fatalf("first Shell: %v", err)
	}
	if !first.Changed {
		t.Error("the first run reported no change")
	}

	second, err := exec.Shell(context.Background(), rc, newDevice(server, ""), params)
	if err != nil {
		t.Fatalf("second Shell: %v", err)
	}
	if second.Changed {
		t.Error("the second run reported a change, so creates did not short-circuit it")
	}
	if got := rc.stats["skipped"]; got != true {
		t.Errorf("skipped = %v, want true", got)
	}
	if got := rc.stats["cmd"]; got != "" {
		t.Errorf("cmd = %q, want empty: nothing should have been sent", got)
	}
}

// TestShell_PipesStdin proves the stdin parameter reaches the shell's own
// standard input rather than the outer login shell's.
func TestShell_PipesStdin(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "tr a-z A-Z",
		"stdin":                         "quiet\n",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := rc.stats["stdout"]; got != "QUIET" {
		t.Errorf("stdout = %q, want %q", got, "QUIET")
	}
}

// The remaining tests cover this method's own error paths. They exist
// because each is a separate call site: the helpers they exercise are
// shared with exec.command and proven there, but "Shell propagates what
// the helper returned" is a wiring question that only a test of Shell can
// answer, and a swallowed error here would turn a failed task into a
// successful one.

// TestShell_RefusesAnUnreachableDevice covers the connect failure path.
func TestShell_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := exec.Shell(context.Background(), newStubContext(testSSHServer{}), newUnreachableDevice(), map[string]any{
		"cmd": "true",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestShell_UnenterableChdirIsReported covers the guard's error path: a
// chdir that cannot be entered must fail the task rather than read as
// "the creates path is absent", which would run the command somewhere
// else entirely.
func TestShell_UnenterableChdirIsReported(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "true",
		"chdir":                         filepath.Join(t.TempDir(), "no-such-directory"),
		"creates":                       "marker",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("an unenterable chdir was treated as an absent creates path")
	}
	if !strings.Contains(err.Error(), "cannot enter directory") {
		t.Errorf("error = %q, want it to name the directory it could not enter", err)
	}
}

// TestShell_StatFailureIsReported covers the branch where the command
// succeeded and recording its answer did not.
func TestShell_StatFailureIsReported(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)
	rc.statErr = errStat

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "true",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the result was swallowed")
	}
}

// TestShell_SkipStatFailureIsReported covers the same branch on the skip
// path, which writes a different set of stats.
func TestShell_SkipStatFailureIsReported(t *testing.T) {
	server := startShellSSHServer(t)
	marker := filepath.Join(t.TempDir(), "already-there")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", marker, err)
	}

	rc := newStubContext(server)
	rc.statErr = errStat

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "touch " + marker,
		"creates":                       marker,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the skip was swallowed")
	}
}

// TestShell_ConnectionFailureDuringTheTaskIsReported covers the branch
// where the connection survives long enough to authenticate and then
// cannot open the session the command needs.
//
// A session budget of zero is what that looks like from the client's
// side, and it is the shape a real device under session pressure
// produces.
func TestShell_ConnectionFailureDuringTheTaskIsReported(t *testing.T) {
	server := startShellSSHServerWithSessionBudget(t, 0)
	rc := newStubContext(server)

	_, err := exec.Shell(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "true",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a connection failure partway through the task was reported as success")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
}

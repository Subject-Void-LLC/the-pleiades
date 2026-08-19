package exec_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/exec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestCommand_Registered proves "exec.command" registered itself through
// this package's init with a manifest that claims what it can back up.
//
// Status and Invoke are checked together on purpose. Either one alone is
// a claim the other has to honor, and the pairing is what the dispatcher
// reads: a StatusImplemented manifest with no Invoke is refused at
// registration, and a StatusDeclared one is refused at dispatch no
// matter how complete the body behind it is.
func TestCommand_Registered(t *testing.T) {
	d, ok := collection.Lookup("exec.command")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "exec.command")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Descriptor.Invoke is nil, so the dispatcher has nothing to call")
	}
	if len(d.Manifest.RequiredCapabilities) != 1 || d.Manifest.RequiredCapabilities[0] != capability.NameCommandExec {
		t.Errorf("RequiredCapabilities = %v, want exactly [%s]", d.Manifest.RequiredCapabilities, capability.NameCommandExec)
	}
}

// TestCommand_RefusesABadInvocationBeforeConnecting proves every
// parameter mistake is caught before a single packet leaves the process.
//
// The target is deliberately nil, so a case that started connecting
// would fail with a different message than the one asserted. That is the
// assertion: resolving what to run happens first, so a typo costs an
// error rather than a connection to a device.
func TestCommand_RefusesABadInvocationBeforeConnecting(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "neither cmd nor argv",
			params: nil,
			want:   "set cmd or argv",
		},
		{
			name:   "both cmd and argv",
			params: map[string]any{"cmd": "uname", "argv": []any{"uname"}},
			want:   "not both",
		},
		{
			name:   "argv is empty",
			params: map[string]any{"argv": []any{}},
			want:   "argv is empty",
		},
		{
			name:   "argv is not a list",
			params: map[string]any{"argv": "uname -a"},
			want:   "argv must be a list of strings",
		},
		{
			name:   "argv holds a number",
			params: map[string]any{"argv": []any{"nc", "-l", 8080}},
			want:   "argv[2] is int, not a string",
		},
		{
			name:   "cmd is only whitespace",
			params: map[string]any{"cmd": "   "},
			want:   "cmd is empty",
		},
		{
			name:   "cmd has an unterminated quote",
			params: map[string]any{"cmd": `echo 'oops`},
			want:   "unterminated single quote",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := exec.Command(context.Background(), nil, nil, tc.params)
			if err == nil {
				t.Fatalf("expected a refusal mentioning %q, got %+v", tc.want, result)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
			if result.Changed {
				t.Error("a refused invocation reported changed")
			}
		})
	}
}

// TestCommand_RefusesAnUnreachableDevice proves a device this method
// cannot reach is named plainly rather than producing a dial to an empty
// address.
func TestCommand_RefusesAnUnreachableDevice(t *testing.T) {
	tests := []struct {
		name   string
		device inventory.InventoryItem
		want   string
	}{
		{name: "no device at all", device: nil, want: "no target device"},
		{name: "device not reachable over SSH", device: newUnreachableDevice(), want: "not reachable over SSH"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := exec.Command(context.Background(), &stubContext{}, tc.device, map[string]any{"cmd": "uname"})
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestCommand_RefusesADeviceWithNoCredential proves a device with no
// usable secret is refused before any dial, rather than attempting an
// unauthenticated login.
func TestCommand_RefusesADeviceWithNoCredential(t *testing.T) {
	server := startShellSSHServer(t)
	rc := &stubContext{secrets: map[string]string{}, stats: map[string]any{}}

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "uname",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("expected a refusal when no credential is available")
	}
	if !strings.Contains(err.Error(), "no usable authentication method") {
		t.Errorf("error = %v, want the authentication refusal", err)
	}
	if strings.Contains(err.Error(), "dial") {
		t.Errorf("error = %v, want the refusal before any dial", err)
	}
}

// TestCommand_RunsAndReportsWhatHappened is the success path: a real
// command runs on the far end of a real SSH session and every stat comes
// back with the value the command actually produced.
func TestCommand_RunsAndReportsWhatHappened(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	result, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"cmd":                           "printf 'out\\n'; printf 'err\\n' >&2",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	// A command that ran always reports changed: nothing can inspect
	// "printf" and decide whether it altered anything, so claiming
	// otherwise would be a guess dressed as a fact.
	if !result.Changed {
		t.Error("a command that ran reported no change")
	}

	// The command was NOT interpreted by a shell, so the semicolon and
	// the redirect above are literal arguments to printf rather than
	// syntax. That is the whole promise of this method, and it is why
	// stdout carries them back as text.
	if got := rc.stats["rc"]; got != 0 {
		t.Errorf("rc = %v, want 0", got)
	}
	if got, _ := rc.stats["stdout"].(string); !strings.Contains(got, ";") {
		t.Errorf("stdout = %q, want the semicolon to have reached printf as text rather than being run as a shell operator", got)
	}
	if got := rc.stats["skipped"]; got != false {
		t.Errorf("skipped = %v, want false", got)
	}
	if got, _ := rc.stats["cmd"].(string); got == "" {
		t.Error("cmd stat is empty, so a reader cannot see what was actually sent")
	}
}

// TestCommand_SeparatesStdoutAndStderr proves the two streams come back
// as two stats, and that the trailing newline a command almost always
// emits is trimmed so a later comparison against a plain string works.
func TestCommand_SeparatesStdoutAndStderr(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"/bin/sh", "-c", "echo out-line; echo err-line >&2"},
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if got := rc.stats["stdout"]; got != "out-line" {
		t.Errorf("stdout = %q, want %q", got, "out-line")
	}
	if got := rc.stats["stderr"]; got != "err-line" {
		t.Errorf("stderr = %q, want %q", got, "err-line")
	}
}

// TestCommand_NonZeroExitIsAFailure proves a command that reports
// failure fails the task, and that the error carries enough to act on.
//
// Ansible's command module draws the same line, and it is the right one:
// a task whose command failed has failed, and a module that returned
// success with a non-zero rc buried in a stat would make a broken run
// look like a working one.
func TestCommand_NonZeroExitIsAFailure(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"/bin/sh", "-c", "echo why-it-failed >&2; exit 3"},
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a command that exited non-zero was reported as success")
	}
	if !strings.Contains(err.Error(), "exited 3") {
		t.Errorf("error = %v, want it to name the exit status", err)
	}
	if !strings.Contains(err.Error(), "why-it-failed") {
		t.Errorf("error = %v, want it to carry what the command said on stderr", err)
	}
	// The stats are recorded before the error is returned, so anything
	// that reads them still sees what happened.
	if got := rc.stats["rc"]; got != 3 {
		t.Errorf("rc stat = %v, want 3", got)
	}
}

// TestCommand_NonZeroExitFallsBackToStdout covers the other half of the
// failure message: a command that explains itself on stdout and says
// nothing on stderr must not produce an error that reads only "exited 1".
func TestCommand_NonZeroExitFallsBackToStdout(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"/bin/sh", "-c", "echo said-on-stdout; exit 1"},
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "said-on-stdout") {
		t.Errorf("error = %v, want it to fall back to stdout when stderr is empty", err)
	}
}

// TestCommand_SilentFailureStillSaysSomething covers the last branch of
// the failure message: a command that exits non-zero and writes nothing
// at all.
func TestCommand_SilentFailureStillSaysSomething(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"/bin/sh", "-c", "exit 4"},
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("error = %v, want it to say the command produced no output", err)
	}
}

// TestCommand_IsIdempotentWithCreates is the contract every later module
// in this tier copies, and the one the roadmap asks each of them to
// prove: run twice, report changed and then not changed.
//
// A command cannot be inspected, so creates is how the author says what
// its work having already happened looks like. Without it this method
// reports changed both times, which is honest rather than broken; with
// it the second run does not connect a session at all.
func TestCommand_IsIdempotentWithCreates(t *testing.T) {
	server := startShellSSHServer(t)
	marker := filepath.Join(t.TempDir(), "VERSION")

	params := map[string]any{
		"argv":                          []any{"touch", marker},
		"creates":                       marker,
		"insecure_skip_host_key_verify": true,
	}

	first := newStubContext(server)
	result, err := exec.Command(context.Background(), first, newDevice(server, ""), params)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !result.Changed {
		t.Fatal("the first run reported no change, but it created the marker")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the first run did not actually create %s: %v", marker, err)
	}
	if got := first.stats["skipped"]; got != false {
		t.Errorf("first run skipped = %v, want false", got)
	}

	second := newStubContext(server)
	result, err = exec.Command(context.Background(), second, newDevice(server, ""), params)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if result.Changed {
		t.Error("the second run reported a change, so this module is not idempotent under creates")
	}
	if got := second.stats["skipped"]; got != true {
		t.Errorf("second run skipped = %v, want true", got)
	}
	if got, _ := second.stats["msg"].(string); !strings.Contains(got, marker) {
		t.Errorf("second run msg = %q, want it to name the path that caused the skip", got)
	}
	if got := second.stats["cmd"]; got != "" {
		t.Errorf("second run cmd = %q, want it empty: no command was sent", got)
	}
}

// TestCommand_RemovesSkipsWhenThePathIsAlreadyGone is the mirror of
// creates: a path whose absence means the work is done.
func TestCommand_RemovesSkipsWhenThePathIsAlreadyGone(t *testing.T) {
	server := startShellSSHServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "stale")

	params := map[string]any{
		"argv":                          []any{"rm", "-f", target},
		"removes":                       target,
		"insecure_skip_host_key_verify": true,
	}

	// Nothing there yet, so there is nothing to remove.
	rc := newStubContext(server)
	result, err := exec.Command(context.Background(), rc, newDevice(server, ""), params)
	if err != nil {
		t.Fatalf("run against an absent path: %v", err)
	}
	if result.Changed {
		t.Error("removes did not short-circuit a run against a path that is already gone")
	}

	// Once the path exists, the same task runs.
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", target, err)
	}
	rc = newStubContext(server)
	result, err = exec.Command(context.Background(), rc, newDevice(server, ""), params)
	if err != nil {
		t.Fatalf("run against a present path: %v", err)
	}
	if !result.Changed {
		t.Error("removes short-circuited a run against a path that was there")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the command did not remove %s", target)
	}
}

// TestCommand_ChdirRunsInTheNamedDirectory proves chdir reaches the
// device, and that a directory which does not exist stops the command
// rather than silently running it somewhere else.
//
// That second half is the one that matters. A typo in chdir applied to
// the wrong path is a much worse outcome than a failed task.
func TestCommand_ChdirRunsInTheNamedDirectory(t *testing.T) {
	server := startShellSSHServer(t)
	dir := t.TempDir()

	t.Run("an existing directory is entered", func(t *testing.T) {
		rc := newStubContext(server)
		_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
			"argv":                          []any{"pwd"},
			"chdir":                         dir,
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Command: %v", err)
		}
		got, _ := rc.stats["stdout"].(string)
		// macOS resolves a temp dir through a symlink, so compare the
		// base name rather than the whole path.
		if filepath.Base(got) != filepath.Base(dir) {
			t.Errorf("pwd reported %q, want the command to have run in %q", got, dir)
		}
	})

	t.Run("a missing directory stops the command", func(t *testing.T) {
		rc := newStubContext(server)
		marker := filepath.Join(dir, "must-not-exist")
		_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
			"argv":                          []any{"touch", marker},
			"chdir":                         filepath.Join(dir, "no-such-directory"),
			"insecure_skip_host_key_verify": true,
		})
		if err == nil {
			t.Fatal("a chdir into a missing directory was reported as success")
		}
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Error("the command ran anyway, in whatever directory the login happened to land in")
		}
	})
}

// TestCommand_UsesTheDevicesWorkingDirectory proves the
// CommandExecCapable accessor is read rather than decorative, and that a
// task's own chdir wins over it.
func TestCommand_UsesTheDevicesWorkingDirectory(t *testing.T) {
	server := startShellSSHServer(t)
	deviceDir := t.TempDir()
	taskDir := t.TempDir()

	t.Run("the device's directory is used when the task names none", func(t *testing.T) {
		rc := newStubContext(server)
		_, err := exec.Command(context.Background(), rc, newDevice(server, deviceDir), map[string]any{
			"argv":                          []any{"pwd"},
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Command: %v", err)
		}
		got, _ := rc.stats["stdout"].(string)
		if filepath.Base(got) != filepath.Base(deviceDir) {
			t.Errorf("pwd reported %q, want the device's own working directory %q", got, deviceDir)
		}
	})

	t.Run("the task's chdir wins", func(t *testing.T) {
		rc := newStubContext(server)
		_, err := exec.Command(context.Background(), rc, newDevice(server, deviceDir), map[string]any{
			"argv":                          []any{"pwd"},
			"chdir":                         taskDir,
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Command: %v", err)
		}
		got, _ := rc.stats["stdout"].(string)
		if filepath.Base(got) != filepath.Base(taskDir) {
			t.Errorf("pwd reported %q, want the task's own chdir %q", got, taskDir)
		}
	})
}

// TestCommand_RunsWithoutACommandExecAccessor proves a device that
// declares CommandExecCapable without implementing its accessor still
// works, which is the Runner's own device adapter exactly.
//
// A hard type assertion here would refuse a dispatch the Controller
// correctly admitted, and it would refuse it at the far end of a
// network hop with a message about a Go interface.
func TestCommand_RunsWithoutACommandExecAccessor(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	result, err := exec.Command(context.Background(), rc, newSSHOnlyDevice(server), map[string]any{
		"argv":                          []any{"echo", "reached"},
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Command against a device with no WorkingDirectory accessor: %v", err)
	}
	if !result.Changed {
		t.Error("the command ran but reported no change")
	}
	if got := rc.stats["stdout"]; got != "reached" {
		t.Errorf("stdout = %q, want %q", got, "reached")
	}
}

// TestCommand_PipesStdin proves the stdin parameter reaches the remote
// command.
func TestCommand_PipesStdin(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"cat"},
		"stdin":                         "piped-value\n",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if got := rc.stats["stdout"]; got != "piped-value" {
		t.Errorf("stdout = %q, want the piped value back", got)
	}
}

// TestCommand_NoShellInterpretsTheArguments is the injection assertion,
// and it is the reason this method exists as something other than
// exec.shell.
//
// Every payload below is a real attempt to get a second command to run.
// If any of them succeeded, the marker file would exist, and a runbook
// parameter would have become remote code execution.
func TestCommand_NoShellInterpretsTheArguments(t *testing.T) {
	server := startShellSSHServer(t)
	dir := t.TempDir()

	payloads := []string{
		"; touch " + filepath.Join(dir, "semicolon"),
		"&& touch " + filepath.Join(dir, "andand"),
		"$(touch " + filepath.Join(dir, "subshell") + ")",
		"`touch " + filepath.Join(dir, "backtick") + "`",
		"| touch " + filepath.Join(dir, "pipe"),
		"\ntouch " + filepath.Join(dir, "newline"),
	}

	for _, payload := range payloads {
		t.Run(strings.TrimSpace(payload[:2]), func(t *testing.T) {
			rc := newStubContext(server)
			_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
				"argv":                          []any{"echo", payload},
				"insecure_skip_host_key_verify": true,
			})
			if err != nil {
				t.Fatalf("Command: %v", err)
			}
			// The payload must come back verbatim as echo's argument,
			// which is the positive half of the assertion: it was passed
			// through, not swallowed.
			if got, _ := rc.stats["stdout"].(string); got != strings.ReplaceAll(payload, "\n", "\n") {
				t.Errorf("stdout = %q, want the payload echoed back verbatim %q", got, payload)
			}
		})
	}

	// The negative half: none of the payloads created anything.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("a shell interpreted an argument and created %v: this is remote command injection", names)
	}
}

// TestCommand_StatFailureIsReported covers the branch where the command
// succeeded and recording its answer did not.
//
// Reported rather than swallowed, because a runbook that registered this
// task's result would otherwise read an absent stat as an empty output
// and carry on.
func TestCommand_StatFailureIsReported(t *testing.T) {
	server := startShellSSHServer(t)
	rc := newStubContext(server)
	rc.statErr = errStat

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"true"},
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the result was swallowed")
	}
}

// TestCommand_SkipStatFailureIsReported covers the same branch on the
// skip path, which writes a different set of stats and would otherwise
// go uncovered.
func TestCommand_SkipStatFailureIsReported(t *testing.T) {
	server := startShellSSHServer(t)
	marker := filepath.Join(t.TempDir(), "already-there")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", marker, err)
	}

	rc := newStubContext(server)
	rc.statErr = errStat

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"touch", marker},
		"creates":                       marker,
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the skip was swallowed")
	}
}

// TestCommand_HostKeyVerificationIsOnByDefault proves the fail-closed
// default survived: without the explicit opt-out and without a
// known_hosts entry, this method refuses rather than trusting whatever
// key the far end presents.
func TestCommand_HostKeyVerificationIsOnByDefault(t *testing.T) {
	// An empty home directory is what "this operator has no known_hosts
	// yet" actually looks like.
	t.Setenv("HOME", t.TempDir())

	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv": []any{"echo", "should-not-run"},
	})
	if err == nil {
		t.Fatal("a target with no known_hosts entry was trusted")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Errorf("error = %v, want it to name the missing known_hosts file", err)
	}
}

// errStat is the deliberate SetStat failure the two stat-failure tests
// above install.
var errStat = statError("deliberate stat failure")

// statError is a minimal error type, so the two tests above do not each
// construct one.
type statError string

func (e statError) Error() string { return string(e) }

// TestCommand_ConnectionFailureDuringTheTaskIsReported covers the three
// branches only a connection that dies partway through can reach.
//
// Each case uses a server that accepts the handshake and then refuses
// session channels once its budget runs out, which is a real
// protocol-level refusal rather than an injected Go error. That matters:
// the failure a device dropping a connection produces is exactly this
// shape, and the point of these branches is that such a failure is
// reported rather than read as an answer. Reading a failed creates check
// as "the path does not exist" would re-apply a change that had already
// been applied.
func TestCommand_ConnectionFailureDuringTheTaskIsReported(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", present, err)
	}

	tests := []struct {
		name string
		// budget is how many session channels the server accepts before
		// refusing; the failing one is always the next after that.
		budget int
		params map[string]any
		want   string
	}{
		{
			name:   "the command itself cannot open a session",
			budget: 0,
			params: map[string]any{"argv": []any{"true"}},
			want:   "open session",
		},
		{
			name:   "the creates check cannot open a session",
			budget: 0,
			params: map[string]any{"argv": []any{"true"}, "creates": filepath.Join(dir, "whatever")},
			want:   "checking creates path",
		},
		{
			name:   "the removes check cannot open a session",
			budget: 1, // the creates check succeeds, the removes check does not
			params: map[string]any{
				"argv":    []any{"true"},
				"creates": filepath.Join(dir, "absent"),
				"removes": present,
			},
			want: "checking removes path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := startShellSSHServerWithSessionBudget(t, tc.budget)
			rc := newStubContext(server)

			params := map[string]any{"insecure_skip_host_key_verify": true}
			for k, v := range tc.params {
				params[k] = v
			}

			_, err := exec.Command(context.Background(), rc, newDevice(server, ""), params)
			if err == nil {
				t.Fatal("a connection failure partway through the task was reported as success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// FuzzCommandParams is this module's parameter fuzz target, which Phase
// 38 requires before a status flips.
//
// The boundary it guards is a real one: params come out of a runbook
// file, and the two forms this method accepts (a free-form string it
// splits, and a pre-split list) are both parsed before anything else
// happens. The property under test is that no combination of bytes ever
// panics, and that the two outcomes are the only two: a refusal, or an
// argument vector that survives the split-then-quote round trip a POSIX
// shell reads back unchanged.
//
// It never connects. The device is nil, so any input that gets past
// parsing fails at the target check, which keeps every iteration to
// microseconds and keeps a fuzzer off the network.
func FuzzCommandParams(f *testing.F) {
	f.Add("uname -a", "", "", "")
	f.Add("", "tar", "-xzf", "/tmp/x.tgz")
	f.Add("echo hi; rm -rf /", "", "", "")
	f.Add(`echo 'unterminated`, "", "", "")
	f.Add("", "", "", "")
	f.Add("\\", "\x00", "\n", "'")
	f.Add("$(whoami)", "`id`", "|", "&&")

	f.Fuzz(func(t *testing.T, cmd, arg0, arg1, arg2 string) {
		params := map[string]any{
			"chdir":                         arg2,
			"creates":                       arg1,
			"insecure_skip_host_key_verify": true,
		}
		if cmd != "" {
			params["cmd"] = cmd
		} else {
			params["argv"] = []any{arg0, arg1, arg2}
		}

		// A nil device means anything that parses cleanly stops at the
		// target check. Either way this must return an error rather than
		// panic, and must never claim a change it did not make.
		result, err := exec.Command(context.Background(), &stubContext{}, nil, params)
		if err == nil {
			t.Fatalf("Command with no target device returned no error for params %+v", params)
		}
		if result.Changed {
			t.Fatalf("Command reported a change alongside an error for params %+v", params)
		}
	})
}

// TestCommand_CreatesAndRemovesResolveFromChdir is the regression test
// for a guard that looked in the wrong directory.
//
// The command runs under chdir and the existence check did not, so a
// relative creates was evaluated wherever the account happened to log
// in. That made creates silently do nothing, which is the one thing it
// exists to do. The removes mirror is worse than useless: the check
// found nothing where it was looking, reported the work already done,
// and left the file sitting in chdir untouched while the task reported
// success.
//
// Ansible's own command module changes directory before evaluating
// creates, and this package's doc comment claims to mean what Ansible
// means, so the divergence was a broken promise as well as a bug.
func TestCommand_CreatesAndRemovesResolveFromChdir(t *testing.T) {
	server := startShellSSHServer(t)

	t.Run("a relative creates fires", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1"), 0o600); err != nil {
			t.Fatalf("creating the marker: %v", err)
		}

		rc := newStubContext(server)
		result, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
			"argv":                          []any{"touch", "ran"},
			"chdir":                         dir,
			"creates":                       "VERSION",
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Command: %v", err)
		}
		if result.Changed {
			t.Error("a relative creates naming a file that exists in chdir did not short-circuit the task")
		}
		if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
			t.Error("the command ran even though creates should have skipped it")
		}
	})

	t.Run("a relative removes does not skip work that is still pending", func(t *testing.T) {
		dir := t.TempDir()
		stale := filepath.Join(dir, "stale.lock")
		if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
			t.Fatalf("creating the stale file: %v", err)
		}

		rc := newStubContext(server)
		result, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
			"argv":                          []any{"rm", "-f", "stale.lock"},
			"chdir":                         dir,
			"removes":                       "stale.lock",
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Command: %v", err)
		}
		if !result.Changed {
			t.Fatal("a relative removes naming a file that exists in chdir skipped the task, leaving the file in place")
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Errorf("the task reported changed but %s is still there", stale)
		}
	})

	t.Run("the device's own working directory counts too", func(t *testing.T) {
		// No chdir param at all: the directory comes from
		// CommandExecCapable, which is the path a real linux_server takes.
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1"), 0o600); err != nil {
			t.Fatalf("creating the marker: %v", err)
		}

		rc := newStubContext(server)
		result, err := exec.Command(context.Background(), rc, newDevice(server, dir), map[string]any{
			"argv":                          []any{"touch", "ran"},
			"creates":                       "VERSION",
			"insecure_skip_host_key_verify": true,
		})
		if err != nil {
			t.Fatalf("Command: %v", err)
		}
		if result.Changed {
			t.Error("the guard ignored the working directory the device declares")
		}
	})
}

// TestCommand_UnenterableChdirIsAnErrorNotAnAbsence proves a directory
// the task cannot enter fails the task, rather than being read as "the
// path is not there."
//
// Reading it as an absence is what silently skips a removes task while
// reporting success, so this distinction is worth its own exit code on
// the wire.
func TestCommand_UnenterableChdirIsAnErrorNotAnAbsence(t *testing.T) {
	server := startShellSSHServer(t)
	missing := filepath.Join(t.TempDir(), "no-such-directory")

	for _, guard := range []string{"creates", "removes"} {
		t.Run(guard, func(t *testing.T) {
			rc := newStubContext(server)
			_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
				"argv":                          []any{"true"},
				"chdir":                         missing,
				guard:                           "whatever",
				"insecure_skip_host_key_verify": true,
			})
			if err == nil {
				t.Fatalf("a %s check against an unenterable directory was treated as an answer", guard)
			}
			if !strings.Contains(err.Error(), "cannot enter directory") {
				t.Errorf("error = %v, want it to name the directory it could not enter", err)
			}
		})
	}
}

// TestCommand_ChdirIsNotParsedAsACdOption proves a directory whose name
// begins with a dash is treated as a directory.
//
// Quoting stops word splitting and expansion; it does not stop option
// parsing. Without cd's "--" terminator, chdir "-P" is consumed as cd's
// physical-path flag and cd then succeeds into the home directory, so the
// command runs somewhere the author never named and the task reports
// success. That is the exact failure the "&&" was already there to
// prevent, arriving through a door the quoting left open.
func TestCommand_ChdirIsNotParsedAsACdOption(t *testing.T) {
	server := startShellSSHServer(t)

	for _, dash := range []string{"-P", "-L"} {
		t.Run(dash, func(t *testing.T) {
			rc := newStubContext(server)
			_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
				"argv":                          []any{"pwd"},
				"chdir":                         dash,
				"insecure_skip_host_key_verify": true,
			})
			if err == nil {
				got, _ := rc.stats["stdout"].(string)
				t.Fatalf("chdir %q was consumed as a cd option and the command ran in %q instead of failing", dash, got)
			}
		})
	}

	t.Run("a real directory named -P is entered", func(t *testing.T) {
		// The terminator has to make a genuinely dash-named directory
		// work, not merely make every dash-named chdir fail.
		parent := t.TempDir()
		odd := filepath.Join(parent, "-P")
		if err := os.Mkdir(odd, 0o700); err != nil {
			t.Fatalf("creating %s: %v", odd, err)
		}

		rc := newStubContext(server)
		if _, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
			"argv":                          []any{"pwd"},
			"chdir":                         odd,
			"insecure_skip_host_key_verify": true,
		}); err != nil {
			t.Fatalf("Command against a directory literally named -P: %v", err)
		}
		got, _ := rc.stats["stdout"].(string)
		if filepath.Base(got) != "-P" {
			t.Errorf("pwd reported %q, want the directory named -P", got)
		}
	})
}

// TestCommand_LargeStdinACommandNeverReadsStillSucceeds is the
// regression test for a successful command reported as an opaque
// failure.
//
// x/crypto/ssh's Session.Wait returns the standard input copy's error
// whenever the command's own exit status was clean. When the library
// owns that copy, a command that exits successfully without draining its
// input closes the channel while the copy is still writing, and the
// resulting EOF surfaces as a failed task with the real exit status,
// stdout and stderr all discarded. Anything that reads a header and
// stops, or ignores stdin entirely, does this.
//
// It lives here rather than beside the code it covers because this is
// where it reproduces: a real /bin/sh on the far end of a real SSH
// session, with real os/exec plumbing between the channel and the
// process. The in-process handler in pkg/remoteexec's own tests never
// developed the same timing and passed against the broken version, which
// is worth stating plainly, because a regression test that cannot fail
// is worse than none.
//
// The size matters. A short payload completes before the remote can
// close and the failure does not appear at all; it was measured absent
// at 256 KiB and present at every attempt from 512 KiB up.
func TestCommand_LargeStdinACommandNeverReadsStillSucceeds(t *testing.T) {
	const payloadSize = 1 << 20

	server := startShellSSHServer(t)
	rc := newStubContext(server)

	result, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		// true exits 0 immediately and reads nothing.
		"argv":                          []any{"true"},
		"stdin":                         strings.Repeat("x", payloadSize),
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("a command that exited cleanly without draining %d bytes of stdin was reported as a failure: %v", payloadSize, err)
	}
	if !result.Changed {
		t.Error("the command ran but reported no change")
	}
	if got := rc.stats["rc"]; got != 0 {
		t.Errorf("rc = %v, want 0", got)
	}
	// The stats have to survive too. The old failure discarded them
	// entirely, so a runbook registering this task saw nothing at all.
	if _, ok := rc.stats["stdout"]; !ok {
		t.Error("no stdout stat was recorded, so a registering task would see nothing")
	}
}

// TestCommand_LargeStdinIsStillDeliveredToACommandThatReadsIt is the
// control for the test above: the fix must not have quietly stopped
// sending the input.
func TestCommand_LargeStdinIsStillDeliveredToACommandThatReadsIt(t *testing.T) {
	const payloadSize = 1 << 20

	server := startShellSSHServer(t)
	rc := newStubContext(server)

	_, err := exec.Command(context.Background(), rc, newDevice(server, ""), map[string]any{
		"argv":                          []any{"wc", "-c"},
		"stdin":                         strings.Repeat("x", payloadSize),
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	got, _ := rc.stats["stdout"].(string)
	if strings.TrimSpace(got) != strconv.Itoa(payloadSize) {
		t.Errorf("the remote command counted %q bytes, want %d: the input was truncated", strings.TrimSpace(got), payloadSize)
	}
}

// Tests for the POSIX steps' scripts and answers, which run unchanged on a
// Mac and on FreeBSD; the scripts themselves run under /bin/sh here, since
// they are POSIX shell and this machine has one (Windows has none, and
// never runs them).
package oscheck

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// posix is a POSIX machine as the Mac's check describes one.
var posix = POSIX{Tool: "maccheck", Machine: "the Mac", Dir: ".pleiades-maccheck", Path: "/opt/homebrew/bin", Wrap: "caffeinate -dims ", Files: "files"}

// needsSh skips a test that runs a script under /bin/sh on a machine with
// none: the POSIX scripts run only on macOS and FreeBSD.
func needsSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX scripts run under /bin/sh, which Windows does not have and never runs them under")
	}
}

// TestTestScript: the packages run in the commit's tree under Wrap, both
// passes, as a job of their own that is written down, stopped by a SIGTERM
// to the script, and the script exits with the tests' status.
func TestTestScript(t *testing.T) {
	s := posix.TestScript("abc123", 2, []string{"github.com/x/a", "github.com/x/b"})
	for _, w := range []string{
		"exec 2>&1",
		`export PATH="$PATH:/opt/homebrew/bin"`,
		`cd "$HOME/.pleiades-maccheck/abc123" || exit 2`,
		"rm -f .pleiades-record-full.json .pleiades-record-repeat.json\nset -m",
		"caffeinate -dims /bin/sh -c 'go run ./tools/testgate -full -report-out .pleiades-record-full.json -p 2 github.com/x/a github.com/x/b && go run ./tools/testgate -repeat 3 -strict -report-out .pleiades-record-repeat.json -p 2 github.com/x/a github.com/x/b' &",
		`echo "$job" > .maccheck.pid`,
		"set +m",
		`trap 'stop_run "$job"; rm -f .maccheck.pid; exit 143' TERM HUP INT`,
		`wait "$job"`,
	} {
		if !strings.Contains(s, w) {
			t.Errorf("script does not hold %q:\n%s", w, s)
		}
	}
	if !strings.HasSuffix(s, "rm -f .maccheck.pid\nexit $status") {
		t.Errorf("script does not end by removing its pid file and exiting with the tests' status:\n%s", s)
	}
	unwrapped := POSIX{Tool: "bsdcheck", Dir: ".b"}.TestScript("abc", 0, []string{"a/b"})
	if !strings.Contains(unwrapped, "\n/bin/sh -c 'go run ./tools/testgate -full -report-out .pleiades-record-full.json -p 0 a/b") {
		t.Errorf("with no Wrap the tests run under sh alone:\n%s", unwrapped)
	}
}

// scriptHome is a fake home for running the scripts: a tree for commit abc,
// and a stand-in go first on the PATH that records its own process ID and
// sleeps, so a stop has a run to end and a test can see the run is gone.
type scriptHome struct {
	home, pids string
	x          POSIX
}

func newScriptHome(t *testing.T) scriptHome {
	t.Helper()
	needsSh(t)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	pids := filepath.Join(home, "go.pids")
	for _, dir := range []string{bin, filepath.Join(home, "trees", "abc")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stand := "#!/bin/sh\necho $$ >> \"" + pids + "\"\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stand), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program
		t.Fatal(err)
	}
	return scriptHome{home: home, pids: pids, x: POSIX{Tool: "xcheck", Dir: "trees", Path: bin}}
}

// sh is script under /bin/sh in the fake home, the stand-in go first on its
// PATH (the script adds its own Path after the PATH it is given).
func (h scriptHome) sh(script string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+h.home, "PATH="+h.x.Path+":/usr/bin:/bin")
	return cmd
}

// startRun starts the test script and waits until the stand-in go is
// running and the run's job is written down.
func (h scriptHome) startRun(t *testing.T) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	run := h.sh(h.x.TestScript("abc", 1, []string{"a/b"}))
	output := &strings.Builder{}
	run.Stdout = output
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Process.Kill() })
	pidFile := filepath.Join(h.home, "trees", "abc", h.x.PIDFile())
	deadline := time.Now().Add(10 * time.Second)
	for !(FileExists(pidFile) && FileExists(h.pids)) {
		if time.Now().After(deadline) {
			t.Fatalf("the run never started its tests:\n%s", output)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return run, output
}

// requireStopped waits for run to end, requires a status that is not 0 (a
// stopped run must never read as a pass), the stand-in go gone, and the
// pid file removed.
func (h scriptHome) requireStopped(t *testing.T, run *exec.Cmd, output *strings.Builder) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("the stopped run exited %v, want a status that is not 0\n%s", err, output)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the run did not end within 10s of being stopped\n%s", output)
	}
	data, err := os.ReadFile(h.pids)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(line)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for alive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("the run's go (pid %d) is still running after the stop", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if FileExists(filepath.Join(h.home, "trees", "abc", h.x.PIDFile())) {
		t.Error("the stopped run left its pid file behind, where a later stop could reach a reused number")
	}
}

// alive reports whether process pid still runs.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}

// TestStopScript_EndsTheRun: the stop script, run as the check runs it
// after an interrupt, ends the test run and every process under it, with or
// without job control in this machine's /bin/sh.
func TestStopScript_EndsTheRun(t *testing.T) {
	h := newScriptHome(t)
	run, output := h.startRun(t)
	if out, err := h.sh(h.x.StopScript("abc")).CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("stop: %v %q", err, out)
	}
	h.requireStopped(t, run, output)
}

// TestTestScript_ATimeoutEndsTheTests: a SIGTERM to the script itself, as
// sshd delivers when the task's timeout runs out, ends the tests too,
// rather than leaving them running behind it.
func TestTestScript_ATimeoutEndsTheTests(t *testing.T) {
	h := newScriptHome(t)
	run, output := h.startRun(t)
	if err := run.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	h.requireStopped(t, run, output)
}

// TestTestScript_AMissingTreeIsStatus2: a tree that was never sent ends
// the run at once with status 2, saying where it looked.
func TestTestScript_AMissingTreeIsStatus2(t *testing.T) {
	h := newScriptHome(t)
	out, err := h.sh(h.x.TestScript("missing", 1, []string{"a/b"})).Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "missing") {
		t.Fatalf("a missing tree: %q, %v", out, err)
	}
}

// TestStopScript_NothingToStop does nothing when there is no pid file or it
// holds anything but a number.
func TestStopScript_NothingToStop(t *testing.T) {
	h := newScriptHome(t)
	tree := filepath.Join(h.home, "trees", "abc")
	for _, content := range []string{"", "1; rm -rf /", "-1"} {
		if content != "" {
			if err := os.WriteFile(filepath.Join(tree, h.x.PIDFile()), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if out, err := h.sh(h.x.StopScript("abc")).CombinedOutput(); err != nil || len(out) != 0 {
			t.Errorf("pid file %q: %v %q", content, err, out)
		}
	}
}

// TestPrepareScript keeps the newest trees, removes an earlier copy of
// this commit's, and prints the home directory, run under sh.
func TestPrepareScript(t *testing.T) {
	needsSh(t)
	home := t.TempDir()
	x := POSIX{Dir: "trees"}
	base := filepath.Join(home, "trees")
	for i, name := range []string{"old1", "old2", "old3", "abc"} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o700); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(time.Duration(i-10) * time.Minute)
		if err := os.Chtimes(filepath.Join(base, name), when, when); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("/bin/sh", "-c", x.prepareScript("abc"))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != home {
		t.Fatalf("prepare: %q, %v", out, err)
	}
	left, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	// The newest Kept-1 stay beside the tree about to arrive; abc, the
	// newest, was this commit's and is removed to be sent again.
	if strings.Join(names, " ") != "old3" {
		t.Errorf("left %v, want only old3 (Kept is %d)", names, Kept)
	}
}

// TestReadyAnswer accepts only the kernel asked for with every command and
// MinFreeGB free, naming what is wrong otherwise.
func TestReadyAnswer(t *testing.T) {
	needs := []Need{{"go", "install it"}, {"caffeinate", "it keeps the Mac awake"}}
	for out, want := range map[string]string{
		"Darwin\ngo\ncaffeinate\n12\n":  "",
		"Darwin\ngo\ncaffeinate\n4\n":   "",
		"Linux\ngo\ncaffeinate\n12\n":   "me@mac is not a Mac: its kernel is Linux",
		"Darwin\nno-go\ncaffeinate\n12": "me@mac has no go on its PATH (looked in its PATH and /opt/homebrew/bin): install it",
		"Darwin\ngo\nno-caffeinate\n12": "has no caffeinate",
		"Darwin\ngo\ncaffeinate\n0\n":   "me@mac has 0 GB free where its tests unpack and build, under the 4 GB a run needs: go clean -cache",
		"Darwin\ngo\ncaffeinate\nlots":  "answered its free space with \"lots\"",
		"Darwin\ngo\ncaffeinate\n":      "answered the readiness check",
		"":                              "answered the readiness check",
	} {
		err := posix.readyAnswer("me@mac", "Darwin", "Mac", out, needs)
		if (want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("readyAnswer(%q) = %v, want %q", out, err, want)
		}
	}
}

// TestReadyScript answers for this machine's own kernel and a command it
// has and one it lacks, run under sh.
func TestReadyScript(t *testing.T) {
	needsSh(t)
	out, err := exec.Command("/bin/sh", "-c", posix.readyScript([]Need{{"sh", ""}, {"no-such-command-" + strconv.Itoa(os.Getpid()), ""}})).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	kernel, _ := exec.Command("uname", "-s").Output()
	if len(lines) != 4 || lines[0] != strings.TrimSpace(string(kernel)) || lines[1] != "sh" || !strings.HasPrefix(lines[2], "no-no-such-command-") {
		t.Fatalf("readyScript printed %q", out)
	}
	if _, err := strconv.Atoi(lines[3]); err != nil {
		t.Errorf("the free space line is %q, not a whole number", lines[3])
	}
}

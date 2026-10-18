// Tests for the POSIX steps' scripts and answers, which run unchanged on a
// Mac and on FreeBSD; the scripts themselves run under /bin/sh here, since
// they are POSIX shell and this machine has one.
package oscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// posix is a POSIX machine as the Mac's check describes one.
var posix = POSIX{Tool: "maccheck", Machine: "the Mac", Dir: ".pleiades-maccheck", Path: "/opt/homebrew/bin", Wrap: "caffeinate -dims ", Files: "files"}

// TestTestScript: the packages run in the commit's tree under Wrap, both
// passes, as a job of their own whose process group is written down, and
// the script always ends by printing the exit status.
func TestTestScript(t *testing.T) {
	s := posix.TestScript("abc123", 2, []string{"github.com/x/a", "github.com/x/b"})
	for _, w := range []string{
		"exec 2>&1",
		`export PATH="$PATH:/opt/homebrew/bin"`,
		`cd "$HOME/.pleiades-maccheck/abc123" || { echo "maccheck-exit=2"; exit 0; }`,
		"set -m",
		"caffeinate -dims /bin/sh -c 'go run ./tools/testgate -full -p 2 github.com/x/a github.com/x/b && go run ./tools/testgate -repeat 3 -strict -p 2 github.com/x/a github.com/x/b' &",
		`echo "$job" > .maccheck.pid`,
		`wait "$job"`,
		"set +m",
		`echo "maccheck-exit=$?"`,
	} {
		if !strings.Contains(s, w) {
			t.Errorf("script does not hold %q:\n%s", w, s)
		}
	}
	unwrapped := POSIX{Tool: "bsdcheck", Dir: ".b"}.TestScript("abc", 0, []string{"a/b"})
	if !strings.Contains(unwrapped, "\n/bin/sh -c 'go run ./tools/testgate -full -p 0 a/b") {
		t.Errorf("with no Wrap the tests run under sh alone:\n%s", unwrapped)
	}
}

// TestTestAndStopScripts_RunUnderSh runs the real scripts in a fake home:
// a tree that is missing ends with status 2, and a run the stop script
// reaches ends, all of its processes with it, within seconds.
func TestTestAndStopScripts_RunUnderSh(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// go stands in for the toolchain: it sleeps, so the stop has a run to end.
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil { // #nosec G306 -- a test's own stand-in program
		t.Fatal(err)
	}
	x := POSIX{Tool: "xcheck", Dir: "trees", Path: bin}
	sh := func(script string) *exec.Cmd {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Env = append(os.Environ(), "HOME="+home)
		return cmd
	}

	out, err := sh(x.TestScript("missing", 1, []string{"a/b"})).Output()
	if _, code, ok := SplitMarker(string(out), Marker("xcheck")); err != nil || !ok || code != 2 || !strings.Contains(string(out), "missing") {
		t.Fatalf("a missing tree: %q, %v", out, err)
	}

	if err := os.MkdirAll(filepath.Join(home, "trees", "abc"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := sh(x.TestScript("abc", 1, []string{"a/b"}))
	var output strings.Builder
	run.Stdout = &output
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(home, "trees", "abc", x.PIDFile())
	deadline := time.Now().Add(10 * time.Second)
	for !FileExists(pidFile) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	start := time.Now()
	if out, err := sh(x.StopScript("abc")).CombinedOutput(); err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("the test script did not exit 0: %v\n%s", err, &output)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the stopped run took %s to end", took)
	}
	_, code, ok := SplitMarker(output.String(), Marker("xcheck"))
	if !ok || code == 0 {
		t.Errorf("a stopped run reported status %d (%v):\n%s", code, ok, &output)
	}
	// Nothing of the run is left: the group the pid file names is gone.
	pid, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("kill", "-0", "--", "-"+strings.TrimSpace(string(pid))).Run(); err == nil {
		t.Error("the run's process group is still alive after the stop")
	}
}

// TestStopScript does nothing when there is no pid file or it holds
// anything but a number.
func TestStopScript(t *testing.T) {
	home := t.TempDir()
	x := POSIX{Tool: "xcheck", Dir: "trees"}
	tree := filepath.Join(home, "trees", "abc")
	if err := os.MkdirAll(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "1; rm -rf /", "-1"} {
		if content != "" {
			if err := os.WriteFile(filepath.Join(tree, x.PIDFile()), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("/bin/sh", "-c", x.StopScript("abc"))
		cmd.Env = append(os.Environ(), "HOME="+home)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Errorf("pid file %q: %v %q", content, err, out)
		}
	}
}

// TestPrepareScript keeps the newest trees, removes an earlier copy of
// this commit's, and prints the home directory, run under sh.
func TestPrepareScript(t *testing.T) {
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

// TestReadyAnswer accepts only the kernel asked for with every command,
// naming what is wrong otherwise.
func TestReadyAnswer(t *testing.T) {
	needs := []Need{{"go", "install it"}, {"caffeinate", "it keeps the Mac awake"}}
	for out, want := range map[string]string{
		"Darwin\ngo\ncaffeinate\n":  "",
		"Linux\ngo\ncaffeinate\n":   "me@mac is not a Mac: its kernel is Linux",
		"Darwin\nno-go\ncaffeinate": "me@mac has no go on its PATH (looked in its PATH and /opt/homebrew/bin): install it",
		"Darwin\ngo\nno-caffeinate": "has no caffeinate",
		"":                          "answered the readiness check",
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
	out, err := exec.Command("/bin/sh", "-c", posix.readyScript([]Need{{"sh", ""}, {"no-such-command-" + strconv.Itoa(os.Getpid()), ""}})).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	kernel, _ := exec.Command("uname", "-s").Output()
	if len(lines) != 3 || lines[0] != strings.TrimSpace(string(kernel)) || lines[1] != "sh" || !strings.HasPrefix(lines[2], "no-no-such-command-") {
		t.Errorf("readyScript printed %q", out)
	}
}

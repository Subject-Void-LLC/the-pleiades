//go:build !windows

// Tests for the detached start script's guards and the parsing of its
// answers: a command that reached the device cut short is never started, and
// a login shell's startup output never stands in for an exit status or a job
// file.
package remoteexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestDetached_ACommandThatArrivesCutShortIsNeverStarted: the device's cat
// keeps only a prefix of what it is sent, as a channel that ends partway
// through the send leaves it. The prefix is a different command, so the start
// refuses it and nothing runs: the marker its whole would write, and its
// prefix would not, is absent, and so is any job directory.
func TestDetached_ACommandThatArrivesCutShortIsNeverStarted(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	head, err := lookPathOutside("head")
	if err != nil {
		t.Skipf("no head on this machine: %v", err)
	}
	fake := t.TempDir()
	script := "#!/bin/sh\nexec " + head + " -c 12\n"
	if err := os.WriteFile(filepath.Join(fake, "cat"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	marker := filepath.Join(home, "whole")
	_, err = conn.Run(WithDetach(context.Background()), "echo prefix; touch "+QuoteArg(marker))
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("Run of a command cut short = %v; want it refused as incomplete", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the whole command ran")
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Fatalf("job directories left behind: %v", left)
	}
}

// lookPathOutside finds name on PATH as an absolute path, before a test
// shadows it.
func lookPathOutside(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New(name + " not found")
}

// TestReadJobFile_RefusesWhatItCannotRead: a missing file, and a file over
// the caller's bound, are errors and never a truncated read.
func TestReadJobFile_RefusesWhatItCannotRead(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	if _, err := conn.readJobFile(context.Background(), QuoteArg(filepath.Join(home, "absent")), "x", 100); err == nil {
		t.Fatal("reading a missing job file succeeded")
	}
	big := filepath.Join(home, "big")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 200)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.readJobFile(context.Background(), QuoteArg(big), "x", 100); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("reading a job file over its bound = %v; want ErrOutputTooLarge", err)
	}
	got, err := conn.readJobFile(context.Background(), QuoteArg(big), "x", 200)
	if err != nil || got != strings.Repeat("x", 200) {
		t.Fatalf("reading a job file at its bound = %d bytes, %v", len(got), err)
	}
}

// TestPollRC_ReadsOnlyTheMarkedLine: the status comes from the marked last
// line after any startup output, and an answer with no such line, or a value
// that is not a number, is an error.
func TestPollRC_ReadsOnlyTheMarkedLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{rcPrefix + "0\n", 0, true},
		{"welcome to this host\n" + rcPrefix + "3\n", 3, true},
		{"3\n", 0, false},
		{rcPrefix + "three\n", 0, false},
	} {
		got, err := pollRC(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("pollRC(%q) = %d, %v; want %d, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

// TestAfterMarker_SkipsStartupOutput: the contents begin after the marker's
// own line, whether or not anything was printed ahead of it, and an answer
// with no marker line is refused.
func TestAfterMarker_SkipsStartupOutput(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"m\nbody\n", "body\n", true},
		{"banner\nm\nbody\nm\n", "body\nm\n", true},
		{"banner m\nbody", "", false},
	} {
		got, ok := afterMarker(tc.in, "m")
		if ok != tc.ok || got != tc.want {
			t.Errorf("afterMarker(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// makeJobDir makes a job directory under home holding a command and, unless
// pid is empty, a pid file, as a start cut off at either point leaves it.
func makeJobDir(t *testing.T, home, name, pid string) string {
	t.Helper()
	dir := filepath.Join(home, ".pleiades-async-"+strconv.Itoa(os.Getuid()), "job."+name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd"), []byte("echo secret-bearing command"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pid != "" {
		if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(pid+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runStopWithKillLogged runs detachStop's script for dir on this machine's sh
// with HOME set to home and kill replaced by a shell function that records
// its arguments and signals nothing, returning the exit status and the kill
// calls. kill is a regular built-in, so the function stands in for it. The
// function answers a signal with killRC (0: the group was there; 1: it was
// gone) and a kill -0 probe with 1, so the grace loop never waits.
func runStopWithKillLogged(t *testing.T, home, dir string, killRC int) (int, []string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "kills")
	script := "kill() { printf '%s\\n' \"$*\" >>" + QuoteArg(log) + "; case $1 in -0) return 1;; esac; return " + strconv.Itoa(killRC) + "; }\n" + detachStop(QuoteArg(dir))
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run the stop script: %v", err)
		}
		code = exit.ExitCode()
	}
	raw, _ := os.ReadFile(log)
	return code, strings.FieldsFunc(string(raw), func(r rune) bool { return r == '\n' })
}

// TestDetachStop_NeverSignalsAPidAJobCannotHave: a pid file holding what $!
// never is (empty, 0, 1, a leading zero, a sign, spaces, a value that would
// wrap) signals nothing, and the job's directory, which holds its command,
// is removed; a base that is a link is neither read nor removed. The script
// runs on this machine, so kill is a logging function here: kill -TERM -1
// from a real kill would signal every process this account owns, which is
// how this test's first version, run on the old script, took down the WSL
// machine it ran on. A control proves the function is what the script calls
// before any such pid is tried.
func TestDetachStop_NeverSignalsAPidAJobCannotHave(t *testing.T) {
	home := t.TempDir()

	// The control: a valid pid (a child of this test's own) reaches the
	// logging kill, and the child is never really signalled.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	pid := strconv.Itoa(child.Process.Pid)
	dir := makeJobDir(t, home, "control", pid)
	code, kills := runStopWithKillLogged(t, home, dir, 0)
	if want := []string{"-TERM -" + pid, "-0 -" + pid, "-KILL -" + pid}; code != 0 || !slices.Equal(kills, want) {
		t.Fatalf("the control stop of pid %s: exit %d, kills %q; want exit 0 and kills %q, the group only", pid, code, kills, want)
	}
	if !processAlive(child.Process.Pid) {
		t.Fatal("the control's child was really signalled: kill is not the logging function, so no unsafe pid is tried")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the control's job directory was left")
	}

	for i, pid := range []string{"", "0", "1", "01", "-1", " 1", "1 2", "+5", "not-a-pid", "12345678", "4294967297"} {
		dir := makeJobDir(t, home, "p"+strconv.Itoa(i), pid)
		code, kills := runStopWithKillLogged(t, home, dir, 0)
		if code == 0 || len(kills) != 0 {
			t.Errorf("pid %q: exit %d, kills %q; want a failed stop that signals nothing", pid, code, kills)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("pid %q: the job directory, which holds its command, was left", pid)
		}
	}

	// A base that is a link (to a job with a valid pid) is not trusted.
	other := t.TempDir()
	target := makeJobDir(t, other, "linked", pid)
	linkedHome := t.TempDir()
	base := filepath.Base(filepath.Dir(target))
	if err := os.Symlink(filepath.Dir(target), filepath.Join(linkedHome, base)); err != nil {
		t.Fatal(err)
	}
	code, kills = runStopWithKillLogged(t, linkedHome, filepath.Join(linkedHome, base, filepath.Base(target)), 0)
	if code == 0 || len(kills) != 0 {
		t.Errorf("a linked base: exit %d, kills %q; want a failed stop that signals nothing", code, kills)
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("a job through a linked base was removed")
	}
}

// TestDetachStop_AJobWhoseGroupHadEndedIsNeverSignalledByPid: when the
// job's process group is gone, the job has ended and its pid may belong to
// another process by now, so nothing is sent to the pid alone; the stop
// says the job had ended, and its directory is removed.
func TestDetachStop_AJobWhoseGroupHadEndedIsNeverSignalledByPid(t *testing.T) {
	home := t.TempDir()
	dir := makeJobDir(t, home, "ended", "4242")
	code, kills := runStopWithKillLogged(t, home, dir, 1)
	if want := []string{"-TERM -4242"}; code != detachStopEnded || !slices.Equal(kills, want) {
		t.Errorf("exit %d, kills %q; want exit %d and kills %q", code, kills, detachStopEnded, want)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the ended job's directory was left")
	}
}

// TestStopResult_NamesWhatTheStopFound: each exit status reads as its
// outcome, and each outcome names the job's state in words an error can
// carry.
func TestStopResult_NamesWhatTheStopFound(t *testing.T) {
	for _, tc := range []struct {
		res  Result
		err  error
		want stopOutcome
		text string
	}{
		{Result{ExitCode: 0}, nil, stopSignalled, "was stopped"},
		{Result{ExitCode: detachStopEnded}, nil, stopEnded, "had already finished"},
		{Result{ExitCode: 1}, nil, stopUnknown, "may be running"},
		{Result{}, errors.New("closed"), stopUnknown, "may be running"},
	} {
		got := stopResult(tc.res, tc.err)
		if got != tc.want || got.String() != tc.text {
			t.Errorf("stopResult(%+v, %v) = %v %q; want %v %q", tc.res, tc.err, got, got.String(), tc.want, tc.text)
		}
	}
}

// TestStopDetached_ReportsAStopOnlyWhenItSignalledAJob: through the real
// connection, a job directory with no pid (a start cut off before writing
// it) or one that is not a number is removed and the stop reports that it
// stopped nothing; a by-name stop under a HOME the account does not own
// finds no job. Only pids no kill could act on belong here: this server's
// commands run on this machine, so 1, 0 and the like are tried only in
// TestDetachStop_NeverSignalsAPidAJobCannotHave, against a logging kill.
func TestStopDetached_ReportsAStopOnlyWhenItSignalledAJob(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	t.Setenv("HOME", home)
	for _, pid := range []string{"", "not-a-pid"} {
		dir := makeJobDir(t, home, "a"+strconv.Itoa(len(pid)), pid)
		if conn.stopDetached(QuoteArg(dir)) != stopUnknown {
			t.Errorf("the stop of a job with pid %q reported a stop", pid)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("the job directory with pid %q, which holds its command, was left", pid)
		}
	}

	info, err := os.Stat("/tmp")
	if os.Getuid() == 0 || err != nil || info.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) {
		t.Skip("needs a /tmp the test's account does not own")
	}
	t.Setenv("HOME", "/tmp")
	if conn.stopDetached(jobByName("b0")) != stopUnknown {
		t.Error("a by-name stop under a HOME the account does not own reported a stop")
	}
}

// TestDetached_AFailedStartOnARawViewSendsNoStop: a Raw view's device has no
// sh to have started a job, so a failed start sends no stop script (which its
// CLI would take as a command); a plain view's failed start does, on a fresh
// login.
func TestDetached_AFailedStartOnARawViewSendsNoStop(t *testing.T) {
	for _, raw := range []bool{true, false} {
		conn, logins, _, _ := detachedConn(t)
		view := conn
		if raw {
			view = conn.Raw()
		}
		_ = conn.client.Close()
		before := logins()
		if _, err := view.Run(WithDetach(context.Background()), "true"); err == nil {
			t.Fatalf("raw %v: a detached run on a closed connection succeeded", raw)
		}
		stopped := logins() > before
		if stopped == raw {
			t.Errorf("raw %v: a stop was sent: %v", raw, stopped)
		}
	}
}

// TestAnsweredJobDir accepts only this job's directory: a job directory at
// all, under the name the client chose, so another job's directory in a
// start's answer is never polled, read or removed.
func TestAnsweredJobDir(t *testing.T) {
	const name = "0123456789abcdef01234567"
	for dir, want := range map[string]bool{
		"/home/u/.pleiades-async-1000/job." + name:                  true,
		"/home/u/.pleiades-async-1000/job.ffffffffffffffffffffffff": false,
		"/home/u/.pleiades-async-1000/job." + name + "0":            false,
		"/home/u/.pleiades-async-1000/job.x" + name[1:]:             false,
		"relative/.pleiades-async-1000/job." + name:                 false,
		"/home/u/.pleiades-async-1000/job." + name + "\n":           false,
		"/home/u/.pleiades-async-1000/other/../job." + name:         false,
	} {
		if got := answeredJobDir(dir, name); got != want {
			t.Errorf("answeredJobDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

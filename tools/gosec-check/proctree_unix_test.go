//go:build !windows

// This file tests that stopping gosec-check stops gosec and what it started,
// with the test binary standing in for both. The stand-in for gosec starts a
// child in a process group of its own, as gosec starts `go list`, and that
// child starts a grandchild in a group of its own, as a `go list` runs a
// compiler; killing the stand-in or its group reaches neither. A stand-in
// for gosec-check runs runGroup with main's own signal handling, so a test
// can send a signal to gosec-check's process group, as a Ctrl-C or make does.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The environment that makes the test binary a stand-in (TestMain).
const (
	// roleEnv is "gosec-exit" or "gosec-wait" (gosec, which then exits or
	// waits), "child" (a `go list`), or "checker" (gosec-check).
	roleEnv = "GOSEC_CHECK_TEST_ROLE"
	// pidsEnv names the file each stand-in appends "<role> <pid>" to.
	pidsEnv = "GOSEC_CHECK_TEST_PIDS"
)

// TestMain runs a stand-in when roleEnv asks for one, and the tests
// otherwise.
func TestMain(m *testing.M) {
	if role := os.Getenv(roleEnv); role != "" {
		os.Exit(standIn(role, os.Getenv(pidsEnv)))
	}
	os.Exit(m.Run())
}

// standIn plays role and returns its exit status.
func standIn(role, pids string) int {
	note := func(name string, pid int) error {
		f, err := os.OpenFile(pids, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "%s %d\n", name, pid)
		return err
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	switch role {
	case "checker":
		// gosec-check: runGroup over a stand-in for gosec, stopped by a
		// signal as main stops it.
		stop, release := interruptions()
		defer release()
		err := runGroup(standInCmd("gosec-wait", pids), stop, nil, os.Stderr)
		fmt.Println(err)
		if errors.Is(err, errStopped) {
			return 0
		}
		return 1
	case "child":
		// A `go list`: its own group, holding a grandchild in another.
		grandchild := exec.Command("sleep", "300")
		grandchild.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := grandchild.Start(); err != nil {
			return fail(err)
		}
		if err := note("grandchild", grandchild.Process.Pid); err != nil {
			return fail(err)
		}
		time.Sleep(300 * time.Second)
		return 0
	}
	// gosec: a child in a group of its own, sharing this output.
	child := standInCmd("child", pids)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Stdout = os.Stdout
	if err := child.Start(); err != nil {
		return fail(err)
	}
	if err := note("gosec", os.Getpid()); err != nil {
		return fail(err)
	}
	if err := note("child", child.Process.Pid); err != nil {
		return fail(err)
	}
	fmt.Println("{}")
	if role == "gosec-wait" {
		time.Sleep(300 * time.Second)
	}
	return 0
}

// standInCmd is a command running the test binary as role, noting pids in
// the file pids names.
func standInCmd(role, pids string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), roleEnv+"="+role, pidsEnv+"="+pids)
	return cmd
}

// pidFile returns the file the stand-ins will note their pids in, and kills
// every pid noted there when t ends.
func pidFile(t *testing.T) string {
	t.Helper()
	pids := filepath.Join(t.TempDir(), "pids")
	t.Cleanup(func() {
		for _, pid := range notedPids(pids) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return pids
}

// notedPids reads every pid noted so far, by role.
func notedPids(path string) map[string]int {
	data, _ := os.ReadFile(path)
	got := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		if name, pid, ok := strings.Cut(line, " "); ok {
			if n, err := strconv.Atoi(pid); err == nil {
				got[name] = n
			}
		}
	}
	return got
}

// waitPids waits until gosec, child and grandchild are all noted and
// returns them.
func waitPids(t *testing.T, path string) map[string]int {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if got := notedPids(path); len(got) >= 3 && got["gosec"] > 0 && got["child"] > 0 && got["grandchild"] > 0 {
			return got
		}
	}
	t.Fatalf("the stand-ins never noted their pids in %s: %v", path, notedPids(path))
	return nil
}

// running reports whether pid still exists, a zombie aside: a killed process
// lasts until whoever adopted it reaps it, so this waits up to five seconds
// for it to go.
func running(pid int) bool {
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return false
		}
	}
	return true
}

// requireAllEnded fails t for each noted process still running.
func requireAllEnded(t *testing.T, pids map[string]int) {
	t.Helper()
	for _, role := range []string{"gosec", "child", "grandchild"} {
		if running(pids[role]) {
			t.Errorf("the stand-in's %s (%d) is still running", role, pids[role])
		}
	}
}

// requireOwnGroups fails t unless the child and grandchild each lead a
// process group of their own, as `go list` and its compilers' group do, so
// the test shows what killing gosec's group would miss.
func requireOwnGroups(t *testing.T, pids map[string]int) {
	t.Helper()
	for _, role := range []string{"child", "grandchild"} {
		if pgid, err := syscall.Getpgid(pids[role]); err != nil || pgid != pids[role] {
			t.Fatalf("the %s %d is in group %d (%v), want a group of its own", role, pids[role], pgid, err)
		}
	}
}

// TestRunGroup_AnInterruptToGosecChecksGroupStopsEverything is a Ctrl-C:
// SIGINT to the whole process group of a gosec-check running runGroup, as
// the terminal, make or a watchdog sends it. gosec, its child and its
// grandchild all end, each of the last two in a group of its own, and
// gosec-check reports it was stopped. When gosec shared gosec-check's group,
// the signal killed gosec on delivery, gosec-check then found nothing under
// it, and the child and grandchild ran on.
func TestRunGroup_AnInterruptToGosecChecksGroupStopsEverything(t *testing.T) {
	pids := pidFile(t)
	checker := standInCmd("checker", pids)
	checker.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out strings.Builder
	checker.Stdout = &out
	if err := checker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-checker.Process.Pid, syscall.SIGKILL) })
	noted := waitPids(t, pids)
	requireOwnGroups(t, noted)

	if err := syscall.Kill(-checker.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := checker.Wait(); err != nil {
		t.Errorf("gosec-check after the interrupt: %v, output %q", err, out.String())
	}
	if !strings.Contains(out.String(), errStopped.Error()) {
		t.Errorf("gosec-check printed %q, want %q", out.String(), errStopped)
	}
	requireAllEnded(t, noted)
}

// TestRunGroup_AStopEndsEverythingGosecStarted: closing stop, as the scan
// does on a signal, ends gosec, its child and its grandchild.
func TestRunGroup_AStopEndsEverythingGosecStarted(t *testing.T) {
	pids := pidFile(t)
	cmd := standInCmd("gosec-wait", pids)
	stop := make(chan struct{})
	result := make(chan error, 1)
	var warn strings.Builder
	go func() { result <- runGroup(cmd, stop, nil, &warn) }()
	noted := waitPids(t, pids)
	requireOwnGroups(t, noted)
	close(stop)
	if err := <-result; !errors.Is(err, errStopped) {
		t.Errorf("runGroup = %v, want errStopped", err)
	}
	requireAllEnded(t, noted)
	if warn.Len() != 0 {
		t.Errorf("warnings: %s", warn.String())
	}
}

// TestRunGroup_ATimeLimitEndsEverythingGosecStarted: the time limit firing,
// once the stand-ins are all running, ends them all, and runGroup says the
// time limit stopped it.
func TestRunGroup_ATimeLimitEndsEverythingGosecStarted(t *testing.T) {
	pids := pidFile(t)
	cmd := standInCmd("gosec-wait", pids)
	expired := make(chan time.Time)
	result := make(chan error, 1)
	go func() { result <- runGroup(cmd, nil, expired, &strings.Builder{}) }()
	noted := waitPids(t, pids)
	close(expired)
	if err := <-result; !errors.Is(err, errTimeLimit) {
		t.Errorf("runGroup = %v, want errTimeLimit", err)
	}
	requireAllEnded(t, noted)
}

// TestRunGroup_AnExitedGosecDoesNotHangOnItsOutput: the stand-in exits and
// leaves its child holding the output open, as no real gosec does; runGroup
// returns with no error and the report the stand-in printed.
func TestRunGroup_AnExitedGosecDoesNotHangOnItsOutput(t *testing.T) {
	pids := pidFile(t)
	cmd := standInCmd("gosec-exit", pids)
	var out strings.Builder
	cmd.Stdout = &out
	if err := runGroup(cmd, nil, nil, &strings.Builder{}); err != nil {
		t.Fatalf("runGroup = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "{}") {
		t.Errorf("output = %q, want the stand-in's report", out.String())
	}
}

// TestDescendants_FollowsEveryGenerationAndSkipsOwnGroup: every process under
// the root is found, however deep, and so is each group they lead, but never
// gosec-check's own group, which a signal would end gosec-check with.
func TestDescendants_FollowsEveryGenerationAndSkipsOwnGroup(t *testing.T) {
	if _, err := parseProcesses([]byte("  1 0 1 Mon Jan 1 00:00:00 2026\n  a b c Mon Jan 1 00:00:00 2026\n")); err == nil {
		t.Fatal("a line whose numbers are words was read, want it refused")
	}
	// A header or blank line, which is short of four fields, is passed over.
	procs, err := parseProcesses([]byte("PID PPID\n\n  1 0 1 t0\n 10 1 7 t1\n 20 10 7 t2\n 30 20 30 t3\n 31 30 30 Sat Nov 21 21:52:01 2026\n 40 1 40 t5\n"))
	if err != nil {
		t.Fatal(err)
	}
	members, groups := descendants(procs, 10, 7)
	var got []string
	for _, m := range members {
		got = append(got, strconv.Itoa(m.pid))
	}
	if strings.Join(got, " ") != "20 30 31" {
		t.Errorf("descendants = %v, want 20 30 31", got)
	}
	if members[2].start != "Sat Nov 21 21:52:01 2026" {
		t.Errorf("start = %q, want the rest of the line", members[2].start)
	}
	if len(groups) != 1 || groups[0] != 30 {
		t.Errorf("groups = %v, want only 30, never gosec-check's own 7", groups)
	}
}

// TestProcTree_StillIsTheSameProcessOnly: a member is still running only when
// its pid is listed with the start time it had, so a pid given to a later
// process is never killed.
func TestProcTree_StillIsTheSameProcessOnly(t *testing.T) {
	tree := procTree{members: []proc{{pid: 20, pgid: 30, start: "t2"}, {pid: 31, pgid: 30, start: "t3"}}, groups: []int{30}}
	now := []proc{{pid: 20, pgid: 30, start: "t2"}, {pid: 31, pgid: 99, start: "later"}}
	alive := tree.still(now)
	if len(alive) != 1 || alive[0].pid != 20 {
		t.Errorf("still = %+v, want only pid 20, whose start time is unchanged", alive)
	}
}

//go:build !windows

// This file tests that stopping gosec-check stops everything gosec started,
// with the test binary standing in for gosec. Like gosec's `go list`, the
// stand-in's child leads a process group of its own, which killing the
// stand-in or its group would not reach. The child is gone after an interrupt
// and after the time limit, and a child left holding the output after the
// script exits does not hang the run.
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

// standInEnv, set to "exit" or "wait", makes the test binary a stand-in for
// gosec (TestMain): it starts a long sleep in a process group of its own, as
// gosec's go/packages starts `go list` (Setpgid), with its own output as the
// sleep's, writes the sleep's pid to the file standInPidEnv names, prints an
// empty report, and then exits or waits.
const (
	standInEnv    = "GOSEC_CHECK_TEST_STAND_IN"
	standInPidEnv = "GOSEC_CHECK_TEST_STAND_IN_PID"
)

// TestMain runs the stand-in when standInEnv asks for it, and the tests
// otherwise.
func TestMain(m *testing.M) {
	if mode := os.Getenv(standInEnv); mode != "" {
		os.Exit(standIn(mode, os.Getenv(standInPidEnv)))
	}
	os.Exit(m.Run())
}

// standIn is the stand-in for gosec, returning its exit status.
func standIn(mode, pidFile string) int {
	child := exec.Command("sleep", "300")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Stdout = os.Stdout
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println("{}")
	if mode == "wait" {
		time.Sleep(300 * time.Second)
	}
	return 0
}

// startStandIn returns a command running the stand-in in mode, and the path
// of the file it writes its child's pid to.
func startStandIn(t *testing.T, mode string) (*exec.Cmd, string) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), standInEnv+"="+mode, standInPidEnv+"="+pidFile)
	return cmd, pidFile
}

// readPid waits for the stand-in to write its child's pid and returns it.
func readPid(t *testing.T, path string) int {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(string(data), "\n") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatalf("pid file holds %q", data)
		}
		return pid
	}
	t.Fatalf("the stand-in never wrote %s", path)
	return 0
}

// alive reports whether pid still exists, a zombie aside: a killed process
// lasts until whoever adopted it reaps it, so this waits up to a few seconds
// for it to go.
func alive(pid int) bool {
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return false
		}
	}
	return true
}

// requireOwnGroup fails t unless pid leads a process group of its own, as
// `go list` does, so the test shows what killing gosec's group would miss.
func requireOwnGroup(t *testing.T, pid int) {
	t.Helper()
	pgid, err := syscall.Getpgid(pid)
	if err != nil || pgid != pid {
		t.Fatalf("the stand-in's child %d is in group %d (%v), want a group of its own", pid, pgid, err)
	}
}

// TestRunGroup_AnInterruptStopsEverythingGosecStarted: closing stop ends the
// stand-in and its child in its own group, and runGroup says it was stopped.
func TestRunGroup_AnInterruptStopsEverythingGosecStarted(t *testing.T) {
	cmd, pidFile := startStandIn(t, "wait")
	// An unbuffered send is taken only by runGroup's select.
	stop := make(chan struct{})
	result := make(chan error, 1)
	var warn strings.Builder
	go func() { result <- runGroup(cmd, stop, time.Time{}, &warn) }()
	child := readPid(t, pidFile)
	t.Cleanup(func() {
		close(stop)
		_ = syscall.Kill(child, syscall.SIGKILL)
	})
	requireOwnGroup(t, child)
	stop <- struct{}{}
	select {
	case err := <-result:
		if !errors.Is(err, errStopped) {
			t.Errorf("runGroup = %v, want errStopped", err)
		}
	case <-time.After(stopGrace + 10*time.Second):
		t.Fatal("runGroup did not return after stop was closed")
	}
	if alive(child) {
		t.Errorf("process %d that the stand-in started is still running", child)
	}
	if warn.Len() != 0 {
		t.Errorf("warnings: %s", warn.String())
	}
}

// TestRunGroup_ATimeLimitStopsEverythingGosecStarted: a deadline that passes
// ends the stand-in and its child, and runGroup says the time limit stopped
// it.
func TestRunGroup_ATimeLimitStopsEverythingGosecStarted(t *testing.T) {
	cmd, pidFile := startStandIn(t, "wait")
	result := make(chan error, 1)
	go func() { result <- runGroup(cmd, nil, time.Now().Add(2*time.Second), &strings.Builder{}) }()
	child := readPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err := <-result; !errors.Is(err, errTimeLimit) {
		t.Errorf("runGroup = %v, want errTimeLimit", err)
	}
	if alive(child) {
		t.Errorf("process %d that the stand-in started is still running", child)
	}
}

// TestRunGroup_AnExitedGosecDoesNotHangOnItsOutput: the stand-in exits and
// leaves its child holding the output open, as no real gosec does; runGroup returns soon after, with
// no error and the report the stand-in printed.
func TestRunGroup_AnExitedGosecDoesNotHangOnItsOutput(t *testing.T) {
	cmd, pidFile := startStandIn(t, "exit")
	var out strings.Builder
	cmd.Stdout = &out
	start := time.Now()
	err := runGroup(cmd, nil, time.Time{}, &strings.Builder{})
	_ = syscall.Kill(readPid(t, pidFile), syscall.SIGKILL)
	if err != nil {
		t.Fatalf("runGroup = %v, want nil", err)
	}
	if took := time.Since(start); took > pipeGrace+5*time.Second {
		t.Errorf("runGroup took %s, want about %s at most", took, pipeGrace)
	}
	if !strings.Contains(out.String(), "{}") {
		t.Errorf("output = %q, want the stand-in's report", out.String())
	}
}

// TestDescendants_FollowsEveryGenerationAndSkipsOwnGroup: every process under
// the root is found, however deep, and so is each group they lead, but never
// gosec-check's own group, which a signal would end gosec-check with.
func TestDescendants_FollowsEveryGenerationAndSkipsOwnGroup(t *testing.T) {
	if _, err := parseProcesses([]byte("  1 0 1\n  a b c\n")); err == nil {
		t.Fatal("a line of three words that are not numbers was read, want it refused")
	}
	// A header or blank line, which is not three fields, is passed over.
	procs, err := parseProcesses([]byte("PID PPID\n\n  1 0 1\n 10 1 7\n 20 10 7\n 30 20 30\n 31 30 30\n 40 1 40\n"))
	if err != nil {
		t.Fatal(err)
	}
	pids, groups := descendants(procs, 10, 7)
	if got := fmtInts(pids); got != "20 30 31" {
		t.Errorf("descendants = %s, want 20 30 31", got)
	}
	if len(groups) != 1 || groups[0] != 30 {
		t.Errorf("groups = %v, want only 30, never gosec-check's own 7", groups)
	}
}

// fmtInts writes ns separated by spaces.
func fmtInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

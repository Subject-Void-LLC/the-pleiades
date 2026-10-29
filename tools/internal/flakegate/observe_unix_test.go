//go:build unix

// Tests for a signal to an observed run's process group, which only a
// system with process groups has; on Windows SignalGroup is a taskkill of
// the tree, which the live Windows leg exercises.
package flakegate

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSignalGroup_EndsWhatTheRunStarted: a command started in its own group
// and a child it started both end on one signal to the group, and a command
// that has not started is an error rather than a signal to nothing.
func TestSignalGroup_EndsWhatTheRunStarted(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $!; wait")
	ownGroup(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	child := strings.TrimSpace(string(buf[:n]))
	if child == "" {
		t.Fatal("the shell printed no child pid")
	}
	if err := SignalGroup(cmd, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the shell did not end on the group's SIGTERM")
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(atoi(t, child), 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the shell's child %s is still running: the signal did not reach the group", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := SignalGroup(exec.Command("sh"), syscall.SIGTERM); err == nil {
		t.Error("a command that never started was signalled")
	}
}

// atoi reads a pid a shell printed.
func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a pid: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

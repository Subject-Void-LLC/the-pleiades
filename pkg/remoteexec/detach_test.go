//go:build !windows

// Tests for detached commands (detach.go) against remoteexectest's real
// in-process SSH server, whose commands run through a real /bin/sh: the
// evidence is what the shell left behind (files, live processes), not the
// client's account of it.
package remoteexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// detachedConn starts a server and connects to it with HOME set to a fresh
// directory, returning the connection, the server and that HOME.
func detachedConn(t *testing.T) (*Conn, func() int64, func(), string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv := startPoolServer(t)
	r := New(Options{InsecureSkipHostKeyVerify: true})
	auth := PasswordAuth(srv.Username, srv.Password)
	conn, err := r.Connect(context.Background(), nil, target(srv), auth)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, srv.Logins, srv.DropConnections, home
}

// jobsLeft lists the job directories left under home.
func jobsLeft(t *testing.T, home string) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(home, ".pleiades-async-*", "job.*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

func TestDetached_ReturnsTheCommandsOutputAndExitCodeAndCleansUp(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	res, err := conn.Run(WithDetach(context.Background()), "echo out; echo err >&2; exit 3")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "out\n" || res.Stderr != "err\n" || res.ExitCode != 3 {
		t.Fatalf("Run = %+v; want stdout out, stderr err, exit 3", res)
	}
	if bases, _ := filepath.Glob(filepath.Join(home, ".pleiades-async-*")); len(bases) != 1 {
		t.Fatalf("the command did not run detached (job bases %v)", bases)
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Fatalf("job directories left behind: %v", left)
	}
}

func TestDetached_OutlivesADroppedConnection(t *testing.T) {
	conn, logins, drop, home := detachedConn(t)
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := conn.Run(WithDetach(context.Background()), "sleep 2; echo finished")
		done <- out{res, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(jobsLeft(t, home)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the detached job never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	drop()
	got := <-done
	if got.err != nil || got.res.Stdout != "finished\n" || got.res.ExitCode != 0 {
		t.Fatalf("Run after a drop = %+v, %v; want finished, exit 0", got.res, got.err)
	}
	if n := logins(); n < 2 {
		t.Fatalf("logins = %d; the polls never reconnected", n)
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Fatalf("job directories left behind: %v", left)
	}
}

func TestDetached_TheLimitStopsEverythingTheJobStarted(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	ctx, cancel := context.WithTimeout(WithDetach(context.Background()), 1500*time.Millisecond)
	defer cancel()
	// The job's child, sleep, is what a stop of the job's shell alone would
	// leave running.
	_, err := conn.Run(ctx, `sleep 3 & echo $! >"$HOME/child"; wait; touch "$HOME/after"`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run past its limit = %v; want context.DeadlineExceeded", err)
	}
	raw, rerr := os.ReadFile(filepath.Join(home, "child"))
	if rerr != nil {
		t.Fatalf("the job never started its child: %v", rerr)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	time.Sleep(3 * time.Second) // past when the job would have finished
	if _, err := os.Stat(filepath.Join(home, "after")); err == nil {
		t.Fatal("the job ran on past its limit")
	}
	if processAlive(child) {
		t.Fatalf("the job's child %d is still running", child)
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Fatalf("job directories left behind: %v", left)
	}
}

func TestDetached_OnlyUnderWithDetach(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	if _, err := conn.Run(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	if bases, _ := filepath.Glob(filepath.Join(home, ".pleiades-async-*")); len(bases) != 0 {
		t.Fatalf("a plain Run ran detached (job bases %v)", bases)
	}
}

// processAlive reports whether pid is a live process (a zombie counts as gone).
func processAlive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	fields := strings.Fields(string(stat))
	return len(fields) < 3 || fields[2] != "Z"
}

func TestDetached_ADeviceThatCannotDetachRunsTheCommandWhereItStands(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	// A HOME that cannot hold the job directory makes the start script stop
	// before starting anything, as a device with no POSIX sh does.
	if err := os.WriteFile(filepath.Join(home, ".pleiades-async-"+strconv.Itoa(os.Getuid())), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := conn.Run(WithDetach(context.Background()), "echo once >>\"$HOME/count\"; echo ran")
	if err != nil || res.Stdout != "ran\n" {
		t.Fatalf("Run = %+v, %v; want ran", res, err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "count"))
	if string(raw) != "once\n" {
		t.Fatalf("the command ran %q times' worth; want exactly once", raw)
	}
}

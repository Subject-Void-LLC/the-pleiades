//go:build !windows

// Tests for detached commands (detach.go) against remoteexectest's real
// in-process SSH server, whose commands run through a real /bin/sh: the
// evidence is what the shell left behind (files, live processes), not the
// client's account of it.
package remoteexec

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// detachedConn starts a server and connects to it with HOME set to a fresh
// directory, returning the connection, the server's login count, a function
// dropping its connections, and that HOME.
func detachedConn(t *testing.T) (*Conn, func() int64, func(), string) {
	t.Helper()
	conn, srv, home := detachedServer(t)
	return conn, srv.Logins, srv.DropConnections, home
}

// detachedServer is detachedConn returning the server itself.
func detachedServer(t *testing.T) (*Conn, *remoteexectest.Server, string) {
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
	return conn, srv, home
}

// waitForPoll waits until srv has run a detached job's first poll, which
// comes only once the start command has answered: a drop before then would
// cut the start itself, which is a different case from a dropped poll.
func waitForPoll(t *testing.T, srv *remoteexectest.Server) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		for _, cmd := range srv.Commands() {
			if strings.Contains(cmd, rcPrefix) && strings.Contains(cmd, "/rc") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the detached job was never polled")
		}
	}
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
	conn, srv, home := detachedServer(t)
	logins, drop := srv.Logins, srv.DropConnections
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := conn.Run(WithDetach(context.Background()), "sleep 2; echo finished")
		done <- out{res, err}
	}()
	waitForPoll(t, srv)
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
	if Detached(context.Background()) || !Detached(WithDetach(context.Background())) || Detached(withoutDetach(WithDetach(context.Background()))) {
		t.Error("Detached does not report WithDetach's mark")
	}
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

func TestDetached_TheStopReconnectsWhenItsConnectionIsGone(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	job := `sleep 30 & echo $! >"$HOME/child"; wait`
	started, err := conn.run(context.Background(), QuoteCommand([]string{"sh", "-c", detachStart, "start", "", strconv.Itoa(len(job)), newJobName()}), strings.NewReader(job), 4096)
	if err != nil || started.ExitCode != 0 {
		t.Fatalf("start: %+v, %v", started, err)
	}
	dir := lastLine(started.Stdout)
	deadline := time.Now().Add(10 * time.Second)
	var raw []byte
	for len(raw) == 0 && time.Now().Before(deadline) {
		raw, _ = os.ReadFile(filepath.Join(home, "child"))
		time.Sleep(20 * time.Millisecond)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if child <= 0 {
		t.Fatalf("the job never recorded its child's pid (%q), so nothing could show the stop reached it", raw)
	}
	// What a Runner's child sees at the limit: the signal that tells it to
	// stop has already closed the connection the job was started on.
	_ = conn.client.Close()
	if got := conn.stopDetached(QuoteArg(dir)); got != stopSignalled {
		t.Errorf("the stop of a running job reported %q; want %q", got, stopSignalled)
	}
	if processAlive(child) {
		t.Errorf("the job's child %d is still running after the stop", child)
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Errorf("job directories left behind: %v", left)
	}
}

func TestDetached_FailuresAreNamedAndNeverRunTwice(t *testing.T) {
	// A connection already closed: the start itself fails.
	conn, _, _, _ := detachedConn(t)
	_ = conn.client.Close()
	if _, err := conn.Run(WithDetach(context.Background()), "true"); err == nil {
		t.Error("a detached run on a closed connection succeeded")
	}

	// A start that answers with something that is not a job directory (here
	// a HOME holding a newline, so the last line is a relative path) is
	// refused, not polled, and not run a second time.
	conn, _, _, home := detachedConn(t)
	broken := filepath.Join(home, "line\nbreak")
	if err := os.Mkdir(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", broken)
	t.Chdir(home)
	if _, err := conn.Run(WithDetach(context.Background()), "echo once >> count"); err == nil || !strings.Contains(err.Error(), "no job directory") {
		t.Errorf("a start with no job directory: %v", err)
	}
	// The job may have started before it was stopped by name; it never runs
	// a second time.
	time.Sleep(500 * time.Millisecond)
	if raw, _ := os.ReadFile(filepath.Join(home, "count")); len(raw) > len("once\n") {
		t.Errorf("the command ran %q times' worth; want at most once", raw)
	}
}

func TestDetached_ALostConnectionThatCannotBeRedialedIsNamed(t *testing.T) {
	conn, srv, _ := detachedServer(t)
	drop := srv.DropConnections
	conn.redial = func(context.Context) (*Conn, error) { return nil, errors.New("the device is gone") }
	done := make(chan error, 1)
	go func() {
		_, err := conn.Run(WithDetach(context.Background()), "sleep 2")
		done <- err
	}()
	waitForPoll(t, srv)
	drop()
	// No connection is left to stop it on, so the error says the job may
	// still run, and where.
	if err := <-done; err == nil || !strings.Contains(err.Error(), "may still be running") {
		t.Errorf("a lost connection: %v, want one naming the job that may still run", err)
	}
}

func TestDetached_ReconnectRefusesWhatIsNotADroppedConnection(t *testing.T) {
	conn, _, _, _ := detachedConn(t)
	for name, err := range map[string]error{"an exit status": &ssh.ExitError{}, "output too large": ErrOutputTooLarge} {
		if conn.reconnect(context.Background(), err) {
			t.Errorf("reconnect on %s redialed", name)
		}
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if conn.reconnect(ended, io.EOF) {
		t.Error("reconnect redialed under an ended context")
	}
	conn.redial = func(context.Context) (*Conn, error) { return nil, errors.New("refused") }
	if conn.reconnect(context.Background(), io.EOF) {
		t.Error("reconnect reported a redial that failed")
	}
	// A stop that can neither run nor redial ends quietly.
	_ = conn.client.Close()
	conn.stopDetached(QuoteArg("/nowhere/.pleiades-async-0/job.x"))
}

func TestDetached_TheJobKeepsTheCallersUmaskAndRunsUnderSh(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	// The start script's own umask 077 is for the job's files; the command
	// creates its own under the umask a plain run would give it.
	if _, err := conn.Run(context.Background(), `umask >"$HOME/plain-umask"`); err != nil {
		t.Fatal(err)
	}
	// A login shell that leaves a mark, then runs the command as sh would.
	loginShell := filepath.Join(home, "login-shell")
	if err := os.WriteFile(loginShell, []byte("#!/bin/sh\n: >\"$HOME/login-shell-ran\"\nexec /bin/sh \"$@\"\n"), 0o700); err != nil { // #nosec G306 -- the test's own executable
		t.Fatal(err)
	}
	t.Setenv("SHELL", loginShell)
	res, err := conn.Run(WithDetach(context.Background()), `umask`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	plain, _ := os.ReadFile(filepath.Join(home, "plain-umask"))
	got := strings.TrimSpace(res.Stdout)
	if got != strings.TrimSpace(string(plain)) {
		t.Fatalf("the detached job's umask is %q; a plain run's is %q", got, plain)
	}
	if got == "0077" || got == "077" {
		t.Fatal("the job ran under the start script's 077, not the caller's umask")
	}
	// sh runs it, as sh runs a plain command (script.go), never $SHELL.
	if _, err := os.Stat(filepath.Join(home, "login-shell-ran")); err == nil {
		t.Fatal("the job ran under the login shell ($SHELL), where a plain run's command runs under sh")
	}
	// A Raw connection's plain commands are the login shell's, and so is its
	// detached job.
	if _, err := conn.Raw().Run(WithDetach(context.Background()), `umask`); err != nil {
		t.Fatalf("Raw Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "login-shell-ran")); err != nil {
		t.Fatal("a Raw connection's job did not run under the login shell ($SHELL), as its plain commands do")
	}
}

func TestDetached_TheCommandIsNeverAnArgument(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	const marker = "pleiades-argv-marker-7f3a"
	done := make(chan error, 1)
	go func() {
		_, err := conn.Run(WithDetach(context.Background()), "sleep 2; : "+marker)
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); len(jobsLeft(t, home)) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the detached job never started")
		}
	}
	time.Sleep(200 * time.Millisecond)
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, p := range procs {
		raw, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(raw), marker) {
			t.Errorf("the command shows in %s: %q", p, strings.ReplaceAll(string(raw), "\x00", " "))
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestDetached_ALoginBannerBeforeTheDirectoryIsSkipped(t *testing.T) {
	if got := lastLine("Welcome to the device\n/home/u/.pleiades-async-1/job.ab12\n"); got != "/home/u/.pleiades-async-1/job.ab12" {
		t.Errorf("lastLine = %q", got)
	}
	// A banner line starting with "/" joined to the directory by a newline is
	// not one job directory.
	if jobDir.MatchString("/banner\n/home/u/.pleiades-async-1/job.ab12") {
		t.Error("jobDir matched across a newline")
	}
}

// whereItRan prints whether it ran detached or attached. A detached job is
// sh reading its job's cmd file, so its $0 ends in /cmd; an attached command
// is read by sh -c, whose $0 is sh. It does not ask whether standard input is
// a pipe: FreeBSD's /dev/stdin is not one without fdescfs mounted, so that
// check read every command as detached there.
const whereItRan = `case $0 in */cmd) echo detached;; *) echo attached;; esac`

func TestDetached_AHomeTheAccountDoesNotOwnRunsTheCommandWhereItStands(t *testing.T) {
	conn, _, _, _ := detachedConn(t)
	// A HOME owned by someone else is what become looks like under a sudo
	// that keeps the login's HOME. /tmp (owned by root, yet writable, so the
	// old script would have used it) stands in for it when the test does not
	// run as root.
	info, err := os.Stat("/tmp")
	if os.Getuid() == 0 || err != nil || info.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) {
		t.Skip("needs a writable /tmp the test's account does not own")
	}
	t.Setenv("HOME", "/tmp")
	res, err := conn.Run(WithDetach(context.Background()), whereItRan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "attached" {
		t.Fatalf("the command ran %s under a HOME the account does not own; want attached", strings.TrimSpace(res.Stdout))
	}
}

func TestDetached_ALinkedBaseIsRefused(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".pleiades-async-"+strconv.Itoa(os.Getuid()))); err != nil {
		t.Fatal(err)
	}
	// A finished job removes its directory, so whether one was made through
	// the link is read from the command itself.
	res, err := conn.Run(WithDetach(context.Background()), whereItRan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "attached" {
		t.Fatalf("the command ran %s through a linked base; want attached", strings.TrimSpace(res.Stdout))
	}
}

// TestDetached_WhereItRanTellsTheTwoApart is the control for whereItRan: it
// reads detached for a detached run and attached for a plain one, so the
// two tests reading attached from it cannot pass on a check that always
// says so.
func TestDetached_WhereItRanTellsTheTwoApart(t *testing.T) {
	conn, _, _, _ := detachedConn(t)
	for want, ctx := range map[string]context.Context{"detached": WithDetach(context.Background()), "attached": context.Background()} {
		res, err := conn.Run(ctx, whereItRan)
		if err != nil || strings.TrimSpace(res.Stdout) != want {
			t.Errorf("whereItRan = %q, %v; want %s", res.Stdout, err, want)
		}
	}
}

// TestDetached_OutputPastTheLimitLeavesNoJob: a finished job whose output
// reads past the limit fails with ErrOutputTooLarge, and its directory,
// which holds its command, is still removed.
func TestDetached_OutputPastTheLimitLeavesNoJob(t *testing.T) {
	conn, _, _, home := detachedConn(t)
	_, err := conn.run(WithDetach(context.Background()), `head -c 1000 /dev/zero | tr '\0' x`, nil, 100)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("a detached run past its limit: %v; want ErrOutputTooLarge", err)
	}
	if left := jobsLeft(t, home); len(left) != 0 {
		t.Errorf("job directories left behind: %v", left)
	}
}

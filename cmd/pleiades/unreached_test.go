// End-to-end tests for a run report's unreached mark, through the built
// binary: a task whose device never answered says so, over SSH and over
// WinRM, and a device that answered with a refusal does not.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// listenAndHold accepts every connection on a local port and, with hang
// false, closes it at once (an SSH handshake then ends at EOF), or with
// hang true holds it open saying nothing (a WinRM request then waits out
// its bound). The listener stays open until the test ends, so the port is
// never one that nothing listens on.
func listenAndHold(t *testing.T, hang bool) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			if !hang {
				_ = c.Close()
				continue
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	return "127.0.0.1", l.Addr().(*net.TCPAddr).Port
}

// unreachedProject is a project holding one device of kind at host:port
// with a password credential.
func unreachedProject(t *testing.T, kind, host string, port int, password string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "dev1", "--type", kind, "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "dev1", "--username", releaseGateSSHUser, "--password", password},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// emptyKnownHostsHome is a home directory of the test's own whose
// known_hosts exists and is empty, so an SSH dial goes as far as the
// handshake whatever the machine running the test holds: with no file at
// all the dial is refused before it starts (host key verification
// unavailable), which is a local mistake, not a silent device, and a test
// reading the runner's own home passed here and failed on CI's.
func emptyKnownHostsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestCLI_ADeviceThatNeverAnsweredIsUnreached: over SSH a connection the
// device closes before its handshake, and over WinRM one it accepts and
// never answers, each fail the task marked unreached, so a caller knows
// nothing ran and running it again is safe.
func TestCLI_ADeviceThatNeverAnsweredIsUnreached(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		hang       bool
		call       []string
	}{
		{"ssh", "linux_server", false, []string{"exec.command", "cmd=hostname"}},
		{"winrm", "windows_server", true, []string{"exec.winrm.shell", "shell=powershell", "command=hostname", "timeout:=2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, port := listenAndHold(t, tc.hang)
			dir := unreachedProject(t, tc.kind, host, port, "pw")
			rep, raw, code := runPleiadesJSONWithHome(t, dir, emptyKnownHostsHome(t), append([]string{"adhoc", "dev1"}, tc.call...)...)
			if code == 0 || len(rep.Tasks) != 1 {
				t.Fatalf("exit %d, report:\n%s", code, raw)
			}
			if task := rep.Tasks[0]; task.Status != "failed" || !task.Unreached {
				t.Errorf("the task reads %+v, want failed and unreached", task)
			}
		})
	}
}

// TestCLI_ARefusedLoginIsNotUnreached: a real sshd that refuses the
// password answered, so the failed task is not marked unreached: running it
// again would present the same password, one more failed login toward the
// account's lockout.
func TestCLI_ARefusedLoginIsNotUnreached(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := unreachedProject(t, "linux_server", host, port, "not-the-password")
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "dev1", "exec.command", "cmd=hostname")
	if code == 0 || len(rep.Tasks) != 1 {
		t.Fatalf("exit %d, report:\n%s", code, raw)
	}
	if task := rep.Tasks[0]; task.Status != "failed" || task.Unreached {
		t.Errorf("the task reads %+v, want failed and not unreached", task)
	}
}

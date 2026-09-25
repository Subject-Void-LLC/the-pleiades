// Release gate for connection persistence: the real pleiades binary runs
// real tasks against a real OpenSSH server, and the server's own log, not
// anything pleiades reports, says how many times it logged in.
package main_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sshdLog is where the pinned openssh-server image's sshd logs.
const sshdLog = "/config/logs/openssh/current"

// sshdLogins counts the successful password logins sshd has logged.
func sshdLogins(t *testing.T, c testcontainers.Container) int {
	t.Helper()
	code, r, err := c.Exec(context.Background(), []string{"cat", sshdLog})
	if err != nil || code != 0 {
		t.Fatalf("reading %s: exit %d, %v", sshdLog, code, err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(out), "Accepted password for "+releaseGateSSHUser)
}

// loginsDuring runs pleiades with args and returns how many logins the
// server logged while it ran, waiting for the log to catch up and then
// for it to stay still, so a late line is counted rather than missed.
func loginsDuring(t *testing.T, c testcontainers.Container, dir, home string, args ...string) int {
	t.Helper()
	before := sshdLogins(t, c)
	if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	last, still := -1, 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && still < 3; {
		time.Sleep(200 * time.Millisecond)
		n := sshdLogins(t, c)
		if n == last {
			still++
		} else {
			last, still = n, 0
		}
	}
	return last - before
}

// persistRunbook is n exec.shell tasks against host, each appending one
// line to a file on the device, with a connection reset after the task
// numbered resetAfter (none when 0).
func persistRunbook(host string, n, resetAfter int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id: persist-gate\nhosts: %s\ntasks:\n", host)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "  - name: append %d\n    fqcn: exec.shell\n    params:\n      cmd: echo %d >> /tmp/persist-gate\n", i, i)
		if i == resetAfter {
			b.WriteString("  - name: log in again\n    fqcn: pleiades.builtin.connection.reset\n")
		}
	}
	return b.String()
}

// TestCLI_RunPersistsConnections is the release gate. Ten tasks log in
// once by default; ten times with --persist-connections=false; ten times
// for a device whose own persist_connections is false, even with the
// flag on, since off at either ladder wins; and twice with one reset
// between them. The tasks' own writes are then read back on the device,
// so a run that logged in less by doing less would fail.
func TestCLI_RunPersistsConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the connection persistence release gate, which runs a real sshd, in short mode")
	}
	c, host, port := startReleaseGateSSHD(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "web", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-host", "web-fresh", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port), "--set", "persist_connections=false"},
		{"add-credential", "web", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"add-credential", "web-fresh", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, dir, "runbooks/ten.yaml", persistRunbook("web", 10, 0))
	writeFile(t, dir, "runbooks/ten-fresh.yaml", persistRunbook("web-fresh", 10, 0))
	writeFile(t, dir, "runbooks/reset.yaml", persistRunbook("web", 10, 5))

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"on by default", []string{"run", "runbooks/ten.yaml"}, 1},
		{"off for the run", []string{"run", "runbooks/ten.yaml", "--persist-connections=false"}, 10},
		{"off for the device", []string{"run", "runbooks/ten-fresh.yaml"}, 10},
		{"the device's off wins over the run's on", []string{"run", "runbooks/ten-fresh.yaml", "--persist-connections=true"}, 10},
		{"a reset logs in again", []string{"run", "runbooks/reset.yaml"}, 2},
	}
	for _, tc := range cases {
		if got := loginsDuring(t, c, dir, home, tc.args...); got != tc.want {
			t.Errorf("%s: %d logins, want %d", tc.name, got, tc.want)
		}
	}

	lines := strings.Fields(verifyOverSSH(t, addr, "cat /tmp/persist-gate"))
	if len(lines) != 10*len(cases) {
		t.Fatalf("the device holds %d appended lines, want %d: a task did not run", len(lines), 10*len(cases))
	}
}

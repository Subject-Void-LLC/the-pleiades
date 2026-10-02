// A throwaway device built from a Dockerfile under testdata, reached over
// SSH as root, and the project that manages it, set up the way a user sets
// one up: init, add-host with a classification, add-credential through
// --password-stdin, and a known_hosts entry recorded from a first login.
//
// The capability reach gate built this inline for its Debian device; the
// check gates need the same for Debian and for a Rocky Linux device, so it
// lives here once rather than as three copies.
package main_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// testdataDevice is a running device container and how a test reaches it.
type testdataDevice struct {
	container testcontainers.Container
	host      string
	port      int
	password  string
}

// startTestdataDevice builds testdata/<dir> and starts it, with sshd on
// 22 accepting root with rootPassword. hostConfig, when set, adjusts the
// container, as a systemd device's writable cgroup tree needs.
func startTestdataDevice(t *testing.T, dir, rootPassword string, hostConfig func(*container.HostConfig)) *testdataDevice {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile:     testcontainers.FromDockerfile{Context: filepath.Join("testdata", dir)},
			ExposedPorts:       []string{"22/tcp"},
			HostConfigModifier: hostConfig,
			WaitingFor:         wait.ForListeningPort("22/tcp").WithStartupTimeout(testsupport.SSHDStartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c) // a failed start still returns its container
		t.Fatalf("starting the %s device: %v", dir, err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return &testdataDevice{container: c, host: host, port: int(mapped.Num()), password: rootPassword}
}

// addr is the device's SSH address from the test's side.
func (d *testdataDevice) addr() string {
	return net.JoinHostPort(d.host, strconv.Itoa(d.port))
}

// manage makes a project managing the device under name, classified as
// classify, with any extra add-host --set values, and a home whose
// known_hosts holds the key the device presented at a first login as
// root. It returns the project directory and that home.
func (d *testdataDevice) manage(t *testing.T, name, classify string, sets ...string) (dir, home string) {
	t.Helper()
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := testsupport.CaptureHostKey(t, d.addr(), "root", d.password)
	line := knownhosts.Line([]string{d.addr()}, key)
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir = t.TempDir()
	addHost := []string{"add-host", name, "--classify", classify, "--set", "host=" + d.host, "--set", "port=" + strconv.Itoa(d.port)}
	for _, s := range sets {
		addHost = append(addHost, "--set", s)
	}
	for _, args := range [][]string{{"init"}, addHost} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, d.password+"\n", "add-credential", name, "--username", "root", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	return dir, home
}

// exec runs argv inside the container as root, beside the product rather
// than through it, so what it reads back is independent of what the
// method under test reported. It returns the trimmed output and exit code.
func (d *testdataDevice) exec(t *testing.T, argv ...string) (string, int) {
	t.Helper()
	code, r, err := d.container.Exec(context.Background(), argv, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("exec %v: %v", argv, err)
	}
	out, _ := io.ReadAll(r)
	return strings.TrimSpace(string(out)), code
}

// deviceSettleTimeout bounds how long a gate waits for a device service a
// container starts after sshd (firewalld under systemd) to answer.
const deviceSettleTimeout = 90 * time.Second

// Release gate for FAILURE_PATTERNS 344: methods whose capability no
// device type used to satisfy run through the real binary against a real
// Debian device.
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

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// debianGatePassword is root's password in the throwaway image
// testdata/debian-sshd builds.
const debianGatePassword = "release-gate-debian-password"

// TestCLI_PackageAndAccountMethodsReachARealDevice classifies a real
// Debian device as linux_server/debian_family and runs identity.group.create
// and pkg.install on it through the real binary. Before the fix both were
// refused before any command was sent ("requires capability
// PosixAccountCapable"), on every real device, whatever the inventory
// said. The group is then read back from the container itself, and the
// already-installed package must report no change.
func TestCLI_PackageAndAccountMethodsReachARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the capability reach release gate, which builds and runs a real Debian sshd, in short mode")
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{Context: filepath.Join("testdata", "debian-sshd")},
			ExposedPorts:   []string{"22/tcp"},
			WaitingFor:     wait.ForListeningPort("22/tcp").WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting the Debian sshd: %v", err)
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
	port := int(mapped.Num())
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, rootHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "debian", "--classify", "linux_server,debian_family", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "debian", "--username", "root", "--password", debianGatePassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, dir, "runbooks/reach.yaml", `id: reach
hosts: debian
tasks:
  - name: make a group
    fqcn: identity.group.create
    params:
      name: pleiadesgate
  - name: the ssh server is installed
    fqcn: pkg.install
    params:
      name: openssh-server
`)
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/reach.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tasks[0] [") || !strings.Contains(out, "]: changed") {
		t.Errorf("the group task did not report changed:\n%s", out)
	}
	if !strings.Contains(out, "tasks[1] [") || strings.Count(out, ": changed") != 1 {
		t.Errorf("want only the group task changed, the installed package unchanged:\n%s", out)
	}

	code, r, err := c.Exec(ctx, []string{"getent", "group", "pleiadesgate"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	if code != 0 || !strings.Contains(string(got), "pleiadesgate:") {
		t.Fatalf("the group is not on the device (exit %d): %q", code, got)
	}
}

// rootHostKey records the key addr presents while logging in as root, as
// ssh-keyscan would, so the run is verified against it.
func rootHostKey(t *testing.T, addr string) ssh.PublicKey {
	t.Helper()
	var key ssh.PublicKey
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{ssh.Password(debianGatePassword)},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = k
			return nil
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("capturing the host key of %s: %v", addr, err)
	}
	_ = client.Close()
	return key
}

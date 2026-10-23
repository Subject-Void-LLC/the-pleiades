// A real Telnet server with a login, for the Telnet back end's tests.
package testsupport

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TelnetdBaseImage is the Alpine release the test Telnet server installs
// BusyBox's telnetd on; the server image itself is built (testdata/telnetd).
const TelnetdBaseImage = "alpine:3.20.10"

// telnetdTag is the local tag the test Telnet server is built under.
const telnetdTag = "pleiades/telnetd:3.20.10"

// The account the test Telnet server logs in, and the prompt its shell
// prints.
const (
	TelnetdUser   = "netops"
	TelnetdPrompt = "lab$"
)

// Telnetd is a running Telnet server.
type Telnetd struct {
	Host     string
	Port     int
	Password string
}

// StartTelnetd builds and starts BusyBox's telnetd behind /bin/login, with
// TelnetdUser's password set to password, and returns it once it serves.
// It is removed when tb ends.
func StartTelnetd(tb testing.TB, password string) *Telnetd {
	tb.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		tb.Skip("docker not found on PATH")
	}
	src := filepath.Join(RepoRoot(tb), "internal", "testsupport", "testdata", "telnetd")
	build := exec.Command("docker", "build", "--build-arg", "BASE="+TelnetdBaseImage, "-t", telnetdTag, src) // #nosec G204 -- fixed arguments and a path in this repository
	if out, err := build.CombinedOutput(); err != nil {
		tb.Fatalf("building %s: %v\n%s", telnetdTag, err, out)
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        telnetdTag,
			ExposedPorts: []string{"23/tcp"},
			Env:          map[string]string{"NETOPS_PASSWORD": password},
			WaitingFor:   wait.ForAll(wait.ForLog("telnetd ready").WithStartupTimeout(ContainerStartupTimeout), wait.ForListeningPort("23/tcp")),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		tb.Fatalf("starting telnetd: %v", err)
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "23/tcp")
	if err != nil {
		tb.Fatal(err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return &Telnetd{Host: host, Port: int(port.Num()), Password: password}
}

// The containerlab API server, for a test of what talks to it. It runs as a
// lab host runs it: privileged, with the host's Docker socket and network
// namespaces, because that is what lets it deploy a lab. That makes the
// container root-equivalent on the machine, so a test that starts it asks for
// the Docker socket first (Require) and removes what it made.
package testsupport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ClabAPIImage is the containerlab API server the tests ask, pinned by digest.
const ClabAPIImage = "ghcr.io/srl-labs/clab-api-server/clab-api-server@sha256:8ac7e88f58e695ee2026a98368b10a24905c5afe412a8043dd2032a23ffeb585"

// ClabAPIUser is the account StartClabServer makes.
const ClabAPIUser = "pleiades"

// ClabNodeImage is the image the test labs' nodes run: a base image the net
// lab already pins, with a shell, ip and ping.
const ClabNodeImage = OVSBaseImage

// StartClabServer starts the pinned API server with one account, ClabAPIUser,
// whose password is made fresh for the run, and returns the server's base URL
// and that password. The server is removed when tb ends. It needs the host's
// Docker socket and skips the test without it.
func StartClabServer(tb testing.TB) (baseURL, password string) {
	tb.Helper()
	_, err := os.Stat("/var/run/docker.sock")
	Require(tb, "docker socket", err == nil, "the containerlab API server deploys labs through the host's /var/run/docker.sock")
	ctx := context.Background()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatal(err)
	}
	password = hex.EncodeToString(raw)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        ClabAPIImage,
			ExposedPorts: []string{"8090/tcp"},
			Env: map[string]string{
				"CLAB_API_USERNAME": ClabAPIUser, "CLAB_API_PASSWORD": password,
				"API_PORT": "8090", "TLS_ENABLE": "false",
			},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Privileged = true
				hc.PidMode = "host"
				hc.Binds = []string{"/var/run/docker.sock:/var/run/docker.sock", "/var/run/netns:/var/run/netns"}
			},
			WaitingFor: wait.ForLog("Starting HTTP server").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		tb.Fatalf("starting the containerlab API server: %v", err)
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
	// The server authenticates against the container's own accounts, so the
	// login is made inside it, as a lab host's image wrapper does.
	script := fmt.Sprintf("groupadd -f clab_api; groupadd -f clab_admins; useradd -m -s /bin/bash -G clab_api,clab_admins %s && echo %s:%s | chpasswd", ClabAPIUser, ClabAPIUser, password)
	if code, _, err := c.Exec(ctx, []string{"sh", "-c", script}); err != nil || code != 0 {
		tb.Fatalf("making the API server's account: exit %d, err %v", code, err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "8090/tcp")
	if err != nil {
		tb.Fatal(err)
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprint(port.Num()))), password
}

// CleanClabLab removes, when tb ends, the containers a lab left on the host
// and the management network containerlab made, whatever the test did: a test
// that fails partway must not leave a lab running. The network is removed only
// when nothing else is on it.
func CleanClabLab(tb testing.TB, lab string) {
	tb.Helper()
	tb.Cleanup(func() {
		out, err := exec.Command("docker", "ps", "-aq", "--filter", "label=containerlab="+lab).Output() // #nosec G204 -- the lab name is the test's own
		if err == nil {
			for _, id := range strings.Fields(string(out)) {
				_ = exec.Command("docker", "rm", "-f", id).Run() // #nosec G204 -- an id docker just listed
			}
		}
		// The network is shared by every lab; a failure (still in use) is the
		// right answer and is ignored.
		_ = exec.Command("docker", "network", "rm", "clab").Run() // #nosec G204 -- fixed arguments
	})
}

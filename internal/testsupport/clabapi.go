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
	// On an engine running on this machine's own kernel the server shares the
	// host's network, as containerlab documents: a lab's deploy makes a Docker
	// bridge and then looks it up by name, which a bridged container's own
	// namespace does not hold. Docker Desktop's engine is in a VM whose network
	// a host-networked container would not publish here, and containerlab
	// deploys from a bridged one there, so it keeps a published port.
	hostNet := !dockerDesktop()
	apiPort := "8090"
	if hostNet {
		apiPort = freeLocalPort(tb)
	}
	req := testcontainers.ContainerRequest{
		Image: ClabAPIImage,
		Env: map[string]string{
			"CLAB_API_USERNAME": ClabAPIUser, "CLAB_API_PASSWORD": password,
			"API_PORT": apiPort, "TLS_ENABLE": "false",
		},
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.Privileged = true
			hc.PidMode = "host"
			hc.Binds = []string{"/var/run/docker.sock:/var/run/docker.sock", "/var/run/netns:/var/run/netns"}
			if hostNet {
				hc.NetworkMode = "host"
			}
		},
		WaitingFor: wait.ForLog("Starting HTTP server").WithStartupTimeout(2 * time.Minute),
	}
	if !hostNet {
		req.ExposedPorts = []string{apiPort + "/tcp"}
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
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
	if hostNet {
		return "http://" + net.JoinHostPort("127.0.0.1", apiPort), password
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

// dockerDesktop reports whether the engine is Docker Desktop's, whose
// containers run in a VM rather than on this machine's kernel.
func dockerDesktop() bool {
	out, err := exec.Command("docker", "info", "--format", "{{.OperatingSystem}}").Output() // #nosec G204 -- fixed arguments
	return err == nil && strings.Contains(string(out), "Docker Desktop")
}

// freeLocalPort returns a TCP port free on 127.0.0.1 now, for a server that
// binds the host's own network.
func freeLocalPort(tb testing.TB) string {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
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

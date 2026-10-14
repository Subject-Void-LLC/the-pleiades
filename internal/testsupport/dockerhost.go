// Starting a real container engine behind a real OpenSSH server for a
// test, in one place, so every suite that reaches a container host does it
// the way a container host is reached in production: an SSH login, then a
// channel to the engine's socket or the engine's own dial-stdio (Phases 119a
// and 119b). StartDockerHost runs Docker in Docker; StartPodmanHost runs
// Podman, rootful as root and rootless as the image's podman user.
package testsupport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The engines the fixtures run, pinned to exact releases like every image
// here. The fixtures are built from them, so CI's image cache
// (tools/testimages) pulls them ahead of the tests.
const (
	DockerHostBaseImage = "docker:29.9.0-dind"
	PodmanHostBaseImage = "quay.io/podman/stable:v5.8.7"
)

// The fixtures' accounts and port. These are throwaway containers started
// fresh for each test, never real devices, so a literal password here is
// not a secret leak (the same reasoning StartSSHD records).
const (
	// DockerHostUser is in the Docker fixture's docker group, as a host's
	// automation account is.
	DockerHostUser = "testuser"
	// DockerHostOutsider is not, so the engine's socket refuses it.
	DockerHostOutsider = "outsider"
	// PodmanHostRootful logs in to the Podman fixture as root, whose Podman
	// runs rootful.
	PodmanHostRootful = "root"
	// PodmanHostRootless logs in as the image's own rootless Podman user.
	PodmanHostRootless = "podman"
	// DockerHostPassword is every fixture account's password.
	DockerHostPassword = "testpass123"
	// DockerHostSocket is where the Docker fixture's engine listens.
	DockerHostSocket = "/var/run/docker.sock"
	// engineHostInnerPort is where sshd listens inside a fixture.
	engineHostInnerPort = 2222
)

// EngineHostOptions configures one fixture.
type EngineHostOptions struct {
	// SSHDConfig is written into sshd_config.d before sshd starts, where
	// its settings win over the image's own. Empty means
	// "AllowTcpForwarding local", the least a channel to the engine's socket
	// needs: OpenSSH applies AllowTcpForwarding to Unix sockets too, and
	// Alpine's default is no.
	SSHDConfig string

	// Env is passed to the fixture's container: for the Docker fixture,
	// DOCKERD_EXTRA_ARGS adds arguments to dockerd (a TLS listener).
	Env map[string]string

	// Files are copied into the fixture before it starts.
	Files []testcontainers.ContainerFile

	// ExposedPorts are published beside sshd's.
	ExposedPorts []string

	// Networks are Docker networks the fixture joins, so its engine can
	// reach another test container (a registry) by its alias there.
	Networks []string
}

// EngineHost is a running fixture and the ways a test reaches it.
type EngineHost struct {
	// Engine is "docker" or "podman".
	Engine string
	// Container is the running container.
	Container testcontainers.Container
	// Host and Port are the address a test dials sshd at from outside.
	Host string
	Port int
}

// engineFixture is how one engine's fixture is built and known to be
// ready.
type engineFixture struct {
	engine string
	dir    string
	base   string
	tag    string
	// ready is a command that succeeds inside the fixture once its engine
	// answers.
	ready []string
	// sshdConfigDir is where the fixture's sshd reads drop-in files.
	sshdConfigDir string

	once sync.Once
	err  error
}

var (
	dockerFixture = &engineFixture{
		engine: "docker", dir: "docker-sshd", base: DockerHostBaseImage, tag: "pleiades/test-docker-sshd:29.9.0",
		ready: []string{"docker", "info"}, sshdConfigDir: "/etc/ssh/sshd_config.d",
	}
	podmanFixture = &engineFixture{
		engine: "podman", dir: "podman-sshd", base: PodmanHostBaseImage, tag: "pleiades/test-podman-sshd:5.8.7",
		ready: []string{"podman", "info"}, sshdConfigDir: "/etc/ssh/sshd_config.d",
	}
)

// StartDockerHost starts a Docker engine behind sshd (testdata/docker-sshd)
// and waits until sshd greets and the engine answers. It is terminated when
// tb ends. A machine with no docker command skips tb through Require, so the
// skip is counted rather than silent.
func StartDockerHost(tb testing.TB, opts EngineHostOptions) *EngineHost {
	tb.Helper()
	return startEngineHost(tb, dockerFixture, opts)
}

// StartPodmanHost starts Podman behind sshd (testdata/podman-sshd), with no
// API service listening: a session reaches it through dial-stdio and a
// service it starts, as on a host where podman.socket is not enabled.
func StartPodmanHost(tb testing.TB, opts EngineHostOptions) *EngineHost {
	tb.Helper()
	return startEngineHost(tb, podmanFixture, opts)
}

// startEngineHost builds f once per process and starts it with opts.
func startEngineHost(tb testing.TB, f *engineFixture, opts EngineHostOptions) *EngineHost {
	tb.Helper()
	_, lookErr := exec.LookPath("docker")
	Require(tb, "docker", lookErr == nil, "the docker command is not on PATH")

	f.once.Do(func() {
		dir := filepath.Join(RepoRoot(tb), "internal", "testsupport", "testdata", f.dir)
		cmd := exec.Command("docker", "build", "--build-arg", "BASE="+f.base, "-t", f.tag, dir) // #nosec G204 -- fixed arguments and a path inside this repository
		if out, err := cmd.CombinedOutput(); err != nil {
			f.err = fmt.Errorf("building the %s host fixture: %w\n%s", f.engine, err, out)
		}
	})
	if f.err != nil {
		tb.Fatal(f.err)
	}

	config := opts.SSHDConfig
	if config == "" {
		config = "AllowTcpForwarding local\n"
	}
	port := fmt.Sprintf("%d/tcp", engineHostInnerPort)
	files := append([]testcontainers.ContainerFile{{
		Reader:            strings.NewReader(config),
		ContainerFilePath: f.sshdConfigDir + "/pleiades-test.conf",
		FileMode:          0o644,
	}}, opts.Files...)
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        f.tag,
			ExposedPorts: append([]string{port}, opts.ExposedPorts...),
			Env:          opts.Env,
			// An engine inside a container needs these privileges; this is
			// a test fixture, never a deployment.
			HostConfigModifier: func(hc *container.HostConfig) { hc.Privileged = true },
			Files:              files,
			Networks:           opts.Networks,
			WaitingFor: wait.ForAll(
				SSHGreeting(port),
				wait.ForExec(f.ready).WithStartupTimeout(ContainerStartupTimeout),
			),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		tb.Fatalf("starting the %s host fixture: %v", f.engine, err)
	}
	tb.Cleanup(func() { _ = c.Terminate(context.Background()) })
	// Registered after Terminate, so it runs first: a failed test gets the
	// fixture's own log (sshd runs with -e, so its refusals are there, each
	// naming its reason), which is what a refused login on a machine nobody
	// can log into, such as a CI runner, otherwise never shows.
	tb.Cleanup(func() {
		if tb.Failed() {
			tb.Logf("the %s host fixture's log:\n%s\nits login settings:\n%s", f.engine, containerLogTail(c, 40), loginFacts(c))
		}
	})

	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatalf("%s host fixture address: %v", f.engine, err)
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		tb.Fatalf("%s host fixture port: %v", f.engine, err)
	}
	return &EngineHost{Engine: f.engine, Container: c, Host: host, Port: int(mapped.Num())}
}

// RootExec runs argv inside the fixture as root, bypassing sshd, and
// returns its combined output; a non-zero exit is an error carrying it.
// It is how a test prepares the engine (a container to exec into) and
// inspects the aftermath through a path independent of the code under
// test.
func (d *EngineHost) RootExec(ctx context.Context, argv ...string) (string, error) {
	code, reader, err := d.Container.Exec(ctx, argv, tcexec.Multiplexed())
	if err != nil {
		return "", fmt.Errorf("exec in the %s host fixture: %w", d.Engine, err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("reading exec output: %w", err)
	}
	if code != 0 {
		return string(out), fmt.Errorf("exec %q exited %d: %s", strings.Join(argv, " "), code, bytes.TrimSpace(out))
	}
	return string(out), nil
}

// StartContainer runs image on the fixture's engine as a container that
// sleeps, under a name no other run uses, and returns the name. For Podman
// it runs as asUser (root or the rootless user), whose store it then lives
// in; for Docker asUser is ignored. The image is copied in from this
// machine's engine (pulled there first only if absent) rather than pulled by
// the fixture, whose engine starts empty: CI loads the image from its cache,
// and a pull from inside every fixture would spend the registry's
// per-address limit. image must be pinned like every image here.
func (d *EngineHost) StartContainer(tb testing.TB, image, asUser string) string {
	tb.Helper()
	ctx := context.Background()
	if exec.Command("docker", "image", "inspect", image).Run() != nil { // #nosec G204 -- an image constant from a test
		if out, err := exec.Command("docker", "pull", "-q", image).CombinedOutput(); err != nil { // #nosec G204 -- same
			tb.Fatalf("pulling %s: %v\n%s", image, err, out)
		}
	}
	tarball := filepath.Join(tb.TempDir(), "image.tar")
	if out, err := exec.Command("docker", "save", "-o", tarball, image).CombinedOutput(); err != nil { // #nosec G204 -- same
		tb.Fatalf("saving %s: %v\n%s", image, err, out)
	}
	// Not /tmp: the dind image mounts a tmpfs there from inside the
	// container, which hides a file the engine copies in.
	const inside = "/opt/pleiades-test-image.tar"
	if err := d.Container.CopyFileToContainer(ctx, tarball, inside, 0o644); err != nil {
		tb.Fatalf("copying %s into the fixture: %v", image, err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatal(err)
	}
	name := "pleiades-test-" + hex.EncodeToString(suffix)
	load := []string{d.Engine, "load", "-i", inside}
	run := []string{d.Engine, "run", "-d", "--name", name, image, "sleep", "600"}
	if d.Engine == "podman" && asUser != "" && asUser != "root" {
		// The rootless user's store is its own; run there through a login
		// shell so its environment is the one an SSH login gets.
		load = []string{"su", "-l", asUser, "-c", strings.Join(load, " ")}
		run = []string{"su", "-l", asUser, "-c", strings.Join(run, " ")}
	}
	if out, err := d.RootExec(ctx, load...); err != nil {
		tb.Fatalf("loading %s in the fixture: %v\n%s", image, err, out)
	}
	if out, err := d.RootExec(ctx, run...); err != nil {
		tb.Fatalf("starting a container in the fixture: %v\n%s", err, out)
	}
	return name
}

// EngineID returns the identity the Docker fixture's engine reports for
// itself. (Podman reports none; see capability.EngineID.)
func (d *EngineHost) EngineID(tb testing.TB) string {
	tb.Helper()
	out, err := d.RootExec(context.Background(), "docker", "info", "--format", "{{.ID}}")
	if err != nil {
		tb.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// KnownHosts writes a known_hosts file into dir naming every one of the
// fixture's host keys under the address a test dials, and returns its
// path, so host keys are verified for real (StartSSHD's KnownHosts says
// why every key type is listed).
func (d *EngineHost) KnownHosts(ctx context.Context, dir string) (string, error) {
	var lines strings.Builder
	for _, keyType := range []string{"ed25519", "ecdsa", "rsa"} {
		pub, err := d.RootExec(ctx, "cat", "/etc/ssh/ssh_host_"+keyType+"_key.pub")
		if err != nil {
			return "", err
		}
		fields := strings.Fields(pub)
		if len(fields) < 2 {
			return "", fmt.Errorf("unexpected host key file content %q", pub)
		}
		for _, host := range []string{d.Host, "127.0.0.1", "localhost"} {
			fmt.Fprintf(&lines, "[%s]:%d %s %s\n", host, d.Port, fields[0], fields[1])
		}
	}
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		return "", fmt.Errorf("writing known_hosts: %w", err)
	}
	return path, nil
}

// containerLogTail returns the last n lines of c's log, or why it could not
// be read.
func containerLogTail(c testcontainers.Container, n int) string {
	logs, err := c.Logs(context.Background())
	if err != nil {
		return "(the log could not be read: " + err.Error() + ")"
	}
	defer logs.Close()
	b, err := io.ReadAll(io.LimitReader(logs, 1<<20))
	if err != nil {
		return "(the log could not be read: " + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// loginFacts is what decides a password login inside a fixture, for a
// refused login on a machine nobody can log into: sshd's effective
// settings, each fixture account's password scheme (the hash's prefix
// only), and the capabilities sshd runs with.
func loginFacts(c testcontainers.Container) string {
	const script = `/usr/sbin/sshd -T 2>&1 | grep -Ei '^(usepam|passwordauthentication|permitrootlogin|kbdinteractiveauthentication) '
awk -F: '$1=="root"||$1=="podman"||$1=="testuser"{print $1, "scheme=" substr($2,1,3)}' /etc/shadow
ls -l /etc/shadow
grep -E '^Cap(Eff|Bnd)' /proc/1/status`
	code, reader, err := c.Exec(context.Background(), []string{"/bin/sh", "-c", script}, tcexec.Multiplexed())
	if err != nil {
		return "(could not be read: " + err.Error() + ")"
	}
	out, _ := io.ReadAll(io.LimitReader(reader, 1<<16))
	return fmt.Sprintf("%s(exit %d)", out, code)
}

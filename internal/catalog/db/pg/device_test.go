// A db.pg device that is a real database host in a container:
// testdata/pgdevice's image, PostgresImage's own server and client
// programs with an SSH server and an unprivileged login. Its pg_dump and
// pg_restore load the libpq of their own release, which no distribution's
// client package promises, so a test can hold a client and its libpq at
// PostgreSQL 15 together. Defined in package pg so its internal tests and
// its external ones (pg_test) start the same device.
package pg

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// The device's login. A throwaway account in a throwaway container, so a
// literal password here is no secret.
const (
	DeviceUser     = "device"
	DevicePassword = "device-pw-for-tests"
	// DeviceHome is DeviceUser's home, the working directory of each of
	// its SSH commands.
	DeviceHome = "/home/device"
)

// deviceTag names the image testdata/pgdevice builds from
// PostgresImage. Not an image to pull: a local build, named so
// tools/testimages does not read it as one.
var deviceTag = "pleiades/db-pg-device:" + testsupport.PostgresImage[strings.LastIndex(testsupport.PostgresImage, ":")+1:]

// deviceBuild builds the image once per test binary.
var deviceBuild struct {
	once sync.Once
	out  string
	err  error
}

// TestDevice is a running device: its SSH server, and its database as
// this process reaches it. The device itself reaches its database at
// 127.0.0.1:5432.
type TestDevice struct {
	Container testcontainers.Container
	Host      string
	SSHPort   int
	DBPort    int
}

// StartTestDevice builds testdata/pgdevice and starts it with a database
// app owned by user, serving TLS from tlsDir's cert.pem and key.pem when
// tlsDir is set. It is removed when t ends.
func StartTestDevice(t *testing.T, user, password, tlsDir string) *TestDevice {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a real PostgreSQL device")
	}
	need.POSIXShell(t)
	_, err := exec.LookPath("docker")
	testsupport.Require(t, need.Docker, err == nil, "the device's image is built with docker build")
	deviceBuild.once.Do(func() {
		cmd := exec.Command("docker", "build", "--build-arg", "BASE="+testsupport.PostgresImage, "-t", deviceTag, "testdata/pgdevice") // #nosec G204 -- fixed arguments and a directory in this package
		out, err := cmd.CombinedOutput()
		deviceBuild.out, deviceBuild.err = string(out), err
	})
	if deviceBuild.err != nil {
		t.Fatalf("building %s: %v\n%s", deviceTag, deviceBuild.err, deviceBuild.out)
	}
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testpg.WithDatabase("app"), testpg.WithUsername(user), testpg.WithPassword(password),
		testcontainers.WithExposedPorts("22/tcp"),
		// PostgresReady's steps, then the SSH server's banner.
		testcontainers.WithWaitStrategyAndDeadline(testsupport.ContainerStartupTimeout,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(testsupport.ContainerStartupTimeout),
			testsupport.ForPublishedPort("5432/tcp", "Postgres"),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(testsupport.ContainerStartupTimeout),
			testsupport.SSHGreeting("22/tcp"),
		),
	}
	if tlsDir != "" {
		// The image's entrypoint installs them as the server needs a key.
		opts = append(opts, testcontainers.WithFiles(
			testcontainers.ContainerFile{HostFilePath: tlsDir + "/cert.pem", ContainerFilePath: "/tls/cert.pem", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: tlsDir + "/key.pem", ContainerFilePath: "/tls/key.pem", FileMode: 0o644},
		))
	}
	c, err := testpg.Run(ctx, deviceTag, opts...)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting the device: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sshPort, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dbPort, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return &TestDevice{Container: c, Host: host, SSHPort: int(sshPort.Num()), DBPort: int(dbPort.Num())}
}

// Root runs script through the device's /bin/sh as root, bypassing SSH,
// for laying out what a test needs and reading what it left, failing t on
// a non-zero exit. It returns the output.
func (d *TestDevice) Root(t *testing.T, script string) string {
	t.Helper()
	code, reader, err := d.Container.Exec(context.Background(), []string{"/bin/sh", "-c", script}, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("exec %q: %v", script, err)
	}
	out, _ := io.ReadAll(reader)
	if code != 0 {
		t.Fatalf("exec %q exited %d: %s", script, code, out)
	}
	return string(out)
}

// Connect opens an SSH connection to the device as DeviceUser, closed
// when t ends.
func (d *TestDevice) Connect(t *testing.T) *remoteexec.Conn {
	t.Helper()
	auth, err := remoteexec.AuthFrom(DeviceUser, DevicePassword, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(context.Background(), nil, remoteexec.Target{Host: d.Host, Port: d.SSHPort}, auth)
	if err != nil {
		t.Fatalf("connecting to the device: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

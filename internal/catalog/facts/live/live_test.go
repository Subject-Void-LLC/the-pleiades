// Helpers for running a probe's command in a container.
package live

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// execTimeout bounds one command in a container.
const execTimeout = 2 * time.Minute

// start runs image with sleep as its command under an init that reaps (a
// daemon a test starts from an exec would otherwise become a zombie its
// stop cannot end), and stops it when the test ends.
func start(t *testing.T, image string) testcontainers.Container {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a container")
	}
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{Image: image, Cmd: []string{"sleep", "infinity"},
			HostConfigModifier: func(hc *container.HostConfig) { init := true; hc.Init = &init }},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatalf("starting %s: %v", image, err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	return c
}

// runAs runs command through sh in c as user (the image's own when empty)
// and returns its standard output and exit status.
func runAs(t *testing.T, c testcontainers.Container, user, command string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	opts := []tcexec.ProcessOption{tcexec.Multiplexed()}
	if user != "" {
		opts = append(opts, tcexec.WithUser(user))
	}
	code, r, err := c.Exec(ctx, []string{"sh", "-c", command}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// run runs command as the image's own user, failing the test unless it
// exits 0.
func run(t *testing.T, c testcontainers.Container, command string) string {
	t.Helper()
	out, code := runAs(t, c, "", command)
	if code != 0 {
		t.Fatalf("%s exited %d: %s", command, code, out)
	}
	return out
}

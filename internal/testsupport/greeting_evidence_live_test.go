// The evidence a timed-out greeting wait reports, read from a real
// container through a real Docker daemon.
package testsupport

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// TestForGreeting_EvidenceFromARealContainer waits for an SSH greeting on a
// published port nothing listens on, inside a real nginx container: the
// refusal names Docker's own bindings and that nothing listens on 2222
// inside, and the same look finds nginx on 80.
func TestForGreeting_EvidenceFromARealContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a container")
	}
	Require(t, "docker", exec.Command("docker", "info").Run() == nil, "this machine's Docker engine does not answer")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{Image: NginxImage, ExposedPorts: []string{"2222/tcp", "80/tcp"}},
		Started:          true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}

	g := &greeting{port: "2222/tcp", prefix: "SSH-", protocol: "SSH", timeout: time.Second}
	err = g.WaitUntilReady(ctx, c)
	if err == nil {
		t.Fatal("a port nothing listens on sent a greeting")
	}
	mapped, mErr := c.MappedPort(ctx, "2222/tcp")
	if mErr != nil {
		t.Fatal(mErr)
	}
	for _, want := range []string{"Docker publishes it at [", ":" + mapped.Port(), "nothing inside listens on 2222"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to say %q", err, want)
		}
	}
	if got := insideListener(ctx, c, "80/tcp"); got != "something inside listens on 80" {
		t.Errorf("nginx's own port reads %q", got)
	}
}

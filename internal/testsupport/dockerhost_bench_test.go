// Benchmarks for reaching the engine fixture: an API request over an SSH
// channel against the docker CLI over the same login.
package testsupport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// BenchmarkEngineInfo_OverSSHChannel is one engine API request over a
// channel to the engine's socket on an open login: the path Phase 119a
// gives container.docker.exec. Each request opens its own channel.
func BenchmarkEngineInfo_OverSSHChannel(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	d := StartDockerHost(b, EngineHostOptions{})
	conn := dialFixture(b, ctx, d, DockerHostUser)
	dial := engineDialer(conn)
	for b.Loop() {
		if _, err := containerengine.ReadIdentity(ctx, dial, capability.EngineDocker); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEngineInfo_DockerCLIOverSSH is the same answer the way
// container.docker.run reaches the engine today and Ansible's CLI-based
// modules do: the docker CLI started in a shell over the same open login.
// It is the alternative the channel is measured against.
func BenchmarkEngineInfo_DockerCLIOverSSH(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	d := StartDockerHost(b, EngineHostOptions{})
	conn := dialFixture(b, ctx, d, DockerHostUser)
	for b.Loop() {
		res, err := conn.Run(ctx, "docker info --format '{{.ID}}'")
		if err != nil || res.ExitCode != 0 {
			b.Fatalf("%+v, %v", res, err)
		}
	}
}

// TestDockerHost_HundredRequestsOverOneLogin is the stress half of the
// gate: one login carries a hundred engine requests in turn, each on its
// own channel, and every one answers with the same identity.
func TestDockerHost_HundredRequestsOverOneLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := StartDockerHost(t, EngineHostOptions{})
	conn := dialFixture(t, ctx, d, DockerHostUser)
	var want capability.EngineID
	for i := range 100 {
		info, err := containerengine.ReadIdentity(ctx, engineDialer(conn), capability.EngineDocker)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if want == "" {
			want = info.ID
		}
		if info.ID != want {
			t.Fatalf("request %d answered %q, earlier ones %q", i, info.ID, want)
		}
	}
	if !strings.Contains(string(want), "-") {
		t.Errorf("identity %q does not look like an engine ID", want)
	}
}

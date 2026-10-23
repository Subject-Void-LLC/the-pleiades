// Spec.Differences against what a real Docker and a real Podman answer for
// a container made from Spec.Create's own request: no difference for the
// declaration it was made from, and one for a changed user.
package containerengine_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// specImage is the image the containers are made from, pinned as every
// test image is.
const specImage = "alpine:3.20.10"

// fullSpec declares every setting Differences compares, each away from its
// default, so a setting either engine reports in another shape shows up.
func fullSpec(t *testing.T, imageID string) containerengine.Spec {
	t.Helper()
	port, err := containerengine.ParsePortSpec("0.0.0.0:18080:80")
	if err != nil {
		t.Fatal(err)
	}
	port2, err := containerengine.ParsePortSpec("18081:81/udp")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := containerengine.ParseDevice("/dev/null:/dev/pleiades-null:r")
	if err != nil {
		t.Fatal(err)
	}
	return containerengine.Spec{
		Name: "pleiades-spec-" + strings.ReplaceAll(t.Name(), "/", "-"), Image: specImage, ImageID: imageID,
		Command: []string{"sleep", "600"}, Env: map[string]string{"A": "1"},
		Network: "bridge", Restart: "no", ReadOnly: true, User: "1000", Workdir: "/tmp",
		Labels: map[string]string{"pleiades.test": "spec"}, CapAdd: []string{"NET_ADMIN"},
		Ports: []containerengine.Port{port, port2}, Devices: []containerengine.Device{dev},
		Memory: 64 << 20, MemorySwap: 128 << 20,
		Attempt: "attempt-1",
	}
}

// requireNoDrift holds the live container to the spec it was made from,
// and to a changed user being a difference.
func requireNoDrift(t *testing.T, spec containerengine.Spec, live container.InspectResponse) {
	t.Helper()
	if d := spec.Differences(live); len(d) > 0 {
		t.Errorf("a container made from the spec differs from it:\n  %s", strings.Join(d, "\n  "))
	}
	changed := spec
	changed.User = "0"
	if d := changed.Differences(live); len(d) != 1 || !strings.HasPrefix(d[0], "user:") {
		t.Errorf("a changed user = %q, want one user difference", d)
	}
	if live.Config.Labels[containerengine.LabelAttempt] != spec.Attempt {
		t.Errorf("the attempt label = %q, want %q", live.Config.Labels[containerengine.LabelAttempt], spec.Attempt)
	}
}

// TestDifferences_Docker makes the container on this machine's Docker.
func TestDifferences_Docker(t *testing.T) {
	if testing.Short() {
		t.Skip("reaches a real Docker engine")
	}
	_, err := os.Stat("/var/run/docker.sock")
	testsupport.Require(t, "docker", err == nil, "no Docker socket")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	api, err := client.New(client.WithHost("unix:///var/run/docker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = api.Close() }()
	img, err := api.ImageInspect(ctx, specImage)
	if err != nil {
		stream, err := api.ImagePull(ctx, specImage, client.ImagePullOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		_ = stream.Close()
		if img, err = api.ImageInspect(ctx, specImage); err != nil {
			t.Fatal(err)
		}
	}
	spec := fullSpec(t, img.ID)
	cfg, host, net, err := spec.Create()
	if err != nil {
		t.Fatal(err)
	}
	made, err := api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: spec.Name, Config: cfg, HostConfig: host, NetworkingConfig: net})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = api.ContainerRemove(context.Background(), made.ID, client.ContainerRemoveOptions{Force: true})
	})
	live, err := api.ContainerInspect(ctx, made.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoDrift(t, spec, live.Container)
}

// TestDifferences_Podman makes the container through Podman's own
// Docker-compatible API, the one a session reaches, inside the fixture.
func TestDifferences_Podman(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Podman")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := testsupport.StartPodmanHost(t, testsupport.EngineHostOptions{})
	d.StartContainer(t, specImage, "")
	id, err := d.RootExec(ctx, "podman", "image", "inspect", "--format", "{{.Id}}", specImage)
	if err != nil {
		t.Fatalf("%v: %s", err, id)
	}
	const sock = "/run/pleiades-api.sock"
	if out, err := d.RootExec(ctx, "sh", "-c", "podman system service -t 0 unix://"+sock+" >/dev/null 2>&1 &"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	curl := func(args ...string) string {
		t.Helper()
		argv := append([]string{"curl", "-sS", "--fail-with-body", "--retry", "10", "--retry-connrefused", "--retry-delay", "1", "--unix-socket", sock}, args...)
		out, err := d.RootExec(ctx, argv...)
		if err != nil {
			t.Fatalf("curl %v: %v\n%s", args, err, out)
		}
		return out
	}
	// Podman names its images sha256:<hex>, as Docker does; the inspect
	// format prints the bare hex.
	spec := fullSpec(t, "sha256:"+strings.TrimSpace(id))
	cfg, host, net, err := spec.Create()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(struct {
		*container.Config
		HostConfig       *container.HostConfig
		NetworkingConfig *network.NetworkingConfig
	}{cfg, host, net})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := d.RootExec(ctx, "sh", "-c", "echo "+base64.StdEncoding.EncodeToString(body)+" | base64 -d > /opt/create.json"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	curl("-H", "Content-Type: application/json", "-d", "@/opt/create.json", "http://d/v1.41/containers/create?name="+spec.Name)
	var live container.InspectResponse
	if err := json.Unmarshal([]byte(curl("http://d/v1.41/containers/"+spec.Name+"/json")), &live); err != nil {
		t.Fatal(err)
	}
	if live.Image != spec.ImageID {
		// Podman may answer the image another way; the comparison is by ID,
		// so name what it said rather than fail on every other line.
		t.Logf("Podman reports the image as %q", live.Image)
	}
	requireNoDrift(t, spec, live)
}

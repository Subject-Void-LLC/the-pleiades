// The test harness, as internal/catalog/container's: this machine's real Docker engine, reached through its
// own socket as a device onboarded with the local carrier, and a runbook
// context that keeps what a method records.
package volume_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// dockerSocket is the engine these tests reach.
const dockerSocket = "/var/run/docker.sock"

// engineDevice is a device whose Docker engine this machine reaches
// through its own socket, as onboarding would have recorded it.
type engineDevice struct {
	*inventorytest.Stub
	id   capability.EngineID
	user string
}

// ContainerEngines names the one engine the device runs.
func (d *engineDevice) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}

// DockerEndpoint is the engine's socket.
func (d *engineDevice) DockerEndpoint() capability.SocketAddress { return dockerSocket }

// EngineSocket is the engine's socket.
func (d *engineDevice) EngineSocket(capability.EngineKind) capability.SocketAddress {
	return dockerSocket
}

// EngineID is the identity onboarding would have pinned.
func (d *engineDevice) EngineID(capability.EngineKind) capability.EngineID { return d.id }

// EngineCarriers is the local carrier alone.
func (d *engineDevice) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}

// EngineLoginUser is the account this test runs as.
func (d *engineDevice) EngineLoginUser() string { return d.user }

// recorder keeps what a method records.
type recorder struct{ stats map[string]any }

// InjectSecrets holds no secrets.
func (r *recorder) InjectSecrets() map[string]string { return map[string]string{} }

// SetStat keeps a stat.
func (r *recorder) SetStat(key string, value any) error { r.stats[key] = value; return nil }

// EmitFact keeps a fact as a stat.
func (r *recorder) EmitFact(key string, value any) error { return r.SetStat(key, value) }

// fresh is an empty recorder.
func fresh() *recorder { return &recorder{stats: map[string]any{}} }

// harness is the real engine as a device, and a client for the test's own
// reads and cleanup.
type harness struct {
	device inventory.InventoryItem
	api    *client.Client
	// nginx is testsupport.NginxImage pinned by digest.
	nginx string
}

// newHarness reaches this machine's Docker, skipping where there is none.
func newHarness(t *testing.T) *harness {
	t.Helper()
	if testing.Short() {
		t.Skip("reaches a real Docker engine")
	}
	_, err := os.Stat(dockerSocket)
	testsupport.Require(t, "docker", err == nil, "no Docker socket at "+dockerSocket)
	ctx := context.Background()
	id, err := containerengine.ReadIdentity(ctx, containerengine.Local(dockerSocket), capability.EngineDocker)
	if err != nil {
		t.Fatalf("reading the engine's identity: %v", err)
	}
	who, err := containerengine.LocalUser()
	if err != nil {
		t.Fatal(err)
	}
	containerengine.AllowLocalCarrier()
	api, err := client.New(client.WithHost("unix://" + dockerSocket))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	h := &harness{
		device: &engineDevice{
			Stub: &inventorytest.Stub{StubName: "engine-host", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal}},
			id: id.ID, user: who,
		},
		api: api,
	}
	h.nginx = h.pinned(t, testsupport.NginxImage)
	return h
}

// pinned pulls image when it is absent and returns it by its repository
// digest.
func (h *harness) pinned(t *testing.T, image string) string {
	t.Helper()
	ctx := context.Background()
	repo, _, _ := strings.Cut(image, ":")
	digestOf := func() (string, []string) {
		info, err := h.api.ImageInspect(ctx, image)
		if err != nil {
			return "", nil
		}
		for _, d := range info.RepoDigests {
			if strings.HasPrefix(d, repo+"@") || strings.HasPrefix(d, "docker.io/library/"+repo+"@") {
				return d, info.RepoDigests
			}
		}
		return "", info.RepoDigests
	}
	// An image restored from a cache (docker load) has its tag and no
	// repository digest; pulling it again records the digest.
	if d, _ := digestOf(); d == "" {
		stream, err := h.api.ImagePull(ctx, image, client.ImagePullOptions{})
		if err != nil {
			t.Fatalf("pulling %s: %v", image, err)
		}
		if err := stream.Wait(ctx); err != nil {
			t.Fatalf("pulling %s: %v", image, err)
		}
		_ = stream.Close()
	}
	d, held := digestOf()
	if d != "" {
		return d
	}
	t.Fatalf("%s has no repository digest: %v", image, held)
	return ""
}

// name is a container name of this test's own, removed when it ends.
func (h *harness) name(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	n := "pleiades-test-" + hex.EncodeToString(b)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = h.api.ContainerRemove(ctx, n, client.ContainerRemoveOptions{Force: true})
	})
	return n
}

// state is the container's state as the engine reports it, "" when there
// is none.
func (h *harness) state(t *testing.T, name string) string {
	t.Helper()
	res, err := h.api.ContainerInspect(context.Background(), name, client.ContainerInspectOptions{})
	if err != nil {
		return ""
	}
	return string(res.Container.State.Status)
}

// suffix is a random name suffix, so this test's objects never meet
// another's on a shared engine.
func suffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// volumeName is a volume name of this test's own, removed when it ends.
// Taken before the test's containers, so it is removed after them.
func (h *harness) volumeName(t *testing.T) string {
	t.Helper()
	n := "pleiades-test-vol-" + suffix()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = h.api.VolumeRemove(ctx, n, client.VolumeRemoveOptions{Force: true})
	})
	return n
}

// container creates, without starting, a container of this test's own
// from nginx mounting volume, and returns its name and ID.
func (h *harness) container(t *testing.T, volume string) (string, string) {
	t.Helper()
	name := h.name(t)
	res, err := h.api.ContainerCreate(context.Background(), client.ContainerCreateOptions{
		Name:       name,
		Config:     &moby.Config{Image: h.nginx},
		HostConfig: &moby.HostConfig{NetworkMode: "none", Binds: []string{volume + ":/data"}},
	})
	if err != nil {
		t.Fatalf("creating container %s: %v", name, err)
	}
	return name, res.ID
}

// The test harness: this machine's real Docker engine, reached through its
// own socket as a device onboarded with the local carrier, a runbook
// context that keeps what a method records, and images of each test's own,
// made by importing a tiny filesystem archive, so no test touches an image
// anything else uses.
package image_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/image"
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

func (d *engineDevice) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}
func (d *engineDevice) DockerEndpoint() capability.SocketAddress { return dockerSocket }
func (d *engineDevice) EngineSocket(capability.EngineKind) capability.SocketAddress {
	return dockerSocket
}
func (d *engineDevice) EngineID(capability.EngineKind) capability.EngineID { return d.id }
func (d *engineDevice) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}
func (d *engineDevice) EngineLoginUser() string { return d.user }

// recorder keeps what a method records.
type recorder struct{ stats map[string]any }

func (r *recorder) InjectSecrets() map[string]string     { return map[string]string{} }
func (r *recorder) SetStat(key string, value any) error  { r.stats[key] = value; return nil }
func (r *recorder) EmitFact(key string, value any) error { return r.SetStat(key, value) }

func fresh() *recorder { return &recorder{stats: map[string]any{}} }

// harness is the real engine as a device, and a client for the test's own
// reads and cleanup.
type harness struct {
	device inventory.InventoryItem
	api    *client.Client
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
	return &harness{
		device: &engineDevice{
			Stub: &inventorytest.Stub{StubName: "engine-host", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal}},
			id: id.ID, user: who,
		},
		api: api,
	}
}

// call runs fqcn's Invoke, or its Check, with params.
func call(t *testing.T, h *harness, rc *recorder, fqcn string, check bool, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	if check {
		return d.Check(context.Background(), rc, h.device, params)
	}
	return d.Invoke(context.Background(), rc, h.device, params)
}

// inverseOf is the undo a run recorded.
func inverseOf(rc *recorder) (string, map[string]any) {
	inv, _ := rc.stats["inverse"].(map[string]any)
	fqcn, _ := inv["fqcn"].(string)
	params, _ := inv["params"].(map[string]any)
	return fqcn, params
}

// suffix is a random name part of this test's own.
func suffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// repo is an image repository of this test's own; every image in it is
// removed when the test ends.
func (h *harness) repo(t *testing.T) string {
	t.Helper()
	r := "pleiades-test-" + suffix()
	t.Cleanup(func() { h.removeRepo(r) })
	return r
}

// removeRepo removes every image tagged in repo.
func (h *harness) removeRepo(repo string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := h.api.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return
	}
	for _, img := range list.Items {
		for _, tag := range img.RepoTags {
			if strings.HasPrefix(tag, repo+":") {
				_, _ = h.api.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{Force: true})
				break
			}
		}
	}
}

// rootfs writes a one-file filesystem archive holding content into dir
// and returns its path.
func rootfs(t *testing.T, dir, content string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: int64(len(content)), ModTime: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "rootfs-"+suffix()+".tar")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// imported makes the image ref from a one-file archive through
// container.image.import_rootfs and returns its ID.
func (h *harness) imported(t *testing.T, ref, content string) string {
	t.Helper()
	rc := fresh()
	if _, err := call(t, h, rc, "container.image.import_rootfs", false, map[string]any{"path": rootfs(t, t.TempDir(), content), "image": ref}); err != nil {
		t.Fatalf("importing %s: %v", ref, err)
	}
	id, _ := rc.stats["image_id"].(string)
	if id == "" {
		t.Fatalf("importing %s returned no image ID: %v", ref, rc.stats)
	}
	return id
}

// idOf is the ID of the image ref names, "" when there is none.
func (h *harness) idOf(t *testing.T, ref string) string {
	t.Helper()
	res, err := h.api.ImageInspect(context.Background(), ref)
	if err != nil {
		return ""
	}
	return res.ID
}

// container creates a stopped container of this test's own from image.
func (h *harness) container(t *testing.T, image string) string {
	t.Helper()
	ctx := context.Background()
	name := "pleiades-test-" + suffix()
	res, err := h.api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name, Config: &mobycontainer.Config{Image: image, Cmd: []string{"/hello.txt"}}})
	if err != nil {
		t.Fatalf("creating a container of %s: %v", image, err)
	}
	t.Cleanup(func() {
		_, _ = h.api.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true})
	})
	return name
}

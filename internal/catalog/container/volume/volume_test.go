// A volume's whole life through the container.volume.* methods, against
// this machine's real Docker engine.
package volume_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

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

// volumeExists reports whether the engine has a volume named name.
func (h *harness) volumeExists(t *testing.T, name string) bool {
	t.Helper()
	_, err := h.api.VolumeInspect(context.Background(), name, client.VolumeInspectOptions{})
	return err == nil
}

// TestVolume_ALifeThroughTheMethods: a check of create makes nothing;
// create makes the volume and records its removal pinned to when it was
// made; a second create is no change and a differing one is refused; info
// reads it; remove refuses a volume a container mounts and a volume made
// at another moment, and the recorded undo, replayed, removes it.
func TestVolume_ALifeThroughTheMethods(t *testing.T) {
	h := newHarness(t)
	vol := h.volumeName(t)
	declared := map[string]any{"name": vol, "labels": map[string]any{"app": "pleiades-test"}}

	if res, err := call(t, h, fresh(), "container.volume.create", true, declared); err != nil || !res.Changed || h.volumeExists(t, vol) {
		t.Fatalf("a check of create = %+v, %v; exists %t", res, err, h.volumeExists(t, vol))
	}
	rc := fresh()
	if res, err := call(t, h, rc, "container.volume.create", false, declared); err != nil || !res.Changed || !h.volumeExists(t, vol) {
		t.Fatalf("create = %+v, %v", res, err)
	}
	fqcn, undo := inverseOf(rc)
	if fqcn != "container.volume.remove" || undo["name"] != vol || undo["created_at"] == "" || undo["created_at"] != rc.stats["created_at"] {
		t.Errorf("create's undo = %s %v, stats %v", fqcn, undo, rc.stats)
	}
	if res, err := call(t, h, fresh(), "container.volume.create", false, declared); err != nil || res.Changed {
		t.Errorf("a second create = %+v, %v; want no change", res, err)
	}
	if _, err := call(t, h, fresh(), "container.volume.create", true, map[string]any{"name": vol, "labels": map[string]any{"app": "other"}}); err == nil || !strings.Contains(err.Error(), "labels: app") {
		t.Errorf("a differing create = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.volume.create", true, map[string]any{"name": vol, "driver": "nfs"}); err == nil || !strings.Contains(err.Error(), "driver") {
		t.Errorf("another driver = %v", err)
	}

	rc = fresh()
	if _, err := call(t, h, rc, "container.volume.info", true, map[string]any{"name": vol}); err != nil {
		t.Fatal(err)
	}
	if labels, _ := rc.stats["labels"].(map[string]string); rc.stats["exists"] != true || rc.stats["driver"] != "local" || labels["app"] != "pleiades-test" || rc.stats["mountpoint"] == "" {
		t.Errorf("info = %v", rc.stats)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.volume.info", false, map[string]any{"name": vol + "-absent"}); err != nil || rc.stats["exists"] != false {
		t.Errorf("info of an absent volume = %v, %v", rc.stats, err)
	}

	ctr, _ := h.container(t, vol)
	if _, err := call(t, h, fresh(), "container.volume.remove", true, map[string]any{"name": vol}); err == nil || !strings.Contains(err.Error(), ctr) {
		t.Errorf("removing a mounted volume = %v, want it refused naming %s", err, ctr)
	}
	if _, err := h.api.ContainerRemove(context.Background(), ctr, client.ContainerRemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := call(t, h, fresh(), "container.volume.remove", false, map[string]any{"name": vol, "created_at": "2001-01-01T00:00:00Z"}); err == nil || !strings.Contains(err.Error(), "does nothing to it") {
		t.Errorf("removing a volume made at another moment = %v", err)
	}
	if res, err := call(t, h, fresh(), "container.volume.remove", true, map[string]any{"name": vol}); err != nil || !res.Changed || !h.volumeExists(t, vol) {
		t.Errorf("a check of remove = %+v, %v; exists %t", res, err, h.volumeExists(t, vol))
	}
	rc = fresh()
	if res, err := call(t, h, rc, fqcn, false, undo); err != nil || !res.Changed || h.volumeExists(t, vol) {
		t.Fatalf("replaying create's undo = %+v, %v; exists %t", res, err, h.volumeExists(t, vol))
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Error("an irreversible removal recorded an undo")
	}
	if res, err := call(t, h, fresh(), "container.volume.remove", false, map[string]any{"name": vol}); err != nil || res.Changed {
		t.Errorf("removing an absent volume = %+v, %v; want no change", res, err)
	}
}

// TestVolumeInfo_NamesOptionsWithoutTheirValues: a local volume's mount
// options can carry a password, so info reports their names only.
func TestVolumeInfo_NamesOptionsWithoutTheirValues(t *testing.T) {
	h := newHarness(t)
	vol := h.volumeName(t)
	if _, err := h.api.VolumeCreate(context.Background(), client.VolumeCreateOptions{Name: vol, Driver: "local",
		DriverOpts: map[string]string{"type": "tmpfs", "device": "tmpfs", "o": "size=1m,uid=4242"}}); err != nil {
		t.Fatal(err)
	}
	rc := fresh()
	if _, err := call(t, h, rc, "container.volume.info", false, map[string]any{"name": vol}); err != nil {
		t.Fatal(err)
	}
	if names, _ := rc.stats["option_names"].([]string); !slices.Equal(names, []string{"device", "o", "type"}) {
		t.Errorf("option_names = %v", rc.stats["option_names"])
	}
	for k, v := range rc.stats {
		if s, ok := v.(string); ok && strings.Contains(s, "4242") {
			t.Errorf("stat %s holds an option's value", k)
		}
	}
}

// TestVolume_RefusesANameAnEngineWouldReadOtherwise: a name that could
// reshape a request path is refused before contact.
func TestVolume_RefusesANameAnEngineWouldReadOtherwise(t *testing.T) {
	h := newHarness(t)
	for _, fqcn := range []string{"container.volume.create", "container.volume.remove", "container.volume.info"} {
		if _, err := call(t, h, fresh(), fqcn, true, map[string]any{"name": "../etc"}); err == nil || !strings.Contains(err.Error(), "volume name") {
			t.Errorf("%s with a hostile name = %v", fqcn, err)
		}
	}
}

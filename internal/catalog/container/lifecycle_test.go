// A container's whole life through the container.* methods, against this
// machine's real Docker engine.
package container_test

import (
	"context"
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

// TestContainer_ALifeThroughTheMethods: a check of run creates nothing;
// run creates and starts, recording the removal by ID; a second run is no
// change; a declaration that differs is refused naming recreate and never
// an environment value; stop and a run that starts the stopped container
// record each other; remove refuses a running container without force,
// refuses another container's ID, removes, and is then no change.
func TestContainer_ALifeThroughTheMethods(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	declared := map[string]any{"name": name, "image": h.nginx, "env": map[string]any{"TOKEN": "secret-sentinel"}}

	rc := fresh()
	if res, err := call(t, h, rc, "container.run", true, declared); err != nil || !res.Changed || h.state(t, name) != "" {
		t.Fatalf("a check of run = %+v, %v; state %q", res, err, h.state(t, name))
	}

	rc = fresh()
	res, err := call(t, h, rc, "container.run", false, declared)
	if err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Fatalf("run = %+v, %v; state %q", res, err, h.state(t, name))
	}
	id, _ := rc.stats["id"].(string)
	if fqcn, p := inverseOf(rc); fqcn != "container.remove" || p["id"] != id || p["force"] != true {
		t.Errorf("run's undo = %s %v", fqcn, p)
	}
	if names, _ := rc.stats["env_names"].([]string); len(names) != 1 || names[0] != "TOKEN" {
		t.Errorf("env_names = %v", rc.stats["env_names"])
	}
	for k, v := range rc.stats {
		if s, ok := v.(string); ok && strings.Contains(s, "secret-sentinel") {
			t.Errorf("stat %s holds the environment value", k)
		}
	}

	if res, err := call(t, h, fresh(), "container.run", false, declared); err != nil || res.Changed {
		t.Errorf("a second run = %+v, %v; want no change", res, err)
	}

	changed := map[string]any{"name": name, "image": h.nginx, "env": map[string]any{"TOKEN": "other-sentinel"}}
	_, err = call(t, h, fresh(), "container.run", false, changed)
	if err == nil || !strings.Contains(err.Error(), "container.recreate") || !strings.Contains(err.Error(), "env: TOKEN") {
		t.Errorf("a differing run = %v, want it refused naming recreate", err)
	}
	if err != nil && (strings.Contains(err.Error(), "sentinel")) {
		t.Errorf("the refusal names an environment value: %v", err)
	}

	rc = fresh()
	if res, err := call(t, h, rc, "container.stop", false, map[string]any{"name": name, "timeout": 1}); err != nil || !res.Changed || h.state(t, name) != "exited" {
		t.Fatalf("stop = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if fqcn, p := inverseOf(rc); fqcn != "container.start" || p["id"] != id {
		t.Errorf("stop's undo = %s %v", fqcn, p)
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.run", false, declared); err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Fatalf("run of the stopped container = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if fqcn, _ := inverseOf(rc); fqcn != "container.stop" {
		t.Errorf("starting it recorded %s as the undo", fqcn)
	}

	if _, err := call(t, h, fresh(), "container.remove", false, map[string]any{"name": name}); err == nil || !strings.Contains(err.Error(), "set force") {
		t.Errorf("removing a running container without force = %v", err)
	}
	other := strings.Repeat("0", 64)
	if _, err := call(t, h, fresh(), "container.remove", false, map[string]any{"name": name, "id": other, "force": true}); err == nil || !strings.Contains(err.Error(), "does nothing to it") {
		t.Errorf("removing with another container's ID = %v", err)
	}
	if res, err := call(t, h, fresh(), "container.remove", true, map[string]any{"name": name, "force": true}); err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Errorf("a check of remove = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if res, err := call(t, h, fresh(), "container.remove", false, map[string]any{"name": name, "id": id, "force": true}); err != nil || !res.Changed || h.state(t, name) != "" {
		t.Fatalf("remove = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if res, err := call(t, h, fresh(), "container.remove", false, map[string]any{"name": name}); err != nil || res.Changed {
		t.Errorf("removing an absent container = %+v, %v; want no change", res, err)
	}
}

// TestRun_SafeDefaults: a container run with no more than a name, an image
// and a read-only bind mount joins no network, mounts read-only, and runs
// with no-new-privileges and the image by digest.
func TestRun_SafeDefaults(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	dir := t.TempDir()
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx, "volumes": []any{dir + ":/mnt"}}); err != nil {
		t.Fatal(err)
	}
	res, err := h.api.ContainerInspect(context.Background(), name, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Container
	if c.HostConfig.NetworkMode != "none" || !strings.Contains(strings.Join(c.HostConfig.SecurityOpt, ","), "no-new-privileges") {
		t.Errorf("host config = network %s, security %v", c.HostConfig.NetworkMode, c.HostConfig.SecurityOpt)
	}
	if len(c.Mounts) != 1 || c.Mounts[0].RW {
		t.Errorf("mounts = %+v, want one read-only", c.Mounts)
	}
	if !strings.HasPrefix(c.Image, "sha256:") || c.Config.Labels["org.pleiades.image"] != h.nginx {
		t.Errorf("image = %s, label %q", c.Image, c.Config.Labels["org.pleiades.image"])
	}
}

// What the container methods do when the engine refuses them, and the
// states they decline to act on, against the real engine.
package container_test

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// TestRun_AFailedStartLeavesNothing: a container whose entrypoint does not
// exist is created and fails to start (a missing command would not: the
// image's entrypoint starts and runs it), and the run removes it rather than
// leave a container no undo names; one on an absent network likewise.
func TestRun_AFailedStartLeavesNothing(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	rc := fresh()
	_, err := call(t, h, rc, "container.run", false, map[string]any{"name": name, "image": h.nginx, "entrypoint": []any{"/pleiades-no-such-program"}})
	if err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("a run whose program is absent = %v, want the start refused and the container removed", err)
	}
	if state := h.state(t, name); state != "" {
		t.Errorf("the failed run left a container in state %q", state)
	}
	if fqcn, _ := inverseOf(rc); fqcn != "" {
		t.Errorf("the failed run recorded %s as its undo", fqcn)
	}

	name = h.name(t)
	// The engine creates a container on an absent network and refuses it
	// only at start, so this too is a start the run cleans up after.
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx, "network": "pleiades-no-such-network"}); err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Errorf("a run on an absent network = %v", err)
	}
	if state := h.state(t, name); state != "" {
		t.Errorf("the refused run left a container in state %q", state)
	}
}

// TestLifecycle_StatesTheMethodsDecline: a created container is started by
// run, recording stop as its undo; a paused one is refused by run and by
// start, and by remove without force; start, stop and restart of an
// absent container are refused; a stop timeout out of range is refused
// before the engine is asked anything.
func TestLifecycle_StatesTheMethodsDecline(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	declared := map[string]any{"name": name, "image": h.nginx}
	if res, err := call(t, h, fresh(), "container.create", false, declared); err != nil || !res.Changed || h.state(t, name) != "created" {
		t.Fatalf("create = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if res, err := call(t, h, fresh(), "container.create", false, declared); err != nil || res.Changed {
		t.Errorf("a second create = %+v, %v; want no change", res, err)
	}
	if res, err := call(t, h, fresh(), "container.run", true, declared); err != nil || !res.Changed || h.state(t, name) != "created" {
		t.Errorf("a check of run on a created container = %+v, %v; state %q", res, err, h.state(t, name))
	}
	rc := fresh()
	if res, err := call(t, h, rc, "container.run", false, declared); err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Fatalf("run of a created container = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if fqcn, p := inverseOf(rc); fqcn != "container.stop" || p["id"] == nil {
		t.Errorf("starting it recorded %s %v as the undo", fqcn, p)
	}

	ctx := context.Background()
	if _, err := h.api.ContainerPause(ctx, name, client.ContainerPauseOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.api.ContainerUnpause(context.Background(), name, client.ContainerUnpauseOptions{}) })
	if _, err := call(t, h, fresh(), "container.run", false, declared); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Errorf("run of a paused container = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.remove", false, map[string]any{"name": name}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Errorf("remove of a paused container without force = %v", err)
	}
	// The refusal's remedy works: a paused container stops, and runs again.
	if res, err := call(t, h, fresh(), "container.stop", false, map[string]any{"name": name, "timeout": 1}); err != nil || !res.Changed || h.state(t, name) != "exited" {
		t.Fatalf("stop of a paused container = %+v, %v; state %q", res, err, h.state(t, name))
	}
	if res, err := call(t, h, fresh(), "container.run", false, declared); err != nil || !res.Changed || h.state(t, name) != "running" {
		t.Fatalf("run after stopping the paused container = %+v, %v; state %q", res, err, h.state(t, name))
	}
	// A declared setting the container does not have is a difference: the
	// user it runs as is compared, not taken as declared.
	asOther := map[string]any{"name": name, "image": h.nginx, "user": "1000"}
	if _, err := call(t, h, fresh(), "container.run", false, asOther); err == nil || !strings.Contains(err.Error(), "user:") {
		t.Errorf("a run declaring another user = %v, want the difference named", err)
	}

	absent := h.name(t)
	for _, fqcn := range []string{"container.start", "container.stop", "container.restart"} {
		if _, err := call(t, h, fresh(), fqcn, false, map[string]any{"name": absent}); err == nil || !strings.Contains(err.Error(), "no container named") {
			t.Errorf("%s of an absent container = %v", fqcn, err)
		}
	}
	for _, timeout := range []any{-1, 1 << 30, "soon"} {
		if _, err := call(t, h, fresh(), "container.stop", false, map[string]any{"name": name, "timeout": timeout}); err == nil {
			t.Errorf("a stop timeout of %v was accepted", timeout)
		}
	}
	if _, err := call(t, h, fresh(), "container.start", false, map[string]any{"name": "-bad name"}); err == nil {
		t.Error("a hostile container name was accepted")
	}
}

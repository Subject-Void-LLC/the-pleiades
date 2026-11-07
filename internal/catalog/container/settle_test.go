// A stop is in the engine's container list by the time container.stop
// returns, on engines that answer a stop before writing it there too.
package container_test

import (
	"context"
	"strings"
	"testing"
	"time"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestStop_TheListShowsTheStopWhenTheMethodReturns stops one container
// twenty times and lists it the moment each stop returns, asking for it as
// stopped the way container.prune does. Docker before 28.3.0, which GitHub's
// Ubuntu runners have, answers a stop before its list says so (moby issue
// 50133). Measured on 28.0.4 without container.stop's settle, all 200 of
// 200 stops listed as running when listed at once; with it, none did.
// FAILURE_PATTERNS 560 was that miss, seen by container.prune about one
// run in eleven, since opening its session takes long enough for the
// engine to catch up most times. On an engine with the fix it passes
// either way.
func TestStop_TheListShowsTheStopWhenTheMethodReturns(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	if _, err := call(t, h, fresh(), "container.run", false, map[string]any{"name": name, "image": h.nginx}); err != nil {
		t.Fatal(err)
	}
	stopped := client.Filters{}.Add("name", "^/"+name+"$").Add("status", "exited")
	missed := 0
	for i := 0; i < 20; i++ {
		if i > 0 {
			if _, err := call(t, h, fresh(), "container.start", false, map[string]any{"name": name}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := call(t, h, fresh(), "container.stop", false, map[string]any{"name": name, "timeout": 1}); err != nil {
			t.Fatal(err)
		}
		res, err := h.api.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: stopped})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 1 {
			missed++
		}
	}
	if missed > 0 {
		t.Errorf("%d of 20 stops were not yet in the engine's list of stopped containers when container.stop returned", missed)
	}
}

// TestStop_AnAutoRemoveContainerIsSettledAndHasNoUndo stops a container
// the engine removes when it stops (docker run --rm). The read after the
// stop finds it gone or going, which inspect reads as no container rather
// than an error, so the stop succeeds; and since starting a removed
// container cannot undo the stop, it records no undo and says why, where
// it used to record container.start, an undo no rollback could run.
func TestStop_AnAutoRemoveContainerIsSettledAndHasNoUndo(t *testing.T) {
	h := newHarness(t)
	name := h.name(t)
	ctx := context.Background()
	if _, err := h.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name, Image: h.nginx, HostConfig: &moby.HostConfig{AutoRemove: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.api.ContainerStart(ctx, name, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	rc := fresh()
	res, err := call(t, h, rc, "container.stop", false, map[string]any{"name": name, "timeout": 1})
	if err != nil || !res.Changed {
		t.Fatalf("stop of an auto-remove container = %+v, %v; want a change", res, err)
	}
	if fqcn, p := inverseOf(rc); fqcn != "" {
		t.Errorf("the stop recorded %s %v as its undo, though the container removes itself", fqcn, p)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "auto-remove") {
		t.Errorf("warnings = %q, want the missing undo explained", warnings)
	}
	deadline := time.Now().Add(30 * time.Second)
	for h.state(t, name) != "" {
		if time.Now().After(deadline) {
			t.Fatalf("the auto-remove container is still %q after its stop", h.state(t, name))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

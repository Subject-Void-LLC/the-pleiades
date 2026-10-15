// Tests that container.docker.run starts an existing stopped container, and
// container.docker.stop's identity guard and undo.
package docker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
)

// TestRun_StartsAStoppedContainer: an existing exited or created container is
// started by its inspected id, the run reports a change, and the undo stops it
// again, pinned to that id; nothing is created.
func TestRun_StartsAStoppedContainer(t *testing.T) {
	for _, status := range []string{"exited", "created"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t, containerFixture{exists: true, status: status})
			result, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
			if err != nil || !result.Changed {
				t.Fatalf("Run = %+v, %v; want the start reported", result, err)
			}
			if calls := h.invocations(t); len(calls) != 1 || calls[0] != "docker start -- "+fakeContainerID {
				t.Fatalf("invocations = %v, want one start by id", calls)
			}
			fqcn, params, ok := inverseOf(h.rc)
			if !ok || fqcn != "container.docker.stop" || params["name"] != "web" || params["id"] != fakeContainerID {
				t.Fatalf("inverse = %q, %v, ok=%v; want a stop pinned to the id", fqcn, params, ok)
			}
		})
	}
}

// TestRun_StartFalseLeavesItAsItIs: a one-shot container whose command is
// meant to run once is left stopped, and nothing is sent.
func TestRun_StartFalseLeavesItAsItIs(t *testing.T) {
	h := newHarness(t, stopped)
	result, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27", "start": false}))
	if err != nil || result.Changed {
		t.Fatalf("Run = %+v, %v; want no change", result, err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("invocations = %v, want none", calls)
	}
}

// TestRun_RefusesWhatIsNotStoppedOrRunning: a paused container is not
// stopped, and dead or removing cannot be started; each is refused by name,
// in a check and for real, unless start is false.
func TestRun_RefusesWhatIsNotStoppedOrRunning(t *testing.T) {
	for status, want := range map[string]string{"paused": "unpause it", "dead": "remove it", "removing": "remove it"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t, containerFixture{exists: true, status: status})
			for _, m := range []methodFunc{dockermod.CheckRun, dockermod.Run} {
				if _, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"})); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
			}
			if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27", "start": false})); err != nil {
				t.Fatalf("start: false = %v, want it left alone", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("invocations = %v, want none", calls)
			}
		})
	}
}

// TestRun_CheckPredictsTheStart: a check reports the start a real run would
// make, sends nothing, and records no undo.
func TestRun_CheckPredictsTheStart(t *testing.T) {
	h := newHarness(t, stopped)
	result, err := dockermod.CheckRun(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
	if err != nil || !result.Changed {
		t.Fatalf("CheckRun = %+v, %v; want the start predicted", result, err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("a check sent %v", calls)
	}
	diff, _ := h.rc.stats[sdk.StatDiff].(map[string]any)
	if after, _ := diff["after"].(map[string]any); after["status"] != "running" {
		t.Errorf("predicted after = %v, want running", diff["after"])
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Error("a check recorded an undo")
	}
}

// TestRun_AStartThatFailsIsAnError: docker refusing the start fails the
// task with its own words, and no undo is recorded for a start that did not
// happen.
func TestRun_AStartThatFailsIsAnError(t *testing.T) {
	h := newHarness(t, containerFixture{exists: true, status: "exited", startExit: 1})
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"})); err == nil || !strings.Contains(err.Error(), "external connectivity") {
		t.Fatalf("err = %v, want docker's refusal", err)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Error("a failed start recorded an undo")
	}
}

// TestRunAndStop_RefuseAnUnusableID: the id each starts or stops by is text
// from the device that becomes an operand, so one that is not a full id is
// refused before anything is sent.
func TestRunAndStop_RefuseAnUnusableID(t *testing.T) {
	h := newHarness(t, containerFixture{exists: true, status: "exited", containerID: "$(id)"})
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"})); err == nil || !strings.Contains(err.Error(), "no usable id") {
		t.Fatalf("run: err = %v, want the id refused", err)
	}
	h = newHarness(t, containerFixture{exists: true, status: "running", containerID: "$(id)"})
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil || !strings.Contains(err.Error(), "no usable id") {
		t.Fatalf("stop: err = %v, want the id refused", err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("invocations = %v, want none", calls)
	}
}

// TestStop_TheIDGuardRefusesAnotherContainerOfTheName: the undo of run's
// start names the container by id, so one run later under the same name is
// never stopped by it, in a check or for real.
func TestStop_TheIDGuardRefusesAnotherContainerOfTheName(t *testing.T) {
	h := newHarness(t, running)
	stranger := h.params(map[string]any{"name": "web", "id": strings.Repeat("f", 64)})
	for _, m := range []methodFunc{dockermod.CheckStop, dockermod.Stop} {
		if _, err := m(context.Background(), h.rc, h.device, stranger); err == nil || !strings.Contains(err.Error(), "nothing was stopped") {
			t.Fatalf("stop with another container's id = %v, want it refused", err)
		}
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("a refused stop sent %v", calls)
	}
	if result, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "id": fakeContainerID})); err != nil || !result.Changed {
		t.Fatalf("stop with the container's own id = %+v, %v", result, err)
	}
}

// TestRunAndStop_ALostReadBackKeepsTheUndo: once a start or a stop happened, a
// read back the connection lost is a warning, and a warning that cannot be
// recorded fails the task.
func TestRunAndStop_ALostReadBackKeepsTheUndo(t *testing.T) {
	h := newHarnessBudgeted(t, stopped, 3)
	if result, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"})); err != nil || !result.Changed {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	if fqcn, _, ok := inverseOf(h.rc); !ok || fqcn != "container.docker.stop" {
		t.Fatalf("inverse = %q, ok=%v", fqcn, ok)
	}
	h = newHarnessBudgeted(t, stopped, 3)
	h.rc.failOnKey = sdk.StatWarnings
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"})); err == nil {
		t.Fatal("a start's warning that could not be recorded was dropped")
	}
	h = newHarnessBudgeted(t, running, 3)
	h.rc.failOnKey = sdk.StatWarnings
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("a stop's warning that could not be recorded was dropped")
	}
}

// TestRun_StartMustBeABoolean: a start written as text ("yes") is refused
// rather than read as true or false.
func TestRun_StartMustBeABoolean(t *testing.T) {
	h := newHarness(t, stopped)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27", "start": "yes"})); err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("err = %v, want start refused", err)
	}
}

// TestStop_AnUndoThatCannotBeRecordedFailsTheTask: a stop whose undo cannot
// be recorded fails, rather than reporting a change nothing can reverse.
func TestStop_AnUndoThatCannotBeRecordedFailsTheTask(t *testing.T) {
	h := newHarness(t, running)
	h.rc.failOnKey = sdk.StatInverse
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil || !strings.Contains(err.Error(), sdk.StatInverse) {
		t.Fatalf("err = %v, want the failed undo surfaced", err)
	}
}

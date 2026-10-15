// Tests for what container.docker.run leaves behind when docker run fails
// after creating the container, and for the id its undo is pinned to.
package docker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
)

// runWeb runs container.docker.run for "web" against h.
func runWeb(t *testing.T, h *harness) error {
	t.Helper()
	_, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
	return err
}

// TestRun_AFailedStartRemovesTheContainerItCreated: docker creates a
// container before starting it, so a start that fails (a host port already
// taken) leaves it "created" under the name. The engine drops a failed task's
// undo, and the next run would find the name and never start it, so the
// failed run removes it, by id, and says so.
func TestRun_AFailedStartRemovesTheContainerItCreated(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 125, runLeftover: "created",
		runFailMessage: "driver failed programming external connectivity: port is already allocated"})
	err := runWeb(t, h)
	if err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Fatalf("err = %v, want the failed start reported with the cleanup", err)
	}
	calls := h.invocations(t)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "docker run ") || calls[1] != "docker rm --force -- "+fakeContainerID {
		t.Fatalf("invocations = %v, want the run then a removal by id", calls)
	}
}

// TestRun_ANameAlreadyInUseIsNotRemoved: docker refusing the name means the
// container under it belongs to someone else, created between this task's
// inspect and its run, and is never removed.
func TestRun_ANameAlreadyInUseIsNotRemoved(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 125, runLeftover: "created",
		runFailMessage: `Conflict. The container name "/web" is already in use by container "` + fakeContainerID + `"`})
	if err := runWeb(t, h); err == nil || strings.Contains(err.Error(), "removed") {
		t.Fatalf("err = %v, want the conflict reported and nothing removed", err)
	}
	if calls := h.invocations(t); len(calls) != 1 {
		t.Fatalf("invocations = %v, want the run alone", calls)
	}
}

// TestRun_ALeftoverThatIsNotCreatedIsLeft: only a container still "created"
// is the failed run's own; one that is running is not this task's to remove.
func TestRun_ALeftoverThatIsNotCreatedIsLeft(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 125, runLeftover: "running"})
	if err := runWeb(t, h); err == nil || !strings.Contains(err.Error(), "not this task's to remove") {
		t.Fatalf("err = %v, want the running container named and left", err)
	}
	if calls := h.invocations(t); len(calls) != 1 {
		t.Fatalf("invocations = %v, want the run alone", calls)
	}
}

// TestRun_ACleanupThatFailsSaysWhatIsLeft: a removal that fails names the
// container left behind, so an operator can remove it.
func TestRun_ACleanupThatFailsSaysWhatIsLeft(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 125, runLeftover: "created", rmExit: 1})
	if err := runWeb(t, h); err == nil || !strings.Contains(err.Error(), "removing it failed") || !strings.Contains(err.Error(), fakeContainerID) {
		t.Fatalf("err = %v, want the leftover container named", err)
	}
}

// TestRun_ARunThatPrintsNoIDIsRefused: the undo is pinned to the id docker
// run prints, which is text from the device, so anything other than a full
// container id is refused rather than recorded as the identity guard.
func TestRun_ARunThatPrintsNoIDIsRefused(t *testing.T) {
	for _, stdout := range []string{"", "not-an-id", strings.Repeat("A", 64), "$(id)"} {
		h := newHarness(t, containerFixture{runStdout: &stdout})
		if err := runWeb(t, h); err == nil || !strings.Contains(err.Error(), "printed no container id") {
			t.Fatalf("stdout %q: err = %v, want it refused", stdout, err)
		}
		if _, _, ok := inverseOf(h.rc); ok {
			t.Fatalf("stdout %q: an undo was recorded without a container id", stdout)
		}
	}
}

// TestRun_OutputPastTheBoundIsCutOff: a docker that writes without end is cut
// off at the method's bound and the run fails, rather than its output growing
// the memory of the process running the task.
func TestRun_OutputPastTheBoundIsCutOff(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 1, runFlood: true})
	if err := runWeb(t, h); err == nil || !strings.Contains(err.Error(), "passed its bound") {
		t.Fatalf("err = %v, want the output bound named", err)
	}
}

// TestRun_FailureDetailIsBoundedAndEscaped: docker's own failure text reaches
// the error escaped and cut, so a device cannot write terminal control
// sequences through it or make an error of any size.
func TestRun_FailureDetailIsBoundedAndEscaped(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 1, runFailMessage: "bad \x1b[31m" + strings.Repeat("y", 4000)})
	err := runWeb(t, h)
	if err == nil {
		t.Fatal("a failed run was reported as a success")
	}
	if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), `\u001b[31m`) {
		t.Errorf("err = %q, want the escape sequence escaped", err)
	}
	if !strings.Contains(err.Error(), "more bytes)") || len(err.Error()) > 1500 {
		t.Errorf("err is %d bytes, want docker's text cut", len(err.Error()))
	}
}

// TestRun_ALeftoverWithNoUsableIDIsNamedAndLeft: the id docker inspect gives
// is text from the device that would become a command's operand, so a
// leftover whose id is not a full container id is named and left, never
// removed by it.
func TestRun_ALeftoverWithNoUsableIDIsNamedAndLeft(t *testing.T) {
	h := newHarness(t, containerFixture{runExit: 125, runLeftover: "created", containerID: "$(id)"})
	if err := runWeb(t, h); err == nil || !strings.Contains(err.Error(), "gave no usable id") {
		t.Fatalf("err = %v, want the unusable id named", err)
	}
	if calls := h.invocations(t); len(calls) != 1 {
		t.Fatalf("invocations = %v, want the run alone", calls)
	}
}

// TestRun_AWarningThatCannotBeRecordedFailsTheTask: when the read back after a
// create fails, the warning saying so is the only record of it, so failing to
// record it fails the task rather than reporting a clean run.
func TestRun_AWarningThatCannotBeRecordedFailsTheTask(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 4)
	h.rc.failOnKey = sdk.StatWarnings
	if err := runWeb(t, h); err == nil || !strings.Contains(err.Error(), "warnings") {
		t.Fatalf("err = %v, want the failed warning surfaced", err)
	}
}

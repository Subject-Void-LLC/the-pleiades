// Tests that the docker CLI methods act only on the engine onboarding pinned
// for the device.
package docker_test

import (
	"context"
	"strings"
	"testing"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
)

// allMethods is every docker CLI method, run and check.
func allMethods() map[string]methodFunc {
	return map[string]methodFunc{
		"run": dockermod.Run, "check run": dockermod.CheckRun,
		"stop": dockermod.Stop, "check stop": dockermod.CheckStop,
		"remove": dockermod.Remove, "check remove": dockermod.CheckRemove,
	}
}

// TestMethods_RefuseAnotherEngine: the docker CLI acts on whatever engine the
// login's own configuration points at, so each method compares the engine's
// identity with the one onboarding pinned, and changes nothing on another.
func TestMethods_RefuseAnotherEngine(t *testing.T) {
	for label, m := range allMethods() {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t, containerFixture{exists: true, status: "running", otherEngine: true})
			_, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
			if err == nil || !strings.Contains(err.Error(), "is not the one onboarding pinned") {
				t.Fatalf("err = %v, want another engine refused", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("a method acted on another engine: %v", calls)
			}
		})
	}
}

// TestMethods_RefuseADeviceWithNoPinnedEngine: a device onboarding never pinned
// an engine for cannot be told from another, so it is refused, naming the
// command that pins one.
func TestMethods_RefuseADeviceWithNoPinnedEngine(t *testing.T) {
	for label, m := range allMethods() {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t, running)
			h.device.(*target).engineID = ""
			_, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
			if err == nil || !strings.Contains(err.Error(), "pleiades onboard") {
				t.Fatalf("err = %v, want the unpinned device refused", err)
			}
		})
	}
}

// TestMethods_ConnectionDiesReadingTheEngine: a connection lost while asking
// docker info for the engine's identity fails the task before anything is
// inspected or changed.
func TestMethods_ConnectionDiesReadingTheEngine(t *testing.T) {
	for label, m := range allMethods() {
		t.Run(label, func(t *testing.T) {
			h := newHarnessBudgeted(t, running, 0)
			if _, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"})); err == nil {
				t.Fatal("expected a connection failure reading the engine's identity")
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("a method acted without reading its engine: %v", calls)
			}
		})
	}
}

// TestMethods_ADockerInfoThatFailsIsAFailure: an engine whose identity cannot
// be read is not assumed to be the pinned one; the task fails, naming docker
// info's own answer, before anything is inspected.
func TestMethods_ADockerInfoThatFailsIsAFailure(t *testing.T) {
	for label, m := range allMethods() {
		t.Run(label, func(t *testing.T) {
			h := newHarness(t, containerFixture{exists: true, status: "running", infoFails: true})
			_, err := m(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx:1.27"}))
			if err == nil || !strings.Contains(err.Error(), "could not read the engine's identity") || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("err = %v, want docker info's failure named", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Fatalf("a method acted without its engine's identity: %v", calls)
			}
		})
	}
}

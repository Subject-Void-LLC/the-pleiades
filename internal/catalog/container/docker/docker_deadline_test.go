// Tests that every docker call ends at its deadline, saying so, rather than
// holding the task while a daemon does not answer.
package docker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
)

// TestCalls_EndAtTheirDeadline: with each call's deadline shortened, a daemon
// that does not answer one subcommand fails the task within that deadline,
// and the error names the call and that a change it was making may or may
// not have happened, never a bare cancellation.
func TestCalls_EndAtTheirDeadline(t *testing.T) {
	t.Cleanup(dockermod.SetCallTimeoutsForTest(200 * time.Millisecond))
	for _, tc := range []struct {
		label   string
		fixture containerFixture
		method  methodFunc
		params  map[string]any
	}{
		{"info", containerFixture{exists: true, status: "running", hang: "info"}, dockermod.Stop, nil},
		{"inspect", containerFixture{exists: true, status: "running", hang: "inspect"}, dockermod.Stop, nil},
		{"image inspect", containerFixture{hang: "image"}, dockermod.Run, nil},
		{"run", containerFixture{hang: "run"}, dockermod.Run, nil},
		{"start", containerFixture{exists: true, status: "exited", hang: "start"}, dockermod.Run, nil},
		{"stop", containerFixture{exists: true, status: "running", hang: "stop"}, dockermod.Stop, nil},
		{"rm", containerFixture{exists: true, status: "exited", hang: "rm"}, dockermod.Remove, nil},
		{"pull", containerFixture{imageAbsent: true, hang: "pull", hangSeconds: 3}, dockermod.Run, map[string]any{"pull_timeout": 1}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h := newHarness(t, tc.fixture)
			params := map[string]any{"name": "web", "image": "nginx:1.27"}
			for k, v := range tc.params {
				params[k] = v
			}
			start := time.Now()
			_, err := tc.method(context.Background(), h.rc, h.device, h.params(params))
			if err == nil || !strings.Contains(err.Error(), "did not answer within") {
				t.Fatalf("err = %v, want the deadline named", err)
			}
			if took := time.Since(start); took > 4*time.Second {
				t.Fatalf("the call took %s, past its deadline", took)
			}
		})
	}
}

// TestCalls_TheTasksOwnDeadlineIsItsOwn: when the task's context ends first,
// the error is that context's, not a call deadline that did not fire.
func TestCalls_TheTasksOwnDeadlineIsItsOwn(t *testing.T) {
	h := newHarness(t, containerFixture{exists: true, status: "running", hang: "stop"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := dockermod.Stop(ctx, h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err == nil || strings.Contains(err.Error(), "did not answer within") || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v, want the task's own deadline", err)
	}
}

// TestRun_PullTimeoutMustBePositiveSeconds refuses a pull_timeout that is
// not a whole number of seconds above zero.
func TestRun_PullTimeoutMustBePositiveSeconds(t *testing.T) {
	for _, bad := range []any{0, -5, "soon", 1.5} {
		h := newHarness(t, absent)
		if err := runWith(t, h, map[string]any{"pull_timeout": bad}); err == nil || !strings.Contains(err.Error(), "pull_timeout") {
			t.Fatalf("pull_timeout %v: err = %v, want it refused", bad, err)
		}
	}
}

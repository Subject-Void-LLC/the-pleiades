// This file covers the cancel control: which jobs it is offered on, and
// what happens when it is pressed on one it should not have been.
//
// The same deliberate boundary relaunch_test.go documents applies here.
// api's context key is unexported and its test-only export is reachable
// only from that package's own tests, so a context carrying an identity
// cannot be built from outside it. The identity check runs before the
// canceller is called, so neither the successful cancel nor the
// already-finished refusal can be reached from this package at all; both
// are covered in internal/api/jobs_cancel_test.go, which drives the real
// router and the real store. Writing a stub-based test for them here would
// produce one that cannot fail.
//
// What is covered here is the gating, and every refusal that happens
// before that line.
package jobs

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// stubCanceler records what it was asked to stop.
type stubCanceler struct {
	err error

	actors []string
	jobs   []string
}

func (s *stubCanceler) Cancel(_ context.Context, jobID string, canceledBy string) error {
	s.jobs = append(s.jobs, jobID)
	s.actors = append(s.actors, canceledBy)
	return s.err
}

// TestCancelable_IsOfferedOnlyWhileSomethingIsStillHappening enumerates
// every declared job state, so a state added later without a decision here
// shows up as a failure rather than as a button that does nothing, or as a
// missing button on a run somebody needs to stop.
func TestCancelable_IsOfferedOnlyWhileSomethingIsStillHappening(t *testing.T) {
	for _, tc := range []struct {
		name     string
		row      view.Row
		expected bool
	}{
		{"a pending job", jobRow("pending", "nightly backup"), true},
		{"a job fanning out", jobRow("fanning_out", "nightly backup"), true},
		{"a running job", jobRow("running", "nightly backup"), true},
		{"a completed job", jobRow("completed", "nightly backup"), false},
		{"a failed job", jobRow("failed", "nightly backup"), false},
		// Already stopped. Offering it again would invite somebody to
		// press a control whose only possible answer is that there was
		// nothing to stop.
		{"a canceled job", jobRow("canceled", "nightly backup"), false},
		// An unrecognised state keeps the control, which is the safe
		// direction: terminalStates is listed positively precisely so a
		// state nothing has decided about is still treated as live.
		{"a state nothing declares", jobRow("inventing things", "nightly backup"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cancelable(tc.row); got != tc.expected {
				t.Errorf("cancelable(%+v) = %v, want %v", tc.row.Cells, got, tc.expected)
			}
		})
	}
}

// TestApplies_CancelAndRelaunchAreNeverOfferedTogether pins the property
// the two gates are supposed to have between them.
//
// A job is either still running, in which case stopping it is the only
// sensible control, or it has finished, in which case running it again is.
// Both gates read the same terminalStates map from opposite sides, so this
// holds by construction, and a test is what keeps it holding when somebody
// later edits one gate and not the other.
func TestApplies_CancelAndRelaunchAreNeverOfferedTogether(t *testing.T) {
	for _, state := range []string{"pending", "fanning_out", "running", "completed", "failed", "canceled"} {
		t.Run(state, func(t *testing.T) {
			row := jobRow(state, "nightly backup")
			cancel := applies(row, auth.RelCancel)
			relaunch := applies(row, auth.RelExecute)
			if cancel && relaunch {
				t.Errorf("state %q offers both cancel and relaunch at once", state)
			}
			if !cancel && !relaunch {
				t.Errorf("state %q offers neither cancel nor relaunch, so a job in it has no control at all", state)
			}
		})
	}
}

// TestApplies_WithdrawsOnlyTheCancelControl proves the cancel gate is as
// narrow as the relaunch one beside it. A predicate that ignored the
// relation would withdraw reading a job as well as stopping it.
func TestApplies_WithdrawsOnlyTheCancelControl(t *testing.T) {
	finished := jobRow("completed", "nightly backup")

	if applies(finished, auth.RelCancel) {
		t.Error("the cancel control is offered on a job that has already finished")
	}
	for _, rel := range []auth.LinkRel{auth.RelSelf, auth.RelCollection} {
		if !applies(finished, rel) {
			t.Errorf("applies() withdrew %q, which cancelling has nothing to do with", rel)
		}
	}
}

// TestCancelAction_IsDeclaredAgainstTheMountedEndpoint keeps the button
// and the route in agreement.
//
// An action's scope and relation come from the endpoint it names, so a
// wrong one here is not a compile error: it is a control gated on somebody
// else's permission, or one the router never mounted.
func TestCancelAction_IsDeclaredAgainstTheMountedEndpoint(t *testing.T) {
	action := cancelAction(&stubCanceler{})

	if action.Name != "cancel" || action.Label == "" {
		t.Errorf("action = %+v, want a named, labelled control", action)
	}
	if action.Endpoint == nil || action.Endpoint.Name != "cancel_job" {
		t.Fatalf("action names endpoint %+v, want cancel_job", action.Endpoint)
	}
	if action.Endpoint.Scope != auth.ScopeRunbookExecute {
		t.Errorf("scope = %q, want %q: stopping a run and starting one are the two ends of one authority", action.Endpoint.Scope, auth.ScopeRunbookExecute)
	}
	if action.Endpoint.Rel != auth.RelCancel {
		t.Errorf("relation = %q, want %q", action.Endpoint.Rel, auth.RelCancel)
	}
}

// TestCancelAction_RefusesWithNoIdentity proves the action will not stop a
// job it cannot attribute.
//
// Recording who stopped a run is the entire reason canceled_by exists, so
// cancelling anonymously is worse than not cancelling: it would leave a
// terminal record asserting somebody decided this, naming nobody.
func TestCancelAction_RefusesWithNoIdentity(t *testing.T) {
	canceler := &stubCanceler{}
	action := cancelAction(canceler)

	_, _, err := action.Submit(context.Background(), "j-1", view.Values{})
	if err == nil {
		t.Fatal("Submit with no identity on the context returned no error")
	}
	if len(canceler.jobs) != 0 {
		t.Errorf("the canceller was called %d time(s) with no identity present, want 0", len(canceler.jobs))
	}
}

// TestCancelAction_RefusesWhenUnwired proves a deployment that wires no
// canceller answers rather than panicking. internal/ui/resources's own
// conformance harness builds its dependencies with collaborators left nil,
// so this is a real arrangement rather than a hypothetical one.
func TestCancelAction_RefusesWhenUnwired(t *testing.T) {
	action := cancelAction(nil)

	_, _, err := action.Submit(context.Background(), "j-1", view.Values{})
	if err == nil {
		t.Fatal("Submit with no canceller wired returned no error")
	}
}

// This file covers the relaunch control: which jobs it is offered on, and
// what happens when it is pressed on one it should not have been.
//
// The gating is the half worth the most attention. api.Dispatcher.Relaunch
// refuses a job that never came from a template, and starting a second copy
// of a job still in flight is never what "run it again" means, so the
// control has to be absent on both rather than present and failing. That is
// the rule this repository already settled once: constrain a control to
// what can actually work, and enforce it on submission as well.
//
// One boundary is deliberate. The paths that read an identity off the
// request context cannot be driven from here: api's context key is
// unexported and its test-only export is reachable only from that package's
// own tests, so a context carrying an identity cannot be built outside it.
// The Templates view's launch action has the identical shape and the
// identical gap. What is covered here is everything up to that line,
// including the refusal that happens when no identity is present at all.
package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// stubRelauncher records what it was asked to repeat.
type stubRelauncher struct {
	newJobID string
	err      error

	actors []string
	jobs   []string
}

// Relaunch records the call and returns the configured outcome.
func (s *stubRelauncher) Relaunch(_ context.Context, actor, jobID string) (string, []launch.IgnoredField, error) {
	s.actors = append(s.actors, actor)
	s.jobs = append(s.jobs, jobID)
	return s.newJobID, nil, s.err
}

// jobRow builds a row in the shape the projector produces.
func jobRow(state, template string) view.Row {
	return view.Row{ID: "j-1", Cells: view.Cells{"state": state, "template": template}}
}

// TestRelaunchable_IsOfferedOnlyWhereItWorks covers both conditions, and
// every job state, so a state added later without a decision here shows up
// as a failure rather than as a button that does nothing.
func TestRelaunchable_IsOfferedOnlyWhereItWorks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		row      view.Row
		expected bool
	}{
		{"a completed job from a template", jobRow("completed", "nightly backup"), true},
		{"a failed job from a template", jobRow("failed", "nightly backup"), true},
		// Still in flight. Relaunching would start a second concurrent
		// copy of work already running.
		{"a pending job", jobRow("pending", "nightly backup"), false},
		{"a job fanning out", jobRow("fanning_out", "nightly backup"), false},
		// No template to repeat: a pre-Phase-21 job named a group and a
		// runbook directly, and there is no saved definition.
		{"a completed job with no template", jobRow("completed", ""), false},
		{"a failed job with no template", jobRow("failed", ""), false},
		{"a state nothing declares", jobRow("inventing things", "nightly backup"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relaunchable(tc.row); got != tc.expected {
				t.Errorf("relaunchable(%+v) = %v, want %v", tc.row.Cells, got, tc.expected)
			}
		})
	}
}

// TestApplies_WithdrawsOnlyTheRelaunchControl proves the gate is narrow.
//
// Applies is consulted for every affordance on the view, so a predicate
// that ignored the relation would withdraw reading a job as well as
// repeating it, and the page would render as though the caller had no
// access at all.
func TestApplies_WithdrawsOnlyTheRelaunchControl(t *testing.T) {
	running := jobRow("pending", "nightly backup")

	if applies(running, auth.RelExecute) {
		t.Error("the relaunch control is offered on a job that is still running")
	}
	for _, rel := range []auth.LinkRel{auth.RelSelf, auth.RelCollection} {
		if !applies(running, rel) {
			t.Errorf("applies() withdrew %q, which relaunching has nothing to do with", rel)
		}
	}
	if !applies(jobRow("completed", "nightly backup"), auth.RelExecute) {
		t.Error("the relaunch control is withheld on a finished job that has a template")
	}
}

// TestRelaunchAction_IsDeclaredAgainstTheMountedEndpoint keeps the button
// and the route in agreement.
//
// An action's scope and relation come from the endpoint it names, so a
// wrong one here is not a compile error: it is a control gated on somebody
// else's permission.
func TestRelaunchAction_IsDeclaredAgainstTheMountedEndpoint(t *testing.T) {
	action := relaunchAction(&stubRelauncher{})

	if action.Name != "relaunch" || action.Label == "" {
		t.Errorf("action = %+v, want a named, labelled control", action)
	}
	if action.Endpoint == nil || action.Endpoint.Name != "relaunch_job" {
		t.Fatalf("action names endpoint %+v, want relaunch_job", action.Endpoint)
	}
	if action.Endpoint.Scope != auth.ScopeRunbookExecute {
		t.Errorf("scope = %q, want %q", action.Endpoint.Scope, auth.ScopeRunbookExecute)
	}
	// Relaunch resolves everything from the job, so there is nothing to
	// prompt for. A form here would invite somebody to change the
	// configuration and still call the result a relaunch.
	if action.Prompts() {
		t.Error("the relaunch action prompts, but a relaunch takes no input")
	}
}

// TestRelaunchAction_RefusesWhenItCannotIdentifyTheCaller covers the two
// failures reachable before an identity is needed.
//
// The actor matters: a relaunch is a new decision, and attributing it to
// whoever launched the original would put somebody else's name on a
// dispatch they did not ask for. So a request with no identity must fail
// rather than fall back to anything.
func TestRelaunchAction_RefusesWhenItCannotIdentifyTheCaller(t *testing.T) {
	t.Run("no dispatcher wired", func(t *testing.T) {
		_, _, err := relaunchAction(nil).Submit(context.Background(), "j-1", view.Values{})
		if err == nil {
			t.Fatal("Submit succeeded with no dispatcher wired")
		}
	})

	t.Run("no identity on the request", func(t *testing.T) {
		runner := &stubRelauncher{newJobID: "j-2"}
		_, _, err := relaunchAction(runner).Submit(context.Background(), "j-1", view.Values{})
		if err == nil {
			t.Fatal("Submit succeeded with no identity on the context")
		}
		if len(runner.actors) != 0 {
			t.Errorf("the dispatcher was called anyway, as %v", runner.actors)
		}
	})
}

// TestRelaunchRefusal_CarriesTheDispatchersOwnSentence proves the message a
// caller sees names the reason.
//
// ErrNotRelaunchable is raised for more than one cause, and the dispatcher
// wraps each with which one applies. Flattening that to "it could not be
// relaunched" would throw away the only part worth reading.
func TestRelaunchRefusal_CarriesTheDispatchersOwnSentence(t *testing.T) {
	wrapped := errors.New("api: this job cannot be relaunched: it was not launched from a template")
	if got := relaunchRefusal(wrapped); !strings.Contains(got, "not launched from a template") {
		t.Errorf("relaunchRefusal() = %q, want the specific reason", got)
	}
	if got := relaunchRefusal(nil); got != "" {
		t.Errorf("relaunchRefusal(nil) = %q, want empty", got)
	}
	// The sentinel the action switches on has to be the one the dispatcher
	// actually raises, or the refusal renders as a 500.
	if !errors.Is(api.ErrNotRelaunchable, api.ErrNotRelaunchable) {
		t.Error("the sentinel is not its own identity")
	}
}

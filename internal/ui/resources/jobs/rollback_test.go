// This file covers the rollback control: which jobs it is offered on, and
// how a refused plan comes back onto the form. The identity boundary
// relaunch_test.go describes applies here too.
package jobs

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file" // file.remove, the run setting the form names
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds" // the registered kinds the gate reads
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestRollbackable_IsOfferedOnlyWhereItCanWork covers what the Controller
// refuses whatever the form says: a job still running, a check, a
// playbook job, and a rollback.
func TestRollbackable_IsOfferedOnlyWhereItCanWork(t *testing.T) {
	row := func(state, kind, mode, of string) view.Row {
		return view.Row{ID: "j-1", Cells: view.Cells{"state": state, "kind": kind, "mode": mode, "rollback_of": of}}
	}
	for _, tc := range []struct {
		name string
		row  view.Row
		want bool
	}{
		{"a completed runbook job", row("completed", "runbook", "execute", ""), true},
		{"a failed one", row("failed", "runbook", "execute", ""), true},
		{"a canceled one", row("canceled", "runbook", "execute", ""), true},
		{"one with no kind recorded", row("completed", "", "execute", ""), true},
		{"a running one", row("running", "runbook", "execute", ""), false},
		{"a pending one", row("pending", "runbook", "execute", ""), false},
		{"a check", row("completed", "runbook", "check", ""), false},
		{"an unreadable mode", row("completed", "runbook", "unreadable", ""), false},
		{"a playbook job", row("completed", "playbook", "execute", ""), false},
		{"a rollback", row("completed", "runbook", "execute", "j-0"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rollbackable(tc.row); got != tc.want {
				t.Errorf("rollbackable = %v, want %v", got, tc.want)
			}
			if got := applies(tc.row, auth.RelRollback); got != tc.want {
				t.Errorf("applies(rollback) = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRollbackProblems_WritesEachUnderTheFieldThatAcceptsIt proves a
// refusal comes back as the form an operator acts on: each problem under
// its list, saying what to add, and one nothing accepts at the top.
func TestRollbackProblems_WritesEachUnderTheFieldThatAcceptsIt(t *testing.T) {
	errs := rollbackProblems([]api.RollbackProblem{
		{Node: "tasks[1]", Device: "web1", Reason: "its undo was not recorded in full", Field: "leave", Value: "tasks[1]"},
		{Reason: "job j-9 changed web1 after this job began", Field: "despite_job", Value: "j-9"},
		{Node: "tasks[0]", Device: "web1", Reason: "node tasks[0] runs file.directory"},
		{Node: "tasks[2]", Device: "web1", Reason: "its undo was recorded under other declarations", Field: "allow_skew", Value: "tasks[2]"},
	})
	if got := errs["allow_skew"]; len(got) != 1 || !strings.Contains(got[0], "Add tasks[2] here") {
		t.Errorf("allow_skew: %q", got)
	}
	if got := errs["leave"]; len(got) != 1 || !strings.HasPrefix(got[0], "tasks[1] on web1: its undo") || !strings.HasSuffix(got[0], "Add tasks[1] here to accept it.") {
		t.Errorf("leave: %q", got)
	}
	if got := errs["despite_job"]; len(got) != 1 || !strings.Contains(got[0], "Add j-9 here") {
		t.Errorf("despite_job: %q", got)
	}
	if got := errs[""]; len(got) != 1 || !strings.HasPrefix(got[0], "tasks[0] on web1:") {
		t.Errorf("the problem nothing accepts: %q", got)
	}
	for name := range errs {
		found := name == ""
		for _, f := range rollbackFields {
			found = found || f.Name == name
		}
		if !found {
			t.Errorf("a problem was written under %q, which the form has no field for", name)
		}
	}
}

// TestRollbackAction_RefusesWithoutAnIdentity is the path this package can
// reach: no identity, no rollback, and nothing asked of the Controller.
func TestRollbackAction_RefusesWithoutAnIdentity(t *testing.T) {
	called := false
	action := rollbackAction(rollbackerFunc(func(context.Context, string, string, api.RollbackRequest) (string, error) {
		called = true
		return "j-2", nil
	}))
	if _, _, err := action.Submit(context.Background(), "j-1", view.Values{}); err == nil || called {
		t.Errorf("err %v, called %v: want a refusal before the Controller is asked", err, called)
	}
	if _, _, err := rollbackAction(nil).Submit(context.Background(), "j-1", view.Values{}); err == nil {
		t.Error("an unwired rollback did not say so")
	}
}

// rollbackerFunc adapts a function to Rollbacker.
type rollbackerFunc func(ctx context.Context, actor, jobID string, req api.RollbackRequest) (string, error)

// Rollback calls f.
func (f rollbackerFunc) Rollback(ctx context.Context, actor, jobID string, req api.RollbackRequest) (string, error) {
	return f(ctx, actor, jobID, req)
}

// TestRollbackRunSettings_HoldsEachToItsMethod proves the form's method
// settings are read as <method>.<parameter>=<value> and held to the method,
// each refusal written under the field without echoing what was typed.
func TestRollbackRunSettings_HoldsEachToItsMethod(t *testing.T) {
	values := func(settings string) view.Values {
		v, _ := view.NewValues(rollbackFields, url.Values{rollbackSettings: {settings}}, false)
		return v
	}
	got, errs := rollbackRunSettings(values("file.remove.check_forbidden=warn"))
	if errs != nil || got["file.remove.check_forbidden"] != "warn" {
		t.Errorf("a valid setting: %v, %v", got, errs)
	}
	if got, errs := rollbackRunSettings(values("")); errs != nil || got != nil {
		t.Errorf("none: %v, %v", got, errs)
	}
	for typed, want := range map[string]string{
		"hunter2":                           "each entry is <method>.<parameter>=<value>",
		"=hunter2":                          "each entry is <method>.<parameter>=<value>",
		"file.remove.check_forbidden=maybe": "must be one of refuse, warn",
	} {
		_, errs := rollbackRunSettings(values(typed))
		msg := strings.Join(errs[rollbackSettings], " ")
		if !strings.Contains(msg, want) || strings.Contains(msg, "hunter2") || strings.Contains(msg, "maybe") {
			t.Errorf("%q: %q, want it refused saying %q and not echoed", typed, msg, want)
		}
	}
}

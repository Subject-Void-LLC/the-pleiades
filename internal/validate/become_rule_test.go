// Package validate_test: BecomeRule, the refusal of a become that no method
// could honor.
package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestBecomeRule: a task that becomes an account is held to a method that
// reaches its device over SSH and does not decline become; every other
// task, with or without become, passes.
func TestBecomeRule(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	none := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	reg := func(name string, transports []string, noBecome string) {
		t.Helper()
		if err := collection.Register(collection.Descriptor{Name: name, Invoke: none, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "fixture", ReadOnly: true},
			SupportedTransports: transports, NoBecomeReason: noBecome,
			ExecutionContext: collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	reg("bt.over_ssh", []string{capability.TransportSSH}, "")
	reg("bt.over_api", []string{capability.TransportHTTPS}, "")
	reg("bt.login_question", []string{capability.TransportSSH}, "its question is the login itself")

	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(`id: b
hosts: h
tasks:
  - name: ok
    bt.over_ssh: {}
    become: true
  - name: no become at all
    bt.over_api: {}
  - name: api
    bt.over_api: {}
    become: true
  - name: login
    bt.login_question: {}
    become: true
  - name: builtin
    noop: {}
    become: true
  - name: off
    bt.over_api: {}
    become: false
`))
	if err != nil {
		t.Fatal(err)
	}
	findings := validate.BecomeRule(validate.WorldView{DAG: dag})
	got := map[string]string{}
	for _, f := range findings {
		got[f.Node] = f.Message
	}
	var nodes int
	for id, task := range dag.Nodes {
		msg, refused := got[id]
		switch task.Name {
		case "ok", "no become at all", "off":
			if refused {
				t.Errorf("task %q was refused: %s", task.Name, msg)
			}
		case "api":
			nodes++
			if !strings.Contains(msg, "does not reach its device over SSH") {
				t.Errorf("api: %q", msg)
			}
		case "login":
			nodes++
			if !strings.Contains(msg, "its question is the login itself") {
				t.Errorf("login: %q", msg)
			}
		case "builtin":
			nodes++
			if !strings.Contains(msg, "not a Collection method") {
				t.Errorf("builtin: %q", msg)
			}
		}
	}
	if nodes != 3 || len(findings) != 3 {
		t.Errorf("%d findings for the 3 tasks that cannot become: %+v", len(findings), findings)
	}
}

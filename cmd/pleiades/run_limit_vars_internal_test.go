// Tests that the run's own validation under --limit judges a variable a
// device brings as validate does without one: run 10's third report.
package main

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestLimit_AVariableOnlyAnotherHostDefinesIsJudgedAsValidateJudgesIt: run
// 10's third report. A template reading a host_vars name only the hub
// defines, inside an {% if %} only the hub takes, passed validate and was
// refused by the run's own validation under --limit on a spoke, though the
// spoke renders it without the name. A device's variables count for every
// device the target names in the inventory, limited or not; a name no device
// defines is still refused.
func TestLimit_AVariableOnlyAnotherHostDefinesIsJudgedAsValidateJudgesIt(t *testing.T) {
	world := func() validate.WorldView {
		w := wanWorld()
		for _, item := range w.Items {
			vars := map[string]any{"role": "spoke"}
			if item.Name() == "core1" {
				vars = map[string]any{"role": "hub", "lan_net": "10.0.0.0/24"}
			}
			item.(*inventorytest.Stub).Props = map[string]any{inventory.VarsProperty: vars}
		}
		return w
	}
	for name, wantRefused := range map[string]bool{"lan_net": false, "nobody_has_it": true} {
		dag := buildRunbook(t, `id: branch
hosts: c8kv
tasks:
  - name: role-dependent show
    net.ios.command:
      command: "show {% if vars.role == 'hub' %}ip route {{ vars.`+name+` | cli_token }}{% else %}version{% endif %}"
`)
		for _, limit := range []string{"", "br1"} {
			w := world()
			w.DAG = dag
			if limit != "" {
				l, _ := engine.ParseLimit(limit)
				var err error
				if w, err = applyLimit(w, dag, l, limit); err != nil {
					t.Fatal(err)
				}
			}
			w.VariablesKnown = true
			if refused := len(validate.VarsRule(w)) > 0; refused != wantRefused {
				t.Errorf("vars.%s under --limit %q: refused %v, want %v: %+v", name, limit, refused, wantRefused, validate.VarsRule(w))
			}
		}
	}
}

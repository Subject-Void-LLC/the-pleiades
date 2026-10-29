// Tests of the variable rules (Phase 87): names, undefined references across
// every scope, and the shadowing warning, each through the real validate entry
// point on a runbook compiled from YAML.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// varsWorld builds a world for a runbook with one device, web1, carrying
// deviceVars as its vars property when it is not nil, and the run's extra
// variables given.
func varsWorld(t *testing.T, runbook string, deviceVars map[string]any, extra map[string]any) validate.WorldView {
	t.Helper()
	props := map[string]inventory.PropertyValue{}
	if deviceVars != nil {
		props[inventory.VarsProperty] = deviceVars
	}
	return validate.WorldView{
		Items:          []inventory.InventoryItem{&inventorytest.Stub{StubID: "d1", StubName: "web1", Props: props, StubState: inventory.StateActive}},
		DAG:            yamlDAG(t, runbook),
		Variables:      extra,
		VariablesKnown: true,
		VariablesFlag:  "--extra-vars",
	}
}

// messages returns the report's findings of rule as strings.
func messages(report validate.Report, rule string) []string {
	var out []string
	for _, f := range report.Findings {
		if f.RuleName == rule {
			out = append(out, f.Node+": "+f.Message)
		}
	}
	return out
}

// TestVarsRule_Scopes: a name is defined by the runbook, a block, a task, a
// set_fact before the reader, a global set_fact before it, a device it
// targets, or the run; it is refused when defined nowhere, only by a set_fact
// further on, or only in another block's vars:.
func TestVarsRule_Scopes(t *testing.T) {
	cases := []struct {
		name      string
		runbook   string
		device    map[string]any
		extra     map[string]any
		want      []string // each a substring of exactly one finding, in order
		wantClean bool
	}{
		{name: "runbook vars", wantClean: true, runbook: `id: r
hosts: web1
vars: {site: lab}
tasks:
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "task vars", wantClean: true, runbook: `id: r
hosts: web1
tasks:
  - name: use
    vars: {site: lab}
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "block vars reach a task inside", wantClean: true, runbook: `id: r
hosts: web1
tasks:
  - name: group
    vars: {site: lab}
    block:
      - name: use
        noop: {msg: "{{ vars.site }}"}
`},
		{name: "set_fact before", wantClean: true, runbook: `id: r
hosts: web1
tasks:
  - set_fact: {site: lab}
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "global set_fact before", wantClean: true, runbook: `id: r
hosts: web1
tasks:
  - set_fact: {site: lab, global: true}
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "device vars", wantClean: true, device: map[string]any{"site": "lab"}, runbook: `id: r
hosts: web1
tasks:
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "extra vars", wantClean: true, extra: map[string]any{"site": "lab"}, runbook: `id: r
hosts: web1
tasks:
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "condition reads a defined name", wantClean: true, runbook: `id: r
hosts: web1
vars: {site: lab}
tasks:
  - name: use
    when: vars.site == 'lab'
    noop: {msg: x}
`},
		{name: "guards", wantClean: true, runbook: `id: r
hosts: web1
tasks:
  - name: defaulted
    noop: {msg: "{{ vars.site | default('x') }}"}
  - name: tested
    when: has(vars.site) && vars.site == 'lab'
    noop: {msg: x}
`},
		{name: "defined nowhere", want: []string{"tasks[0]", "no value is supplied for vars.site; give it with --extra-vars site=<value>"}, runbook: `id: r
hosts: web1
tasks:
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "condition reads an undefined name", want: []string{"condition: no value is supplied for vars.region"}, runbook: `id: r
hosts: web1
tasks:
  - name: use
    when: vars.region == 'x'
    noop: {msg: x}
`},
		{name: "set_fact only later", want: []string{"set by the set_fact at tasks[1], which does not run before this task"}, runbook: `id: r
hosts: web1
tasks:
  - name: use
    noop: {msg: "{{ vars.site }}"}
  - set_fact: {site: lab}
`},
		{name: "set_fact only in a later parallel sibling", want: []string{"set by the set_fact at tasks[0].parallel[1]"}, runbook: `id: r
hosts: web1
tasks:
  - parallel:
      - name: use
        noop: {msg: "{{ vars.site }}"}
      - set_fact: {site: lab}
`},
		{name: "only another block's vars", want: []string{"defined only in the vars: of tasks[0]"}, runbook: `id: r
hosts: web1
tasks:
  - name: first
    vars: {site: lab}
    block:
      - noop: {msg: x}
  - name: use
    noop: {msg: "{{ vars.site }}"}
`},
		{name: "rollback step", want: []string{"tasks[0].rollback[0]", "vars.undo_to"}, runbook: `id: r
hosts: web1
tasks:
  - name: change
    noop: {msg: x}
    rollback:
      - noop: {to: "{{ vars.undo_to }}"}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := validate.Validate(varsWorld(t, tc.runbook, tc.device, tc.extra))
			got := messages(report, "vars")
			if tc.wantClean {
				if len(got) != 0 {
					t.Fatalf("findings = %v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("findings = %v, want one", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0], w) {
					t.Errorf("finding %q does not contain %q", got[0], w)
				}
			}
		})
	}
}

// TestVarsRule_NotKnownIsQuiet: a caller that does not know the run's
// variables (the Controller, the Runner) is told nothing about a name that
// could be one of them.
func TestVarsRule_NotKnownIsQuiet(t *testing.T) {
	world := varsWorld(t, "id: r\nhosts: web1\ntasks:\n  - noop: {msg: \"{{ vars.site }}\"}\n", nil, nil)
	world.VariablesKnown = false
	if got := messages(validate.Validate(world), "vars"); len(got) != 0 {
		t.Errorf("findings = %v", got)
	}
}

// TestVarNamesRule: a key that cannot be a variable name, a set_fact option
// that is not a boolean, and a set_fact that sets nothing are refused.
func TestVarNamesRule(t *testing.T) {
	cases := []struct {
		name    string
		runbook string
		want    string
	}{
		{"runbook key", "id: r\nhosts: web1\nvars: {bad-name: 1}\ntasks:\n  - noop: {msg: x}\n", `the runbook's vars:: "bad-name" is not a variable name`},
		{"task key", "id: r\nhosts: web1\ntasks:\n  - name: t\n    vars: {\"1x\": 1}\n    noop: {msg: x}\n", `"1x" is not a variable name`},
		{"fact name", "id: r\nhosts: web1\ntasks:\n  - set_fact: {\"a b\": 1}\n", `"a b" is not a variable name`},
		{"global not boolean", "id: r\nhosts: web1\ntasks:\n  - set_fact: {site: lab, global: \"yes\"}\n", "option global must be true or false"},
		{"no_override not boolean", "id: r\nhosts: web1\ntasks:\n  - set_fact: {site: lab, no_override: 1}\n", "option no_override must be true or false"},
		{"no fact", "id: r\nhosts: web1\ntasks:\n  - set_fact: {global: true}\n", "needs at least one variable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			world := varsWorld(t, tc.runbook, nil, nil)
			got := messages(validate.Validate(world), "varnames")
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("findings = %v, want one containing %q", got, tc.want)
			}
		})
	}

	// A block's bad key is reported once, not on every task it reached.
	block := "id: r\nhosts: web1\ntasks:\n  - vars: {\"bad-name\": 1}\n    block:\n      - noop: {msg: a}\n      - noop: {msg: b}\n"
	if got := messages(validate.Validate(varsWorld(t, block, nil, nil)), "varnames"); len(got) != 1 {
		t.Errorf("a block's bad key reported %d times: %v", len(got), got)
	}
}

// TestVarShadowWarnings: a variable a task reads that two scopes define is
// said once, as a warning, naming the scopes and the winner.
func TestVarShadowWarnings(t *testing.T) {
	runbook := `id: r
hosts: web1
vars: {site: lab, other: x}
tasks:
  - name: first
    vars: {site: edge}
    noop: {msg: "{{ vars.site }} {{ vars.other }}"}
  - name: second
    noop: {msg: "{{ vars.site }}"}
  - name: third
    noop: {msg: "{{ vars.site }}"}
  - name: group
    vars: {level: block}
    block:
      - name: inner
        vars: {level: task}
        noop: {msg: "{{ vars.level }}"}
`
	report := validate.Validate(varsWorld(t, runbook, map[string]any{"site": "device"}, nil))
	if report.HasErrors() {
		t.Fatalf("shadowing failed validation: %v", report.Findings)
	}
	var warnings []string
	for _, w := range report.Warnings {
		if w.RuleName == "vars" {
			warnings = append(warnings, w.Node+": "+w.Message)
		}
	}
	want := []string{
		"tasks[0]: task tasks[0] (name \"first\"): vars.site is defined in the runbook's vars: and in the device's vars (group_vars or host_vars) and in this task's vars:; the narrowest, this task's vars:, is the one read",
		"tasks[1]: task tasks[1] (name \"second\"): vars.site is defined in the runbook's vars: and in the device's vars (group_vars or host_vars); the narrowest, the device's vars (group_vars or host_vars), is the one read",
		"tasks[3].block[0]: task tasks[3].block[0] (name \"inner\"): vars.level is defined in the enclosing block's vars: and in this task's vars:; the narrowest, this task's vars:, is the one read",
	}
	if strings.Join(warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings\n%s\nwant\n%s", strings.Join(warnings, "\n"), strings.Join(want, "\n"))
	}
}

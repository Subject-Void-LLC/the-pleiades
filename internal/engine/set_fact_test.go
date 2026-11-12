// Tests of the set_fact decision's behaviours through the real Executor: a
// run's extra variables beat a set_fact of the same name, two devices setting
// one global to different values fail naming both (while a condition that
// picks one device keeps working), a secret reference in a value fails the
// run, and a second run starts with no facts at all. The undo half of the
// decision is in undo_vars_test.go.
package engine_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestSetFact_AnExtraVariableBeatsIt: a run's extra variable (a launch's, or
// pleiades run --extra-vars) is read in place of a set_fact of the same name,
// whether the fact is the device's, global, or set with no_override, in a
// parameter and in a condition alike. A fact under another name is read as
// set.
func TestSetFact_AnExtraVariableBeatsIt(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), map[string]any{"mine": "launch", "shared": "launch", "kept": "launch"}, `id: launch-wins
hosts: web
tasks:
  - set_fact:
      mine: fact
      other: fact
  - set_fact:
      shared: fact
      global: true
  - set_fact:
      kept: fact
      no_override: true
  - name: read
    when_cel: vars.mine == 'launch' && vars.shared == 'launch' && vars.kept == 'launch'
    rbtest.make:
      name: r
      note: "{{ vars.mine }} {{ vars.shared }} {{ vars.kept }} {{ vars.other }}"
`)
	want := []string{"a: launch launch launch fact", "b: launch launch launch fact"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}
}

// TestSetFact_TwoDevicesSettingOneGlobalDifferentlyFailNamingBoth: a global
// set_fact that two devices of one task set to different values fails on the
// second device, naming both, at run time. The same task setting one value on
// both is fine, and so is a condition that lets only one device set it, which
// a refusal at plan time would have broken: every device then reads that
// device's value.
func TestSetFact_TwoDevicesSettingOneGlobalDifferentlyFailNamingBoth(t *testing.T) {
	_, result := runVars(t, varsDevices(nil, nil), nil, `id: clash
hosts: web
tasks:
  - name: each device its own
    set_fact:
      leader: "{{ device.name }}"
      global: true
`)
	var failed []engine.NodeResult
	for _, n := range result.Nodes {
		if n.Err != nil {
			failed = append(failed, n)
		}
	}
	if len(failed) != 1 {
		t.Fatalf("%d devices failed, want exactly the second to set it: %+v", len(failed), result.Nodes)
	}
	msg := failed[0].Err.Error()
	for _, want := range []string{`"leader"`, `"a"`, `"b"`, "each device its own"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %s", msg, want)
		}
	}

	seen, result := runVars(t, varsDevices(nil, nil), nil, `id: one-picks
hosts: web
tasks:
  - name: the same value from both
    set_fact:
      region: east
      global: true
  - name: only a decides
    when_cel: device.name == 'a'
    set_fact:
      leader: "{{ device.name }}"
      global: true
  - rbtest.make:
      name: r
      note: "{{ vars.region }} {{ vars.leader }}"
`)
	want := []string{"a: east a", "b: east a"}
	if result.HasErrors() || !slices.Equal(seen, want) {
		t.Fatalf("saw %v, want %v, %+v", seen, want, result.Nodes)
	}
}

// TestSetFact_ASecretReferenceFailsTheRunToo: a runbook that reached the
// executor without validation still cannot copy a secret into a fact: the
// set_fact fails before it sets anything, and the task after it never runs.
func TestSetFact_ASecretReferenceFailsTheRunToo(t *testing.T) {
	seen, result := runVars(t, varsDevices(nil, nil), nil, `id: secret-fact
hosts: web
tasks:
  - name: keep the key
    set_fact:
      psk: "{{ secret.vpn.psk }}"
  - rbtest.make:
      name: r
      note: "{{ vars.psk }}"
`)
	if !result.HasErrors() || len(seen) != 0 {
		t.Fatalf("a set_fact of a secret reference ran: saw %v, %+v", seen, result.Nodes)
	}
	if msg := result.Nodes[0].Err.Error(); !strings.Contains(msg, "keep the key") || !strings.Contains(msg, "secret reference") {
		t.Errorf("error %q does not name the task and the reference", msg)
	}
}

// TestSetFact_ASecondRunStartsWithNoFacts: one Executor running a runbook
// twice starts each run with no facts, so the first task of the second run
// reads the default, not what the first run set.
func TestSetFact_ASecondRunStartsWithNoFacts(t *testing.T) {
	varsFixtures.Do(func() {
		registerRollbackFixtures(t)
		registerDeviceRootTypes()
	})
	seen := &paramsSeen{}
	x := engine.NewExecutor(varsDevices(nil, nil), factAware{seen, engine.NewBuiltinActionExecutor()}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	runbook := `id: rerun
hosts: web
tasks:
  - rbtest.make:
      name: r
      note: "{{ vars.mine | default('none') }} {{ vars.shared | default('none') }}"
  - set_fact:
      mine: set
  - set_fact:
      shared: set
      global: true
`
	for i := range 2 {
		result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
		if err != nil || result.HasErrors() {
			t.Fatalf("run %d: %v %+v", i, err, result.Nodes)
		}
	}
	slices.Sort(seen.seen)
	want := []string{"a: none none", "a: none none", "b: none none", "b: none none"}
	if !slices.Equal(seen.seen, want) {
		t.Errorf("saw %v, want %v", seen.seen, want)
	}
}

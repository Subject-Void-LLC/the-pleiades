// Tests of what validate says about a set_fact (the set_fact decision), through
// the real validate entry point on a runbook compiled from YAML: a warning when
// a run's extra variable has a name a set_fact sets, the refusal of a secret
// reference in a set_fact value, naming the task, and no plan-time refusal of
// a global that differs by device.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// warningsOf returns the report's warnings as "node: message" strings.
func warningsOf(report validate.Report) []string {
	var out []string
	for _, f := range report.Warnings {
		out = append(out, f.Node+": "+f.Message)
	}
	return out
}

// TestSetFact_AnExtraVariableOfTheSameNameWarns: an extra variable named like
// a set_fact's fact is warned about once per name and task, naming the flag
// it came in through; a fact under another name, an option, and a run with no
// extra variables or with unknown ones are quiet.
func TestSetFact_AnExtraVariableOfTheSameNameWarns(t *testing.T) {
	runbook := `id: r
hosts: web1
tasks:
  - name: pick
    set_fact:
      region: east
      zone: a
      global: true
  - name: use
    noop: {msg: "{{ vars.region }} {{ vars.zone }}"}
`
	report := validate.Validate(varsWorld(t, runbook, nil, map[string]any{"region": "west", "global": "x"}))
	got := warningsOf(report)
	var hits []string
	for _, w := range got {
		if strings.Contains(w, "set_fact sets") {
			hits = append(hits, w)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("warnings %q, want one about region", got)
	}
	for _, want := range []string{"tasks[0]", `"pick"`, "vars.region", "--extra-vars gives region", "never read"} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("warning %q does not say %q", hits[0], want)
		}
	}
	if len(report.Findings) != 0 {
		t.Errorf("a warning failed validation: %+v", report.Findings)
	}

	for name, world := range map[string]validate.WorldView{
		"another name":      varsWorld(t, runbook, nil, map[string]any{"other": "x"}),
		"no extra variable": varsWorld(t, runbook, nil, nil),
	} {
		for _, w := range warningsOf(validate.Validate(world)) {
			if strings.Contains(w, "set_fact sets") {
				t.Errorf("%s: warned %q", name, w)
			}
		}
	}
	unknown := varsWorld(t, runbook, nil, map[string]any{"region": "west"})
	unknown.VariablesKnown = false
	for _, w := range warningsOf(validate.Validate(unknown)) {
		if strings.Contains(w, "set_fact sets") {
			t.Errorf("unknown variables: warned %q", w)
		}
	}
}

// TestSetFact_ASecretReferenceIsRefusedNamingTheTask: a secret reference in a
// set_fact's value is refused before anything runs, however it is written (the
// plain reference, one inside a list, one joined to other text), and the
// refusal names the task and says why.
func TestSetFact_ASecretReferenceIsRefusedNamingTheTask(t *testing.T) {
	for name, value := range map[string]string{
		"plain":       `"{{ secret.vpn.psk }}"`,
		"in a list":   `["a", "{{ secret.vpn.psk }}"]`,
		"joined":      `"{{ 'key=' ~ secret.vpn.psk }}"`,
		"no spaces":   `"{{secret.vpn.psk}}"`,
		"with filter": `"{{ secret.vpn.psk | upper }}"`,
	} {
		t.Run(name, func(t *testing.T) {
			report := validate.Validate(varsWorld(t, `id: r
hosts: web1
tasks:
  - name: keep the key
    set_fact:
      psk: `+value+`
`, nil, nil))
			found := messages(report, "template")
			if len(found) != 1 {
				t.Fatalf("template findings %q, want one", found)
			}
			for _, want := range []string{"tasks[0]", `"keep the key"`, "parameter psk", "a set_fact may not hold a secret reference", "run journal"} {
				if !strings.Contains(found[0], want) {
					t.Errorf("finding %q does not say %q", found[0], want)
				}
			}
		})
	}

	// The same reference where its method takes one stays the method's.
	report := validate.Validate(varsWorld(t, `id: r
hosts: web1
tasks:
  - name: plain set_fact
    set_fact:
      label: "{{ 'a' ~ 'b' }}"
`, nil, nil))
	if found := messages(report, "template"); len(found) != 0 {
		t.Errorf("a set_fact with no secret was refused: %q", found)
	}
}

// TestSetFact_AGlobalReadingTheDeviceIsNotRefusedAtPlanTime: a global set_fact
// whose value differs by device is legal before the run, since a condition
// may let only one device set it; two devices that do set it differently
// fail when they run (the engine's TestSetFact_TwoDevicesSettingOneGlobal
// DifferentlyFailNamingBoth).
func TestSetFact_AGlobalReadingTheDeviceIsNotRefusedAtPlanTime(t *testing.T) {
	report := validate.Validate(varsWorld(t, `id: r
hosts: web1
tasks:
  - name: only one decides
    when_cel: device.name == 'web1'
    set_fact:
      leader: "{{ device.name }}"
      global: true
  - name: use
    noop: {msg: "{{ vars.leader }}"}
`, nil, nil))
	if len(report.Findings) != 0 {
		t.Errorf("findings %+v, want none", report.Findings)
	}
}

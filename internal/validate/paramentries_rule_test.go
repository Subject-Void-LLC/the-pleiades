// Tests that validation holds a structured parameter's entries to their
// declaration (collection.Param.Entries), with the real net.batfish.check,
// and refuses a project file where the run has no project.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/batfish"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
)

// buildRunbook compiles payload through the real builder, as the command
// line and the Runner do, so a YAML flow mapping splits exactly as it does
// for a user.
func buildRunbook(t *testing.T, payload string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	return dag
}

// checkTask is a net.batfish.check task with entries in place of the
// accept_unrecognized line, under hosts: batfish.
func checkTask(devices, accept string) string {
	return "id: gate\nhosts: batfish\ntasks:\n  - name: hold the candidate\n    net.batfish.check:\n" +
		"      devices:\n" + devices +
		"      assertions:\n        - ospf_established: true\n" + accept
}

// TestParamEntriesRule_RefusesFailurePattern584sSplitEntry is 584's own
// case: the documented accept_unrecognized entry, its reason unquoted, so
// the YAML flow mapping splits at the comma into a third key. Validation
// passed it before entries were declared and the run refused it; now
// validation refuses it, naming the entry, the stray key and the quoting
// that fixes it. Quoted, it passes.
func TestParamEntriesRule_RefusesFailurePattern584sSplitEntry(t *testing.T) {
	devices := "        - {name: r1, config: \"hostname r1\\n\"}\n"
	split := checkTask(devices, "      accept_unrecognized:\n        - {text: ip ospf authentication-key, reason: lab only, not on a real network}\n")
	findings := validate.ParamEntriesRule(validate.WorldView{DAG: buildRunbook(t, split)})
	if len(findings) != 1 || !strings.Contains(findings[0].Message, `accept_unrecognized[0] has a key "not on a real network"`) ||
		!strings.Contains(findings[0].Message, "quote a value holding one") {
		t.Fatalf("findings = %+v, want the split entry refused, naming its stray key", findings)
	}
	quoted := checkTask(devices, "      accept_unrecognized:\n        - {text: ip ospf authentication-key, reason: \"lab only, not on a real network\"}\n")
	if f := validate.ParamEntriesRule(validate.WorldView{DAG: buildRunbook(t, quoted)}); len(f) != 0 {
		t.Errorf("the quoted entry was refused: %+v", f)
	}
}

// TestParamEntriesRule_HoldsEachEntry: a key no entry takes, a required key
// left out, a value of another type or outside its choices, and a bad key
// in a scenario's own assertions, each refused by place; a template is left
// to the run.
func TestParamEntriesRule_HoldsEachEntry(t *testing.T) {
	for _, tc := range []struct {
		name, devices, extra, want string
	}{
		{"a key no device entry takes", "        - {name: r1, config: x, hostname: r1}\n", "", `devices[0] has a key "hostname"`},
		{"a device with no name", "        - {config: x}\n", "", "devices[0] has no name, which every entry needs"},
		{"a name that is not text", "        - {name: 7, config: x}\n", "", "devices[0].name is a number, and an entry's name is string"},
		{"a format outside its choices", "        - {name: r1, format: junos, config: x}\n", "", `devices[0].format is "junos", which is not one of frr, cisco_ios`},
		{"a scenario's assertion of no kind", "        - {name: r1, config: x}\n",
			"      scenarios:\n        - {name: r1 down, down_nodes: [r1], assertions: [{ospf_up: true}]}\n", `scenarios[0].assertions[0] has a key "ospf_up"`},
		{"an accept with no reason", "        - {name: r1, config: x}\n", "      accept_unrecognized:\n        - {text: bfd}\n", "accept_unrecognized[0] has no reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.ParamEntriesRule(validate.WorldView{DAG: buildRunbook(t, checkTask(tc.devices, tc.extra))})
			if len(findings) != 1 || findings[0].RuleName != "param-entries" || !strings.Contains(findings[0].Message, tc.want) {
				t.Fatalf("findings = %+v, want one saying %q", findings, tc.want)
			}
		})
	}
	templated := checkTask("        - {name: r1, config: \"{{ result.r1.running_config }}\"}\n", "")
	if f := validate.ParamEntriesRule(validate.WorldView{DAG: buildRunbook(t, templated)}); len(f) != 0 {
		t.Errorf("a templated config was judged before it rendered: %+v", f)
	}
}

// TestProjectFileRule_RefusesAProjectFileOnlyWhereThereIsNoProject: a
// devices entry's src and net.frr.config's src name project files; where the
// run has no project directory (a Walk-tier Runner) each is refused by
// place, and on the command line, which has the project, neither is. A
// configuration given inline is refused nowhere.
func TestProjectFileRule_RefusesAProjectFileOnlyWhereThereIsNoProject(t *testing.T) {
	dag := buildRunbook(t, "id: change\nhosts: r1\ntasks:\n"+
		"  - name: hold the candidate\n    net.batfish.check:\n      target: batfish\n"+
		"      devices:\n        - {name: r1, config: \"hostname r1\\n\"}\n        - {name: r2, src: configs/r2.frr.conf}\n"+
		"      assertions:\n        - ospf_established: true\n"+
		"  - name: apply it\n    net.frr.config:\n      src: configs/r1.frr.conf\n")
	walk := validate.ProjectFileRule(validate.WorldView{DAG: dag, NoProjectDirectory: true})
	if len(walk) != 2 {
		t.Fatalf("findings = %+v, want the devices entry's src and net.frr.config's src", walk)
	}
	joined := walk[0].Message + "\n" + walk[1].Message
	for _, want := range []string{"gives devices[1].src, a file in the project", "gives src, a file in the project", "has no project directory"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings %q do not say %q", joined, want)
		}
	}
	if strings.Contains(joined, "devices[0]") {
		t.Errorf("the inline configuration was refused: %q", joined)
	}
	if cli := validate.ProjectFileRule(validate.WorldView{DAG: dag}); len(cli) != 0 {
		t.Errorf("the command line, which has the project, was refused: %+v", cli)
	}
}

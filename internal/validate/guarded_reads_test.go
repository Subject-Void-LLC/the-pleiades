// Tests that a read the renderer marks Guarded (render.ExpressionInfo:
// under | default(...) on the name itself, tested with is defined, or
// through a name bound from such a read) counts as handling a missing
// register or variable, that the same read unguarded is still refused, and
// that a guard never lifts the rules about which roots a template may read.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// guardedParam is a parameter value for the task after a rescued block,
// as YAML: the template, quoted.
func guardedParam(template string) string {
	return "    noop:\n      saw: \"" + template + "\"\n"
}

// TestHandlersRule_GuardedReads: a register only a rescued block's own task
// writes may not exist after the block, and a read written for its absence
// is not refused, while the same read written plainly is.
func TestHandlersRule_GuardedReads(t *testing.T) {
	const refused = `reads the register "probe"`
	for _, tc := range []struct {
		name, template, want string
	}{
		// Guarded: each handles the register's absence itself.
		{"is defined in an if", "{% if result.probe is defined %}probed{% else %}not probed{% endif %}", ""},
		{"is defined as the value", "{{ result.probe is defined }}", ""},
		{"nodes tested with is defined", "{% if nodes.probe is defined %}probed{% endif %}", ""},
		{"default on the register", "{{ result.probe | default('none') }}", ""},
		{"default on nodes", "{{ nodes.probe | default({}) | to_json }}", ""},
		{"through a binding from a default", "{% set p = result.probe | default({}) %}{{ p | to_json }}", ""},
		{"default under or", "{{ result.probe | default('') or 'none' }}", ""},
		{"default under and", "{{ result.probe | default(false) and 'yes' }}", ""},
		{"is defined under or", "{{ result.probe is defined or 'none' }}", ""},
		// A read only a defined test lets through: an if's branch, the
		// later operand of and, or of or past an is not defined.
		{"a read inside an if that guards it", "{% if result.probe is defined %}{{ result.probe.ok }}{% endif %}", ""},
		{"a read after is defined under and", "{{ result.probe is defined and result.probe.ok }}", ""},
		{"a read after is not defined under or", "{{ result.probe is not defined or result.probe.ok }}", ""},
		{"an else past is not defined", "{% if result.probe is not defined %}none{% else %}{{ result.probe.ok }}{% endif %}", ""},
		{"an elif past is not defined", "{% if result.probe is not defined %}none{% elif result.probe.ok %}ok{% endif %}", ""},
		{"a guard over a deeper path", "{% if nodes.probe.web1 is defined %}{{ nodes.probe.web1.ok }}{% endif %}", ""},

		// Unguarded: the same reads written plainly.
		{"a plain read", "{{ result.probe }}", refused},
		{"a plain read of nodes", "{{ nodes.probe | to_json }}", refused},
		{"a plain read in an if", "{% if result.probe.ok %}x{% endif %}", refused},
		{"through a binding from a plain read", "{% set p = result.probe %}{{ p | to_json }}", refused},
		// Short-circuit: or reads its right operand when the left is false,
		// which a default of '' is, so that operand is a read of its own.
		{"the right operand of or", "{{ result.probe | default('') or result.probe.ok }}", refused},
		{"the right operand of and", "{{ vars.go | default(true) and result.probe.ok }}", refused},
		// default guards its own operand, never its fallback.
		{"a default's fallback", "{{ vars.x | default(result.probe.ok) }}", refused},
		// A guard covers only where the evaluation must pass it.
		{"a read in the else of is defined", "{% if result.probe is defined %}x{% else %}{{ result.probe.ok }}{% endif %}", refused},
		{"a read before the guard", "{{ result.probe.ok }}{% if result.probe is defined %}x{% endif %}", refused},
		{"a read after is defined under or", "{{ result.probe is defined or result.probe.ok }}", refused},
		{"a guard over a deeper path only", "{% if nodes.probe.web1 is defined %}{{ nodes.probe.web2 }}{% endif %}", refused},
		{"a default after another filter", "{{ result.probe.ok | upper | default('none') }}", refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.HandlersRule(validate.WorldView{DAG: buildYAML(t, guardedRunbook(plainRescue, guardedParam(tc.template)))})
			switch {
			case tc.want == "" && len(findings) != 0:
				t.Errorf("%s: want no finding, got %v", tc.template, findings)
			case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, tc.want)):
				t.Errorf("%s: want one finding saying %q, got %v", tc.template, tc.want, findings)
			}
		})
	}
}

// TestVarsRule_GuardedReads: a run variable read under a guard is optional,
// and the same variable read plainly is refused when nothing supplies it.
func TestVarsRule_GuardedReads(t *testing.T) {
	const refused = "no value is supplied for vars.region"
	for _, tc := range []struct {
		name, template, want string
	}{
		{"default", "{{ vars.region | default('us-east-1') }}", ""},
		{"is defined in an if", "{% if vars.region is defined %}{{ vars.region }}{% else %}us-east-1{% endif %}", ""},
		{"is defined under and", "{{ vars.region is defined and vars.region }}", ""},
		{"default under or", "{{ vars.region | default('') or 'us-east-1' }}", ""},
		{"through a binding from a default", "{% set r = vars.region | default('us-east-1') %}{{ r }}", ""},
		{"a plain read", "{{ vars.region }}", refused},
		{"a plain read under or", "{{ vars.region or 'us-east-1' }}", refused},
		{"a default's fallback", "{{ vars.zone | default(vars.region) }}", refused},
		// The renderer fails on upper before default can rescue, so this is
		// a plain read.
		{"a default after another filter", "{{ vars.region | upper | default('d') }}", refused},
		{"is defined in an if, the read outside it", "{% if vars.region is defined %}x{% endif %}{{ vars.region }}", refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, "id: v\ntasks:\n  - name: apply\n    noop:\n      region: \""+tc.template+"\"\n")
			findings := validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true})
			var got []string
			for _, f := range findings {
				got = append(got, f.Message)
			}
			switch {
			case tc.want == "" && len(findings) != 0:
				t.Errorf("%s: want no finding, got %v", tc.template, got)
			case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, tc.want)):
				t.Errorf("%s: want one finding saying %q, got %v", tc.template, tc.want, got)
			}
		})
	}
}

// TestGuardedReads_KeepTheRootRules: a guard handles a name's absence, never
// which roots a template may read. error outside a rescue, an unknown root
// and a named secret the engine would render are each refused under a
// default or an is defined exactly as they are without one.
func TestGuardedReads_KeepTheRootRules(t *testing.T) {
	for _, tc := range []struct {
		name, template, want string
		rule                 func(validate.WorldView) []validate.Finding
	}{
		{"error under default, outside a rescue", "{{ error.message | default('') }}", "reads error", validate.HandlersRule},
		{"error tested with is defined, outside a rescue", "{% if error is defined %}failed{% endif %}", "reads error", validate.HandlersRule},
		{"an unknown root under default", "{{ secrets.token | default('') }}", "may read only", validate.TemplateRule},
		{"an unknown root tested with is defined", "{% if secrets is defined %}x{% endif %}", "may read only", validate.TemplateRule},
		{"an unknown root under or", "{{ 'none' or secrets.token | default('') }}", "may read only", validate.TemplateRule},
		{"a named secret under default", "{{ secret.dmvpn.psk | default('') }}", "secret", validate.TemplateRule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := tc.rule(validate.WorldView{DAG: buildYAML(t, guardedRunbook(plainRescue, guardedParam(tc.template)))})
			var got []string
			for _, f := range findings {
				got = append(got, f.Message)
			}
			if len(findings) == 0 || !strings.Contains(strings.Join(got, "\n"), tc.want) {
				t.Errorf("%s: want a finding saying %q, got %v", tc.template, tc.want, got)
			}
		})
	}
}

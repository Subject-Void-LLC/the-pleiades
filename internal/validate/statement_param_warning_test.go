// Tests for StatementParamWarnings: a parameter that is a template only
// through a statement or a comment is warned about, naming the parameter
// and raw, and one holding an expression or a raw block is not.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestStatementParamWarnings is the table.
func TestStatementParamWarnings(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  any
		warn bool
	}{
		{"a statement and no expression", "{% if vars.x %}reboot{% endif %}", true},
		{"a comment and no expression", "ls {# all #}", true},
		{"a statement inside a list", []any{"a", "{% for x in vars.l %}x{% endfor %}"}, true},
		{"an expression too", "{% if vars.x %}{{ vars.x | quote }}{% endif %}", false},
		{"already raw", "printf '{% raw %}{%s}{% endraw %}'", false},
		{"no template at all", "echo hi", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.StatementParamWarnings(validate.WorldView{DAG: dagWithParams("exec.shell", map[string]any{"cmd": tc.cmd})})
			if !tc.warn {
				if len(findings) != 0 {
					t.Fatalf("want no warning, got %v", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("want one warning, got %v", findings)
			}
			for _, part := range []string{"parameter cmd", "renders as a template", "{% raw %}"} {
				if !strings.Contains(findings[0].Message, part) {
					t.Errorf("warning %q does not say %q", findings[0].Message, part)
				}
			}
		})
	}

	// It is a warning, so a runbook holding one still validates.
	rep := validate.Validate(validate.WorldView{DAG: dagWithParams("exec.shell", map[string]any{"cmd": "{% if vars.x %}reboot{% endif %}"})})
	found := false
	for _, w := range rep.Warnings {
		found = found || w.RuleName == "statement-param"
	}
	if !found {
		t.Errorf("Validate's warnings %v do not carry the statement-param warning", rep.Warnings)
	}
}

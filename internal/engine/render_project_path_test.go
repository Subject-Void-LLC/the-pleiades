// Tests that a project path the run writes renders only from vars, held by
// the rule the executor applies again when the task renders.
package engine

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestTemplateProblems_AProjectPathReadsOnlyVars: vars is the person's and
// passes; result, nodes and error hold what a device returned and are each
// refused, naming the root; the same expressions in a parameter of no
// format pass, so the rule is the format's alone.
func TestTemplateProblems_AProjectPathReadsOnlyVars(t *testing.T) {
	eng := render.New()
	for source, refused := range map[string]string{
		"configs/{{ vars.site }}.conf":         "",
		"configs/{{ vars.site | lower }}.conf": "",
		"{{ result.r3.running_config }}":       "never result",
		"configs/{{ nodes.r3 | to_json }}":     "never nodes",
		"{{ error.message }}":                  "never error",
		"{{ vars.dir }}/{{ result.r3.name }}":  "never result",
	} {
		tmpl, err := eng.Compile(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		problems := TemplateProblems(tmpl, collection.ParamFormatProjectPath)
		switch {
		case refused == "" && len(problems) > 0:
			t.Errorf("%s was refused: %v", source, problems)
		case refused != "" && (len(problems) != 1 || !strings.Contains(problems[0], refused) || !strings.Contains(problems[0], "may read only vars")):
			t.Errorf("%s: problems %v, want one saying %q", source, problems, refused)
		}
		if plain := TemplateProblems(tmpl, ""); len(plain) > 0 {
			t.Errorf("%s in a parameter of no format: %v", source, plain)
		}
	}
}

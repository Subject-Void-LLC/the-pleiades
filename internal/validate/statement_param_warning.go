// Package validate: the warning for a parameter that is a template only
// through a statement or a comment.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// StatementParamWarnings warns on each task parameter holding "{%" or "{#"
// and no "{{". Such a parameter renders, as one holding an expression does
// (engine.IsTemplate), where before statements existed it reached the
// method as written. It is a warning and not a refusal, since an if or a
// for in a parameter is a fair thing to write; the warning is for the
// author who meant the text for the device, a shell's printf '{%s}' or a
// Jinja template copied onto a host, and says how to keep it literal.
func StatementParamWarnings(world WorldView) []Finding {
	var out []Finding
	for _, pt := range paramTasks(world.DAG) {
		keys := make([]string, 0, len(pt.task.Params))
		for k := range pt.task.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, source := range engine.TemplateStrings(pt.task.Params[key]) {
				// An expression says the author meant a template; a raw block
				// says they already chose what stays literal.
				if strings.Contains(source, "{{") || strings.Contains(source, "endraw") {
					continue
				}
				out = append(out, Finding{
					RuleName: "statement-param",
					Node:     pt.id,
					Message: fmt.Sprintf("task %s, parameter %s: it holds {%% or {# and no {{, and it renders as a template, so its statements run and its comments vanish; to send the text as written, wrap it in {%% raw %%}...{%% endraw %%}",
						taskName(pt.id, pt.task), key),
				})
				break
			}
		}
	}
	return out
}

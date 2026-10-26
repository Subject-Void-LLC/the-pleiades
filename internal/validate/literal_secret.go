// LiteralSecretWarnings: the advice that a secret written out in a parameter
// that takes secret references belongs in the project's named secrets, so an
// agent or a person who writes a pre-shared key into a configuration line
// learns from validate itself how to keep it out of the runbook.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// LiteralSecretWarnings returns a warning, which never fails validation, for
// each task parameter that takes secret references (collection.Param.
// SecretRefs) and holds a secret written out: text pkg/netcfg would mask
// (the word after pre-shared-key, password, secret, key and the rest), once
// any secret reference is set aside. Each names the command that stores the
// value and the reference that names it. The value itself is never quoted.
func LiteralSecretWarnings(dag *engine.DAG) []Finding {
	if dag == nil {
		return nil
	}
	var out []Finding
	for _, pt := range paramTasks(dag) {
		takes := engine.ParamSecretRefs(pt.task.FQCN)
		keys := make([]string, 0, len(takes))
		for k := range takes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, text := range paramStrings(pt.task.Params[key]) {
				written := secretrefPattern(text)
				if _, found := netcfg.MaskFound(written); len(found) > 0 {
					out = append(out, Finding{
						RuleName: "literal_secret",
						Node:     pt.id,
						Message: fmt.Sprintf("task %s, parameter %s: a line holds a secret written out (after %q); store it with `pleiades add-secret <name> --field <field>` (--generate 32 for a new key, --value-stdin or --value-file for one you have) and write {{ secret.<name>.<field> }} in its place, so the runbook never holds it",
							taskName(pt.id, pt.task), key, secretWordBefore(written)),
					})
				}
			}
		}
	}
	return out
}

// secretrefPattern is text with every secret reference taken out, so a line
// that names its secret is not taken for one that writes it.
func secretrefPattern(text string) string {
	for _, r := range secretref.Find(text) {
		text = strings.ReplaceAll(text, r.String(), "")
		text = strings.ReplaceAll(text, "{{"+secretref.Root+"."+r.Name+"."+r.Field+"}}", "")
	}
	return text
}

// secretWordBefore is the word a masked line's secret follows, for a warning
// that may name the word and never the secret: the masked line's word before
// its first placeholder.
func secretWordBefore(text string) string {
	masked := strings.Fields(netcfg.Mask(text))
	for i, w := range masked {
		if strings.HasPrefix(w, netcfg.MaskPrefix) && i > 0 {
			return masked[i-1]
		}
	}
	return "a secret word"
}

// paramStrings is every string in v, at any depth.
func paramStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, paramStrings(e)...)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, paramStrings(t[k])...)
		}
		return out
	}
	return nil
}

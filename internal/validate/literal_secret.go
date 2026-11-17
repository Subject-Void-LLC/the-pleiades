// LiteralSecretWarnings: the advice that a secret written out in a parameter
// that takes secret references belongs in the project's named secrets, so an
// agent or a person who writes a pre-shared key into a configuration line
// learns from validate itself how to keep it out of the runbook.
package validate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// LiteralSecretWarnings returns a warning, which never fails validation, for
// each task parameter that takes secret references (collection.Param.
// SecretRefs) and holds a secret written out, once any secret reference is
// set aside: in a line, text pkg/netcfg would mask (the word after
// pre-shared-key, password, secret, key and the rest); in a structured value
// (a mapping, or a list holding one, as net.gnmi.config's val), a value the
// run's masking of a JSON document would mask, such as the value of a
// pre-shared-key field. Each names the command that stores the value and the
// reference that names it. The value itself is never quoted.
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
			value := pt.task.Params[key]
			if structured(value) {
				// A mapping's secret is in a field, never after a word, so it
				// is read as the JSON document the run masks it as.
				if written := withoutRefs(value); masksSomething(written) {
					out = append(out, literalSecret(pt.id, pt.task, key, fmt.Sprintf("a value holds a secret written out (in field %q)", secretField(written))))
				}
				continue
			}
			for _, text := range paramStrings(value) {
				// A reference stands in its place as a placeholder, not as
				// nothing: taken out, the word after it moved up behind the
				// secret word and read as a secret written out ("password {{
				// secret.a.b }} role network-admin" warned of "role").
				written := refsAsPlaceholders(text)
				if _, found := netcfg.MaskFound(written); len(found) > 0 {
					out = append(out, literalSecret(pt.id, pt.task, key, fmt.Sprintf("a line holds a secret written out (after %q)", secretWordBefore(written))))
				}
			}
		}
	}
	return out
}

// literalSecret is the warning for parameter key of task id, what saying
// where the secret is written.
func literalSecret(id string, task *engine.Task, key, what string) Finding {
	return Finding{
		RuleName: "literal_secret",
		Node:     id,
		Message: fmt.Sprintf("task %s, parameter %s: %s; store it with `pleiades add-secret <name> --field <field>` (--generate 32 for a new key, --value-stdin or --value-file for one you have) and write {{ secret.<name>.<field> }} in its place, so the runbook never holds it",
			taskName(id, task), key, what),
	}
}

// structured reports whether v holds a mapping at any depth.
func structured(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		return true
	case []any:
		for _, e := range t {
			if structured(e) {
				return true
			}
		}
	}
	return false
}

// withoutRefs is v with every secret reference taken out of its strings
// (secretrefPattern), so a field that names its secret is not taken for one
// that writes it.
func withoutRefs(v any) any {
	switch t := v.(type) {
	case string:
		return secretrefPattern(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = withoutRefs(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = withoutRefs(e)
		}
		return out
	}
	return v
}

// masksSomething reports whether the run's masking of v as one JSON document
// (pkg/netcfg.MaskOutput) finds a secret in it. A value JSON cannot hold, or
// a document masking cannot vouch for, finds none here: the warning is
// advice, and the method refuses such a value itself.
func masksSomething(v any) bool {
	doc, err := json.Marshal(v)
	if err != nil {
		return false
	}
	_, found, err := netcfg.MaskOutput(string(doc))
	return err == nil && len(found) > 0
}

// secretField names the field of v, a structured value masksSomething finds
// a secret in, that holds it: the innermost field whose own entry masks
// something, the first by name where several do.
func secretField(v any) string {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if f := secretField(e); f != "" {
				return f
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if f := secretField(t[k]); f != "" {
				return f
			}
			if masksSomething(map[string]any{k: t[k]}) {
				return k
			}
		}
	}
	return ""
}

// secretrefPattern is text with every secret reference taken out, so a field
// that names its secret is not taken for one that writes it.
func secretrefPattern(text string) string {
	return replaceRefs(text, "")
}

// referencePlaceholder is what a secret reference reads as in a line: one
// word pkg/netcfg takes for a secret masked already, so the secret word
// before it has its secret and every other word keeps its place.
const referencePlaceholder = netcfg.MaskPrefix + "secret-reference"

// refsAsPlaceholders is text with every secret reference replaced by
// referencePlaceholder, so a line that names its secret is not taken for one
// that writes it, whatever words follow the reference.
func refsAsPlaceholders(text string) string {
	return replaceRefs(text, referencePlaceholder)
}

// replaceRefs is text with every secret reference, whatever its spacing,
// replaced by with. Only the two spacings a runbook usually writes were
// replaced before, so "{{secret.a.b }}" was left in the line and its braces
// read as a secret written out.
func replaceRefs(text, with string) string {
	return secretref.ReplaceAll(text, with)
}

// secretWordBefore is the word a masked line's secret follows, for a warning
// that may name the word and never the secret: the masked line's word before
// its first placeholder that is not a reference's (referencePlaceholder).
func secretWordBefore(text string) string {
	masked := strings.Fields(netcfg.Mask(text))
	for i, w := range masked {
		if strings.HasPrefix(w, netcfg.MaskPrefix) && w != referencePlaceholder && i > 0 {
			return masked[i-1]
		}
	}
	return "a secret word"
}

// paramStrings is every string in v, a string or a list of them at any
// depth: a value holding a mapping is never read here, but whole, as the
// JSON document masksSomething reads.
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
	}
	return nil
}

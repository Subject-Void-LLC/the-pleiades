// Package playbook: resolving {{ }} templates from the playbook's own
// variables, through the platform's one template renderer.
package playbook

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// wholeVar matches a string that is one variable and nothing else, whose
// value keeps its own type (a list stays a list) rather than becoming
// text, as Ansible does.
var wholeVar = regexp.MustCompile(`^\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)

// hasTemplate reports whether s holds Jinja.
func hasTemplate(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "{%") || strings.Contains(s, "{#")
}

// resolver resolves templates for one conversion, recording each
// variable it resolved and where.
type resolver struct {
	ix     *varIndex
	engine render.Engine
	// uses records, per variable, its definition and every use.
	uses map[string]*Resolution
	// order keeps the variables in first-use order.
	order []string
}

// newResolver returns a resolver over ix.
func newResolver(ix *varIndex) *resolver {
	return &resolver{ix: ix, engine: render.New(), uses: map[string]*Resolution{}}
}

// value resolves name at use, with bindings (a loop's item) first.
func (r *resolver) value(name string, bindings map[string]any, use Position, depth int) (any, *resolveError) {
	if v, ok := bindings[name]; ok {
		return v, nil
	}
	if depth > maxVarDepth {
		return nil, &resolveError{"template.unresolved", fmt.Sprintf("%s chains through more than %d variables, or back to itself", name, maxVarDepth)}
	}
	def, rerr := r.ix.literal(name)
	if rerr != nil {
		return nil, rerr
	}
	v, err := naturalValue(def.value)
	if err != nil {
		return nil, &resolveError{"vault.value", fmt.Sprintf("%s: %v", name, err)}
	}
	v, rerr = r.expand(v, bindings, use, depth+1)
	if rerr != nil {
		return nil, rerr
	}
	if secretValue(v) {
		return nil, &resolveError{"template.secret", fmt.Sprintf("%s holds a value shaped like a secret", name)}
	}
	r.record(name, def.at, use)
	return v, nil
}

// expand resolves every template inside v, a value read from YAML.
func (r *resolver) expand(v any, bindings map[string]any, use Position, depth int) (any, *resolveError) {
	switch t := v.(type) {
	case string:
		if !hasTemplate(t) {
			return t, nil
		}
		return r.render(t, bindings, use, depth)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			x, err := r.expand(item, bindings, use, depth)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			x, err := r.expand(item, bindings, use, depth)
			if err != nil {
				return nil, err
			}
			out[k] = x
		}
		return out, nil
	}
	return v, nil
}

// render resolves s, a string holding a template. A string that is one
// variable keeps that variable's type; anything else renders to text, and
// Jinja the renderer does not support leaves the task for a person.
func (r *resolver) render(s string, bindings map[string]any, use Position, depth int) (any, *resolveError) {
	if m := wholeVar.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		return r.value(m[1], bindings, use, depth)
	}
	tpl, err := r.engine.Compile(s)
	if err != nil {
		return nil, &resolveError{"template.unsupported", "the template uses Jinja this converter cannot evaluate"}
	}
	vars := map[string]any{}
	for _, name := range tpl.Names() {
		v, rerr := r.value(name, bindings, use, depth)
		if rerr != nil {
			return nil, rerr
		}
		// Only text is printed into other text: a bool, a number, a list
		// or a map prints differently in Jinja (True, {'k': 'v'}) and in
		// the renderer. A map or list may still be read through a path
		// (item.key), with every leaf that is not text removed, so reading
		// one of those fails rather than prints it.
		switch v.(type) {
		case string:
		case map[string]any, []any:
			if printsWhole(s, name) {
				return nil, &resolveError{"template.unsupported", fmt.Sprintf("%s is a map or a list printed whole into text, which Jinja and this renderer print differently", name)}
			}
			v = textLeaves(v)
		default:
			return nil, &resolveError{"template.unsupported", fmt.Sprintf("%s is not text, and Jinja and this renderer print it differently inside other text", name)}
		}
		vars[name] = v
	}
	out, err := tpl.Render(vars)
	if err != nil {
		return nil, &resolveError{"template.unsupported", "the template could not be rendered"}
	}
	return out, nil
}

// record notes one use of name, defined at def.
func (r *resolver) record(name string, def, use Position) {
	res, ok := r.uses[name]
	if !ok {
		res = &Resolution{Variable: name, DefinedAt: def}
		r.uses[name] = res
		r.order = append(r.order, name)
	}
	for _, p := range res.UsedAt {
		if p == use {
			return
		}
	}
	res.UsedAt = append(res.UsedAt, use)
}

// resolutions returns every resolution, in first-use order.
func (r *resolver) resolutions() []Resolution {
	out := make([]Resolution, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, *r.uses[name])
	}
	return out
}

// secretValue reports whether any text in v is shaped like a secret.
func secretValue(v any) bool {
	switch t := v.(type) {
	case string:
		return redact.SecretShaped(t)
	case []any:
		for _, item := range t {
			if secretValue(item) {
				return true
			}
		}
	case map[string]any:
		for k, item := range t {
			if redact.SecretName(k) || secretValue(item) {
				return true
			}
		}
	}
	return false
}

// printsWhole reports whether src prints name itself, rather than a
// path into it: {{ name }} or {{ name | filter }}.
func printsWhole(src, name string) bool {
	return regexp.MustCompile(`\{\{-?\s*` + regexp.QuoteMeta(name) + `\s*(\||-?\}\})`).MatchString(src)
}

// textLeaves returns v with every leaf that is not text removed.
func textLeaves(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range t {
			if x := textLeaves(item); x != nil {
				out[k] = x
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			out = append(out, textLeaves(item))
		}
		return out
	case string:
		return t
	}
	return nil
}

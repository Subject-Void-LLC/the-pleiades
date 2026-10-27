// Rendered task parameters (Phase 117a): a task's params may read the run's
// variables and earlier tasks' registered results through the one template
// renderer (internal/render, PLAN.md Section 25), rendered when the task's
// node dispatches, never when the DAG compiles.
//
// Rendering at dispatch is Phase 87's own gate decision, which this file
// adopts: a result only exists once the task that registers it has run.
// Three roots are readable, and nothing else:
//
//   - vars: the run's variables (a launch's extra variables and survey
//     answers, `pleiades run --extra-vars`), without any secret a bound
//     credential injected (internal/adapters/native's injectedVariables);
//   - nodes: every registered result, register first and then device id,
//     the same snapshot a condition reads;
//   - result: each register that exactly one device (or one device-less
//     task) wrote, by name alone, so an author need not type a device id;
//   - error: inside a rescue: only, the failure its block is handling on the
//     device the task runs on (handlers.go's errorContext);
//   - device: the device the task runs on (its name, id, tags, properties,
//     capabilities and state, deviceRoot), which makes the task render and
//     decide its condition once per device (device_root.go).
//
// A value with no "{{" is passed through untouched, so a runbook that
// renders nothing costs nothing and behaves as it always did.
package engine

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/secretref"
)

// The roots a rendered parameter may read.
const (
	TemplateRootVars   = "vars"
	TemplateRootNodes  = "nodes"
	TemplateRootResult = "result"
	TemplateRootError  = "error"
	TemplateRootDevice = "device"
)

// TemplateRoots are the roots a rendered parameter may read, for plan-time
// validation.
func TemplateRoots() []string {
	return []string{TemplateRootDevice, TemplateRootError, TemplateRootNodes, TemplateRootResult, TemplateRootVars}
}

// WithRenderer hands the Executor the template renderer its tasks' params
// render through. A composition root passes the one engine it holds; an
// Executor with none refuses a task whose params hold a template rather
// than handing the method the literal text.
func WithRenderer(eng render.Engine) ExecutorOption {
	return func(x *Executor) { x.renderer = eng }
}

// HasTemplate reports whether v, at any depth of a decoded YAML or JSON
// value, holds a string with a template action in it.
func HasTemplate(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, "{{")
	case map[string]any:
		for _, e := range t {
			if HasTemplate(e) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if HasTemplate(e) {
				return true
			}
		}
	}
	return false
}

// renderTask returns task with its params rendered, or task itself when no
// param holds a template. The copy shares everything but Params, so the
// DAG's own task is never changed and a later node reads the authored text.
// roots are the roots this one evaluation adds (a rescue task's error, the
// device a per-device task runs on).
func (r *run) renderTask(task *Task, roots map[string]any) (*Task, error) {
	hasFile := templateFileParam(task.FQCN) != ""
	if !hasFile && !HasTemplate(task.Params) {
		return task, nil
	}
	if r.x.renderer == nil && !hasFile {
		return nil, errors.New("this run has no template renderer, so a parameter holding {{ cannot be rendered")
	}
	ctx, err := r.templateContext()
	if err != nil {
		return nil, err
	}
	vars, err := r.mergedVars(task, roots)
	if err != nil {
		return nil, err
	}
	ctx[TemplateRootVars] = vars
	for k, v := range roots {
		if k != hostVarsRoot {
			ctx[k] = v
		}
	}
	// A template file (file.template's src) is rendered to text here and set
	// as the content parameter only after the other parameters render, so the
	// rendered text is never rendered a second time.
	fileText, fromFile := "", false
	if hasFile {
		if task, fileText, fromFile, err = r.renderTemplateFile(task, ctx); err != nil {
			return nil, err
		}
	}
	if !HasTemplate(task.Params) {
		if !fromFile {
			return task, nil
		}
		out := *task
		out.Params = withContent(task.Params, fileText)
		return &out, nil
	}
	if r.x.renderer == nil {
		return nil, errors.New("this run has no template renderer, so a parameter holding {{ cannot be rendered")
	}
	// The rules validation holds a template to are held again here, so a
	// DAG that reached the executor without validation still cannot render
	// data onto a command line or into a request's host.
	if err := checkTaskTemplates(r.x.renderer, task); err != nil {
		return nil, err
	}
	rendered, err := renderParams(r.x.renderer, task.FQCN, task.Params, ctx)
	if err != nil {
		return nil, err
	}
	out := *task
	out.Params = rendered
	if fromFile {
		out.Params = withContent(rendered, fileText)
	}
	return &out, nil
}

// renderParams renders each of params, as its method declares it. A
// parameter declared a string renders to text, and so does each item of a
// list of strings, whatever the expression's value: a template rendering 7
// into stdin is the text 7, where keeping the number handed the method a
// value its string reader takes for absent. Any other rendered parameter
// keeps its value's type and must be of the declared type
// (collection.ParamFits), or the task fails naming it. A parameter with no
// template, or one the method does not declare (validation's finding), is
// left as it is.
func renderParams(eng render.Engine, fqcn string, params map[string]any, ctx map[string]any) (map[string]any, error) {
	types := ParamTypes(fqcn)
	secretRefs := ParamSecretRefs(fqcn)
	out := make(map[string]any, len(params))
	for key, value := range params {
		if !HasTemplate(value) {
			out[key] = value
			continue
		}
		typ := types[key]
		text := typ == collection.ParamTypeString || typ == collection.ParamTypeListOfString
		var rendered any
		var err error
		switch {
		case secretRefs[key]:
			rendered, err = renderKeepingRefs(eng, value, ctx, text)
		case text:
			rendered, err = renderText(eng, value, ctx)
		default:
			rendered, err = renderValue(eng, value, ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("parameter %s: %w", key, err)
		}
		if typ != "" {
			fits, err := collection.ParamFits(typ, rendered)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", key, err)
			}
			if !fits {
				return nil, fmt.Errorf("parameter %s rendered to %s, and %s declares it %s", key, collection.ValueKind(rendered), fqcn, typ)
			}
		}
		out[key] = rendered
	}
	return out, nil
}

// renderText renders a string holding a template to text, and each such
// string in a list, leaving every other value for ParamFits to judge. A
// string that is one expression whose value is a list or a map is refused:
// its text is a rendering nobody chose, where a number or a boolean has one
// obvious text.
func renderText(eng render.Engine, v any, ctx map[string]any) (any, error) {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, "{{") {
			return t, nil
		}
		tmpl, err := eng.Compile(t)
		if err != nil {
			return nil, err
		}
		if value, single, err := tmpl.Value(ctx); single {
			if err != nil {
				return nil, err
			}
			if value != nil {
				switch reflect.TypeOf(value).Kind() {
				case reflect.Slice, reflect.Array, reflect.Map:
					return nil, fmt.Errorf("renders to %s, and this parameter takes text", collection.ValueKind(value))
				}
			}
		}
		return tmpl.Render(ctx)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			rendered, err := renderText(eng, e, ctx)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil
	}
	return v, nil
}

// renderKeepingRefs renders a parameter that may hold secret references
// (collection.Param.SecretRefs): a string holding one is passed on exactly as
// written, for the method to resolve, and any other templated string renders
// as usual, text or typed as text says. A rendered string that comes out
// holding a reference is refused, so a run's variables or a device's output
// can never write one: only the runbook's author does.
func renderKeepingRefs(eng render.Engine, v any, ctx map[string]any, text bool) (any, error) {
	switch t := v.(type) {
	case string:
		if secretref.Has(t) {
			return t, nil
		}
		var rendered any
		var err error
		if text {
			rendered, err = renderText(eng, t, ctx)
		} else {
			rendered, err = renderValue(eng, t, ctx)
		}
		if err != nil {
			return nil, err
		}
		// The whole value is held, not only text: a one-expression template
		// keeps its value's type, so a variable or a register holding a list
		// of maps could otherwise carry a reference in (the review's F2).
		if holdsRef(rendered) {
			return nil, errors.New("renders to a value holding a secret reference, and only the runbook's author may write one")
		}
		return rendered, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			rendered, err := renderKeepingRefs(eng, e, ctx, false)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", k, err)
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			rendered, err := renderKeepingRefs(eng, e, ctx, text)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil
	}
	return v, nil
}

// holdsRef reports whether v, at any depth, holds a string that reads the
// secret root, or a map key that does.
func holdsRef(v any) bool {
	switch t := v.(type) {
	case string:
		return secretref.Has(t)
	case []any:
		for _, e := range t {
			if holdsRef(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range t {
			if secretref.Has(k) || holdsRef(e) {
				return true
			}
		}
	}
	return false
}

// templateContext is what a rendered parameter reads: vars, nodes and
// result, from the run's variables and its workflow context as they stand
// when the node dispatches.
func (r *run) templateContext() (map[string]any, error) {
	tree, err := r.x.workflow.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read the registered results: %w", err)
	}
	vars := r.x.extraVars
	if vars == nil {
		vars = map[string]any{}
	}
	return map[string]any{
		TemplateRootVars:   vars,
		TemplateRootNodes:  tree,
		TemplateRootResult: singleWriters(tree),
	}, nil
}

// singleWriters returns, from a workflow context snapshot, each register
// that exactly one device (or one device-less task) wrote, keyed by the
// register alone. A register several devices wrote is left out, so reading
// it through result is undefined and says so, rather than picking one.
func singleWriters(tree map[string]any) map[string]any {
	out := make(map[string]any, len(tree))
	for register, raw := range tree {
		byDevice, ok := raw.(map[string]any)
		if !ok || len(byDevice) != 1 {
			continue
		}
		for _, stats := range byDevice {
			out[register] = stats
		}
	}
	return out
}

// renderValue renders every string in v that holds a template, walking maps
// and lists and leaving every other value as it is. A string that is one
// expression and nothing else keeps its value's type (render.Template.Value);
// any other string renders to text.
func renderValue(eng render.Engine, v any, ctx map[string]any) (any, error) {
	switch t := v.(type) {
	case string:
		if !strings.Contains(t, "{{") {
			return t, nil
		}
		tmpl, err := eng.Compile(t)
		if err != nil {
			return nil, err
		}
		if value, single, err := tmpl.Value(ctx); single {
			return value, err
		}
		return tmpl.Render(ctx)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			rendered, err := renderValue(eng, e, ctx)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", k, err)
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			rendered, err := renderValue(eng, e, ctx)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil
	}
	return v, nil
}

// commandSafeFilters are the filters an expression rendered into command
// text must end with: quote for a POSIX shell, cli_token for a network CLI.
var commandSafeFilters = []string{"quote", "cli_token"}

// TemplateProblems returns what is wrong with one compiled template for a
// parameter of the given format, or nothing: an expression reading a root
// other than vars, nodes or result; in command text, an expression that
// does not end with quote or cli_token; in a URL, text before the first
// expression that does not already fix the scheme and host; in a project
// path the run writes, an expression reading anything but vars. Validation and
// the executor share it, so the rule is held once at plan time and again
// when the task renders.
func TemplateProblems(tmpl render.Template, format collection.ParamFormat) []string {
	return TaskTemplateProblems(tmpl, format, nil)
}

// TaskTemplateProblems is TemplateProblems for a parameter of task, which may
// also read the roots its own loop gives it (item or the loop_var, loop and
// the index_var). The loop's values are held to the same rules as vars are:
// in command text they must be quoted, and they never name a project path.
func TaskTemplateProblems(tmpl render.Template, format collection.ParamFormat, task *Task) []string {
	roots := TemplateRoots()
	if task != nil {
		roots = append(roots, task.loopRoots()...)
	}
	var problems []string
	for _, expr := range tmpl.Expressions() {
		root := expr.Path[0]
		if !slices.Contains(roots, root) {
			problems = append(problems, fmt.Sprintf("an expression reads %q, and a parameter may read only %s", root, strings.Join(roots, ", ")))
			continue
		}
		if format == collection.ParamFormatCommand && (len(expr.Filters) == 0 || !slices.Contains(commandSafeFilters, expr.Filters[len(expr.Filters)-1])) {
			problems = append(problems, "this parameter is command text, so each expression must end with | quote (a shell) or | cli_token (a network CLI)")
		}
		// A project path the run writes may take only what the person
		// running the work supplied: nodes, result and error hold what a
		// device returned, which must not choose the file replaced.
		if format == collection.ParamFormatProjectPath && root != TemplateRootVars && !(task != nil && task.authoredLoopRoot(root)) {
			problems = append(problems, fmt.Sprintf("this parameter is a path in the project that the run writes, so an expression may read only vars (or the item of a loop written in the runbook or read from vars), never %s, which holds what a device returned", root))
		}
	}
	if format == collection.ParamFormatURL && !URLPrefixFixed(tmpl.Source()) {
		problems = append(problems, "this parameter is a URL, so the text before its first {{ must name the scheme and host and end the host with /, ? or #, or begin with / for a path on the device's API")
	}
	return problems
}

// URLPrefixFixed reports whether the text of source before its first
// expression fixes where the request goes: it begins with "/" (a path on
// the target device's own API), or it holds a scheme, "://", a host, and
// one of "/", "?" or "#" ending the host, so an expression can neither
// choose the host nor extend it ("api.example.com{{ x }}" could become
// "api.example.com.attacker.net").
func URLPrefixFixed(source string) bool {
	prefix, _, _ := strings.Cut(source, "{{")
	if strings.HasPrefix(prefix, "/") {
		return true
	}
	_, rest, ok := strings.Cut(prefix, "://")
	if !ok {
		return false
	}
	return strings.IndexAny(rest, "/?#") > 0
}

// ParamFormats returns each declared parameter's Format for fqcn, or an
// empty map for a name that is not a registered method.
func ParamFormats(fqcn string) map[string]collection.ParamFormat {
	out := map[string]collection.ParamFormat{}
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		return out
	}
	for _, p := range desc.Manifest.Doc.Params {
		if p.Format != "" {
			out[p.Name] = p.Format
		}
	}
	return out
}

// ParamSecretRefs returns the declared parameters of fqcn that may hold
// secret references (collection.Param.SecretRefs), or an empty map for a name
// that is not a registered method.
func ParamSecretRefs(fqcn string) map[string]bool {
	out := map[string]bool{}
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		return out
	}
	for _, p := range desc.Manifest.Doc.Params {
		if p.SecretRefs {
			out[p.Name] = true
		}
	}
	return out
}

// SecretRefProblems judges a template string that reads the secret root
// (secretref.Has), for a parameter that does or does not take secret
// references. It reports whether source is such a string, which the caller
// then neither compiles nor renders, and what is wrong with it: a reference
// in a parameter that takes none, or one that is not exactly
// {{ secret.<name>.<field> }} standing apart from any other expression.
// Validation and the executor share it, as they share TemplateProblems.
func SecretRefProblems(source string, takes bool) (bool, []string) {
	if !secretref.Has(source) {
		return false, nil
	}
	if !takes {
		return true, []string{"a secret reference may appear only in a parameter its method resolves itself (pleiades doc names them), and the engine renders this one"}
	}
	return true, secretref.Problems(source)
}

// ParamTypes returns each declared parameter's type for fqcn, its own and
// its fragments' (fragment.Types), or an empty map for a name that is not a
// registered method.
func ParamTypes(fqcn string) map[string]string {
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		return map[string]string{}
	}
	return fragment.Types(desc)
}

// TemplateStrings returns every string in v, at any depth, that holds a
// template action, in a stable order.
func TemplateStrings(v any) []string {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "{{") {
			return []string{t}
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, TemplateStrings(t[k])...)
		}
		return out
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, TemplateStrings(e)...)
		}
		return out
	}
	return nil
}

// checkTaskTemplates holds every templated parameter of task to
// TemplateProblems, returning the first problem as an error naming the
// parameter. A rendered target is held here too, and to within: besides
// (resolveBounded).
func checkTaskTemplates(eng render.Engine, task *Task) error {
	formats := ParamFormats(task.FQCN)
	secretRefs := ParamSecretRefs(task.FQCN)
	keys := make([]string, 0, len(task.Params))
	for k := range task.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, source := range TemplateStrings(task.Params[key]) {
			if isRef, problems := SecretRefProblems(source, secretRefs[key]); isRef {
				if len(problems) > 0 {
					return fmt.Errorf("parameter %s: %s", key, problems[0])
				}
				continue
			}
			tmpl, err := eng.Compile(source)
			if err != nil {
				return fmt.Errorf("parameter %s: %w", key, err)
			}
			problems := TaskTemplateProblems(tmpl, formats[key], task)
			if key == collection.TargetParam {
				problems = append(problems, TargetTemplateProblems(tmpl)...)
			}
			if len(problems) > 0 {
				return fmt.Errorf("parameter %s: %s", key, problems[0])
			}
		}
	}
	return nil
}

// resolveBounded resolves a target rendered from data (task.Within set):
// the rendered params.target names one or more devices, by name, as text
// (one name, or several separated by commas) or as a list of names, and
// each must be one of the devices within: resolves to. A tag, a pattern or
// a name outside the bound fails the task naming both, and nothing falls
// back to hosts: (FAILURE_PATTERNS 11). The bound is the author's, written
// literally; the names are the data's, and data never gets to name a tag.
func (r *run) resolveBounded(task *Task) ([]inventory.InventoryItem, error) {
	names, err := renderedNames(task.Params[collection.TargetParam])
	if err != nil {
		return nil, err
	}
	bound := r.x.resolver.Resolve(task.Within)
	if len(bound) == 0 {
		return nil, fmt.Errorf("within: %q matches no inventory host or tag", task.Within)
	}
	byName := make(map[string]inventory.InventoryItem, len(bound))
	for _, d := range bound {
		byName[d.Name()] = d
	}
	var out []inventory.InventoryItem
	seen := map[string]bool{}
	for _, name := range names {
		d, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("the rendered target names %q, which is not a device within %q", name, task.Within)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, d)
		}
	}
	return out, nil
}

// renderedNames reads a rendered params.target as device names: text holds
// one name or several separated by commas, and a list holds one name per
// item. Anything else, or no name at all, is refused.
func renderedNames(raw any) ([]string, error) {
	var names []string
	switch t := raw.(type) {
	case string:
		for _, part := range strings.Split(t, ",") {
			if name := strings.TrimSpace(part); name != "" {
				names = append(names, name)
			}
		}
	case []any:
		for _, item := range t {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("the rendered target is a list holding %s, and each item must be a device name", collection.ValueKind(item))
			}
			names = append(names, strings.TrimSpace(s))
		}
	default:
		return nil, fmt.Errorf("the rendered target is %s, and it must be a device name, several separated by commas, or a list of names", collection.ValueKind(raw))
	}
	if len(names) == 0 {
		return nil, errors.New("the rendered target names no device")
	}
	return names, nil
}

package templates

// This file is the template edit form's per-kind execution fields: forks,
// limit, verbosity and the rest, each paired with a checkbox that adds it
// to Prompts. A template's real field set is per launch.Kind, so it cannot
// be part of the static Fields declaration in templates.go the way name or
// description are -- a kind arrives in a file this package has never seen,
// bringing fields nothing here declared. It is resolved per record through
// view.Descriptor.FieldsFor instead, the same seam RecordAction.FieldsFor
// already uses for the launch form (actions.go), rather than a second one
// invented for this.
//
// Create does not get these controls. A template's kind is not chosen
// until the RUNS picker's submission is parsed at create, so there is
// nothing yet to resolve a field set from; setting what a template runs
// with is the edit that follows.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// promptSuffix names a field's companion "ask on launch" checkbox, derived
// from the field's own name so the two can never drift apart.
const promptSuffix = "_prompt"

// defaultsFields resolves one template's own kind fields for its edit form:
// a control per declared field, prefilled with what the template is saved
// with (via the projector's Form func, defaultsValues below), and a
// checkbox beside it that is checked when a launch may override it.
func defaultsFields(store launch.Store) func(context.Context, string) ([]view.Field, error) {
	return func(ctx context.Context, id string) ([]view.Field, error) {
		tmpl, ok := load(ctx, store, id)
		if !ok {
			// A template that cannot be read offers no controls for values
			// nothing here could apply. GET returns not-found separately.
			return nil, nil
		}
		d, err := tmpl.Descriptor()
		if err != nil {
			// A kind that is no longer registered has nothing to render
			// fields from.
			return nil, nil
		}

		out := make([]view.Field, 0, len(d.Fields)*2)
		for _, spec := range d.Fields {
			out = append(out, defaultField(spec), promptField(spec))
		}
		return out, nil
	}
}

// defaultField renders one kind field as the template's own control: what
// this template runs with unless a launch that opens it overrides it.
//
// This is deliberately not launchFields' fieldFor (actions.go): that one
// prefills nothing and describes the template's saved value in its help
// text, because leaving a launch override blank means "use the template's
// value". Here the control IS the template's value, so it is prefilled
// directly and left blank means "no default", not "inherit from itself".
func defaultField(spec launch.FieldSpec) view.Field {
	f := view.Field{
		Name:         spec.Name,
		Label:        spec.Label,
		Help:         spec.Help,
		Autocomplete: "off",
		InForm:       true,
	}
	switch spec.Type {
	case launch.TypeInt:
		f.Kind = view.KindNumber
		if spec.Bounded() {
			f.Help += " Between " + strconv.Itoa(spec.Min) + " and " + strconv.Itoa(spec.Max) + "."
		}
	case launch.TypeStringList:
		f.Kind = view.KindTags
	case launch.TypeMap:
		f.Kind = view.KindLongText
		f.Help += " One key=value per line."
	default:
		f.Kind = view.KindText
	}
	return f
}

// promptField is one field's "ask on launch" checkbox: whether a launch may
// override defaultField's value. Ticking it is what B1 replaces the old
// union "prompt on launch" multi-select with, one checkbox per field
// instead of one control for every field of every kind at once.
func promptField(spec launch.FieldSpec) view.Field {
	return view.Field{
		Name:   spec.Name + promptSuffix,
		Label:  "PROMPT ON LAUNCH",
		Help:   "Let a launch override " + spec.Label + ".",
		Kind:   view.KindBool,
		InForm: true,
	}
}

// defaultsValues prefills one template's per-kind controls: its own saved
// field values, plus which of them a launch may currently override.
func defaultsValues(tmpl launch.Template) map[string]string {
	d, err := tmpl.Descriptor()
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(d.Fields)*2)
	for _, spec := range d.Fields {
		out[spec.Name] = defaultValue(spec, tmpl.Defaults)
		if tmpl.Promptable(spec.Name) {
			// The checkbox template only renders `checked` for the literal
			// string "true" (view/field.templ); absent means unchecked, so
			// an un-promptable field is simply not set here rather than set
			// to a falsy string.
			out[spec.Name+promptSuffix] = "true"
		}
	}
	return out
}

// defaultValue renders one field's stored value for its control, the
// inverse of bindDefaults' parse for the same field.
func defaultValue(spec launch.FieldSpec, defaults launch.Fields) string {
	if !defaults.Has(spec.Name) {
		return ""
	}
	switch spec.Type {
	case launch.TypeInt:
		return strconv.Itoa(defaults.Int(spec.Name))
	case launch.TypeStringList:
		return strings.Join(defaults.List(spec.Name), ",")
	case launch.TypeMap:
		return joinVariables(defaults.Map(spec.Name))
	default:
		return defaults.String(spec.Name)
	}
}

// joinVariables renders a variable map back into the key=value-per-line
// text parseVariables (actions.go) reads, so a saved template's own extra
// variables round-trip through its own edit form rather than only through
// the launch form's override control.
func joinVariables(vars map[string]any) string {
	if len(vars) == 0 {
		return ""
	}
	lines := make([]string, 0, len(vars))
	for k, v := range vars {
		lines = append(lines, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// bindDefaults reads the submitted per-kind controls into the sparse
// Defaults a template is saved with, and the list of fields a launch may
// override.
//
// It does not need to know which kind is being edited. defaultsFields
// already narrowed what this submission could legally carry to that
// template's own kind, and view.Values answers empty for any name this
// submission's descriptor did not declare (model.go), so walking every
// registered kind's fields and keeping only what answers is exactly the
// set the actual kind opened -- whatever the wrong kind's fields would have
// read is simply never there to read. The same dedup-by-name this package's
// old promptOptions relied on for the fields two kinds share.
func bindDefaults(v view.Values) (launch.Fields, []string, view.FieldErrors) {
	errs := view.FieldErrors{}
	defaults := launch.Fields{}
	var prompts []string

	seen := map[string]bool{}
	for _, d := range launch.Kinds() {
		for _, spec := range d.Fields {
			if seen[spec.Name] {
				continue
			}
			seen[spec.Name] = true

			if v.Bool(spec.Name + promptSuffix) {
				prompts = append(prompts, spec.Name)
			}

			raw := strings.TrimSpace(v.Get(spec.Name))
			if raw == "" {
				continue
			}

			var submitted any = raw
			switch spec.Type {
			case launch.TypeStringList:
				submitted = v.Tags(spec.Name)
			case launch.TypeMap:
				vars, err := parseVariables(raw)
				if err != nil {
					errs.Add(spec.Name, err.Error())
					continue
				}
				submitted = vars
			}

			value, err := d.Normalize(spec.Name, submitted)
			if err != nil {
				errs.Add(spec.Name, fieldMessage(err))
				continue
			}
			defaults[spec.Name] = value
		}
	}

	if len(defaults) == 0 {
		defaults = nil
	}
	return defaults, prompts, errs
}

// This file is the Injectors tab of a credential type: the read-only table
// of where the type's inputs reach a run, and the header action that adds
// one.
//
// An injector is the highest-consequence document a credential type has: it
// decides which environment variables a customer's playbook runs with, and
// setting the wrong one is code execution inside that run. The package doc
// comment explains why it is never a free-form field. It is authored here
// through a structured, validated control instead: one target, one name,
// one template, judged by the store, which compiles the template against
// the type's own inputs and refuses an environment variable name that would
// change how the run executes.
package credentialtypes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// injectorsTitle is the section heading and the tab slug an add redirects to.
const injectorsTitle = "Injectors"

// addInjectorName is the Add-injector header action's URL segment.
const addInjectorName = "add-injector"

// The injector target values a form submits, and the labels it shows.
const (
	targetEnv      = "env"
	targetExtraVar = "extra_var"
	targetFile     = "file"
)

// filePrefix is the key an injector file entry carries in the multi-file
// spelling: "template.<label>". It is credtype's own convention.
const filePrefix = "template."

// injectorColumns are the read-only columns the Injectors table renders.
var injectorColumns = []view.Field{
	{Name: "target", Label: "TARGET", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true},
	{Name: "template", Label: "TEMPLATE", Kind: view.KindText, InList: true},
}

// injectorsSection is the Injectors tab: where a type's inputs go at run
// time, with an Add control in the header.
func injectorsSection(store credstore.Store) view.Section {
	return view.Section{
		Title:   injectorsTitle,
		Summary: "Where this type's inputs reach a run. Every template renders over the type's own input ids, for example {{ api_token }}.",
		Status:  view.StatusImplemented,
		Fields:  injectorColumns,
		Empty:   "This credential type injects nothing yet. A machine or vault type reaches a run directly and needs none.",
		Rows:    injectorRows(store),
		Actions: []string{addInjectorName},
	}
}

// injectorRows projects a type's injector document onto section rows, one
// per environment variable, extra variable and generated file.
func injectorRows(store credstore.TypeReader) func(context.Context, string) ([]view.Row, error) {
	return func(ctx context.Context, parentID string) ([]view.Row, error) {
		if parentID == "" {
			return nil, nil
		}
		numeric, err := strconv.Atoi(parentID)
		if err != nil {
			return nil, nil
		}
		ct, err := store.GetType(ctx, numeric)
		if err != nil {
			return nil, err
		}

		inj := ct.Injectors
		var rows []view.Row

		for _, name := range sortedKeys(inj.Env) {
			rows = append(rows, injectorRow("env-"+name, "environment variable", name, inj.Env[name]))
		}
		for _, key := range sortedAnyKeys(inj.ExtraVars) {
			rows = append(rows, injectorRow("extra-"+key, "extra variable", key, fmt.Sprint(inj.ExtraVars[key])))
		}
		for _, key := range sortedKeys(inj.File) {
			rows = append(rows, injectorRow("file-"+key, "file", fileLabel(key), inj.File[key]))
		}
		return rows, nil
	}
}

// injectorRow builds one section row for an injector.
func injectorRow(id, target, name, template string) view.Row {
	return view.Row{
		ID: id,
		Cells: view.Cells{
			"target":   target,
			"name":     name,
			"template": template,
		},
	}
}

// fileLabel reads a file entry's label out of its key, naming the single-file
// spelling rather than showing an empty cell for it.
func fileLabel(key string) string {
	if key == "template" {
		return "(single file)"
	}
	return strings.TrimPrefix(key, filePrefix)
}

// sortedKeys returns a string map's keys in order, so the table does not
// reshuffle between page loads the way a raw map range would.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedAnyKeys is sortedKeys for the extra-vars map, whose values are any.
func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// injectorTargetOptions offers the three places an input can be injected.
func injectorTargetOptions(context.Context) ([]view.Option, error) {
	return []view.Option{
		{Label: "environment variable", Value: targetEnv},
		{Label: "extra variable", Value: targetExtraVar},
		{Label: "generated file", Value: targetFile},
	}, nil
}

// addInjectorAction adds one injector to a type's document.
//
// One action with a target selector rather than three, so it carries one
// relation: the affordance layer keys one relation to one endpoint per
// resource, and three add controls would need three. The Submit reads the
// stored type so the input schema beside the injectors is carried forward,
// and the store compiles the template and refuses a dangerous target.
func addInjectorAction(store credstore.Store) view.RecordAction {
	return view.RecordAction{
		Name:     addInjectorName,
		Label:    "Add injector",
		Heading:  "Add an injector to this credential type",
		Endpoint: &apispec.SetCredentialTypeInjectors,
		Fields: []view.Field{
			{
				Name: "target", Label: "TARGET", Kind: view.KindSelect, Required: true, InForm: true,
				Options: injectorTargetOptions,
				Help:    "Where the value goes: an environment variable, an extra variable, or a generated file.",
			},
			{
				Name: "name", Label: "NAME", Kind: view.KindText, Required: true, InForm: true,
				Autocomplete: "off",
				Help:         "The environment variable name, extra-variable key, or file label. An env name that would change how the run executes is refused.",
			},
			{
				Name: "template", Label: "TEMPLATE", Kind: view.KindLongText, Required: true, InForm: true,
				Help: "A template over this type's input ids, for example {{ api_token }}. It is compiled when you save.",
			},
		},
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", nil, credstore.ErrNotFound
			}
			ct, err := store.GetType(ctx, numeric)
			if err != nil {
				return "", nil, err
			}

			name := strings.TrimSpace(v.Get("name"))
			template := strings.TrimSpace(v.Get("template"))
			addInjector(&ct.Injectors, v.Get("target"), name, template)

			if _, err := store.UpdateType(ctx, numeric, ct.CredentialType); err != nil {
				return "", injectorFault(err), nil
			}
			return "/ui/" + Name + "/" + id + "?tab=" + view.TabSlug(injectorsTitle), nil, nil
		},
	}
}

// addInjector writes one injector into the document, creating whichever map
// it belongs to. A file uses the multi-file spelling, which is the general
// one; credtype refuses mixing it with the single-file spelling in the same
// type.
func addInjector(inj *credtype.Injectors, target, name, template string) {
	switch target {
	case targetExtraVar:
		if inj.ExtraVars == nil {
			inj.ExtraVars = map[string]any{}
		}
		inj.ExtraVars[name] = template
	case targetFile:
		if inj.File == nil {
			inj.File = map[string]string{}
		}
		inj.File[filePrefix+name] = template
	default:
		if inj.Env == nil {
			inj.Env = map[string]string{}
		}
		inj.Env[name] = template
	}
}

// injectorFault turns the store's injector refusals into messages on the
// control that caused them, so a reserved environment variable name or a
// template referencing an input the type does not declare is answered on
// the box that was wrong.
func injectorFault(err error) view.FieldErrors {
	errs := view.FieldErrors{}
	if err == nil {
		return errs
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "environment variable") || strings.Contains(msg, "reserved"):
		errs.Add("name", msg)
	case strings.Contains(msg, "single-file") || strings.Contains(msg, "multi-file") || strings.Contains(msg, "file"):
		errs.Add("name", msg)
	case strings.Contains(msg, "undeclared") || strings.Contains(msg, "references") || strings.Contains(msg, "template") || strings.Contains(msg, "render"):
		errs.Add("template", msg)
	default:
		errs.Add("template", msg)
	}
	return errs
}

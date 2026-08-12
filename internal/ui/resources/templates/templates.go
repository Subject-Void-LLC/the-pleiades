// Package templates is the Templates view resource: the saved, reusable
// definitions of what this platform runs, where it runs it, and how.
//
// It replaces the Configurations view, which was declared rather than
// implemented and was named after the wrong half of the object. AWX calls
// the saved thing a Job Template and Semaphore a Task Template; this
// project's own spec used "LaunchConfig" for the launch-time override
// bundle a schedule or a relaunch attaches, so the previous view had taken
// its name from the overrides and then described the definition. A template
// is what to run; a saved configuration is one answer to how, and both now
// exist under their own names.
//
// The kind badge on every row is the user-visible half of the open Kind
// registry. A native runbook and a sandboxed Ansible playbook are both
// launchable, carry the same surveys, access and history, and reach
// different execution adapters with different trust and performance
// stories, so an operator scanning a list should be able to tell which is
// which without opening one.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package templates

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "templates"

// fields drive the table, the form, the detail list, validation and the
// mobile card layout from one declaration.
func fields(sets inventory.SetStore) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, Autocomplete: "off",
			Help:          "What this saved definition is called. Unique within its organization, so two tenants may both have a \"patch the edge routers\".",
			InList:        true,
			InForm:        true,
			MobilePrimary: true,
		},
		{
			Name: "kind", Label: "KIND", Kind: view.KindSelect,
			Required: true, InForm: true, InList: true,
			BadgeClass: kindBadge,
			Help:       "What sort of thing this runs, which decides the executor it reaches.",
			Options:    kindOptions,
		},
		{
			Name: "definition", Label: "RUNS", Kind: view.KindText,
			Required: true, MaxLen: 512, Autocomplete: "off",
			InForm: true, InList: true,
			Help: "The runbook id or playbook path this template runs. Not editable afterwards: re-pointing a saved definition at different code, while it keeps its name, its grants and its job history, is a copy rather than an edit.",
		},
		{
			Name: "inventory", Label: "INVENTORY", Kind: view.KindSelect,
			Required: true, InForm: true, InList: true,
			References: "inventories",
			Help:       "The set of devices this runs against. Required, and it is what gives every job this template launches its organization.",
			Options:    inventoryOptions(sets),
		},
		{
			Name: "organization", Label: "ORGANIZATION", Kind: view.KindReadOnly,
			InList:     true,
			References: "organizations",
			Help:       "Derived from the inventory rather than chosen: a template that could name its own tenant could tag its jobs with somebody else's.",
		},
		{
			Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
			MaxLen: 1024, InForm: true,
			Help: "What this template is for, for somebody who did not write it.",
		},
		{
			Name: "prompts", Label: "PROMPT ON LAUNCH", Kind: view.KindLookup,
			InForm: true,
			Help:   "Which fields a launch may override. Everything else is locked to what this template was saved with, and a launch supplying a locked field is told so by name rather than having it applied or silently dropped.",
			// Options come from the registered kinds rather than from this
			// package, because the field set is per kind: a template of a
			// kind this file has never seen still renders its own fields.
			Options: promptOptions,
		},
		{
			Name: "allow_simultaneous", Label: "ALLOW PARALLEL RUNS", Kind: view.KindBool,
			InForm: true,
			Help:   "Whether more than one job from this template may run at once. Off by default: two runs of the same change against the same fleet is more often a mistake than an intention.",
		},
		{
			Name: "survey", Label: "SURVEY", Kind: view.KindReadOnly,
			InList: true,
			Help:   "How many questions a launching operator is asked. The questions themselves are listed below.",
		},
	}
}

// kindBadge paints the kind indicator from the descriptor's own declared
// class, so a kind arriving in a file this package has never seen brings
// its own colour rather than falling into a default nothing chose.
func kindBadge(kind string) string {
	if d, ok := launch.Lookup(kind); ok {
		return d.BadgeClass
	}
	return "badge-changed"
}

// kindOptions offers every registered kind. A template of an unregistered
// kind cannot be created, which is what makes the badge honest.
func kindOptions(context.Context) ([]view.Option, error) {
	descriptors := launch.Kinds()
	out := make([]view.Option, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, view.Option{Label: d.Label, Value: d.Kind})
	}
	return out, nil
}

// promptOptions is the union of the fields every registered kind declares.
//
// A union rather than the chosen kind's own set, and that is an honest
// limitation rather than a design: the option list is resolved before the
// form is rendered, so it cannot depend on a value the reader has not
// picked yet. Choosing a field the selected kind does not have is refused
// at the write with a message naming it, which is a legible failure rather
// than a silent one. The alternative, offering only what every kind shares,
// would hide a playbook's tags from a form that can perfectly well set
// them.
func promptOptions(context.Context) ([]view.Option, error) {
	seen := map[string]string{}
	for _, d := range launch.Kinds() {
		for _, f := range d.Fields {
			if _, ok := seen[f.Name]; !ok {
				seen[f.Name] = f.Label
			}
		}
	}

	out := make([]view.Option, 0, len(seen))
	for name, label := range seen {
		out = append(out, view.Option{Label: label, Value: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out, nil
}

// inventoryOptions offers the inventories a template may target. A select
// rather than a number field, for the reason the Inventories form gives
// about its own organization: a control asking somebody to type a numeric
// primary key is a control that will receive the wrong one.
func inventoryOptions(sets inventory.SetStore) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		found, err := sets.List(ctx, inventory.SetQuery{Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(found))
		for _, set := range found {
			label := set.Name
			if set.OrganizationName != "" {
				// Qualified by tenant, because two organizations may each
				// have a "production" and picking the wrong one points a
				// template at the wrong fleet.
				label = set.Name + " (" + set.OrganizationName + ")"
			}
			out = append(out, view.Option{Label: label, Value: strconv.Itoa(set.ID)})
		}
		return out, nil
	}
}

// reader adapts the launch.Store's read half.
type reader struct{ store launch.Store }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[launch.Template], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	after := 0
	if q.Cursor != "" {
		parsed, err := strconv.Atoi(q.Cursor)
		if err != nil {
			// A malformed cursor is treated as no cursor rather than as an
			// error: the worst outcome is the first page, which is a page
			// that works.
			parsed = 0
		}
		after = parsed
	}

	// One more than asked for, so a next page is observed rather than
	// inferred from a page that happened to come back full.
	found, err := r.store.List(ctx, launch.Query{After: after, Limit: limit + 1, Search: q.Search})
	if err != nil {
		return view.Page[launch.Template]{}, err
	}

	page := view.Page[launch.Template]{Items: found}
	if len(found) > limit {
		page.Items = found[:limit]
		page.NextCursor = strconv.Itoa(found[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (launch.Template, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return launch.Template{}, launch.ErrNotFound
	}
	return r.store.Get(ctx, numeric)
}

// writer adapts the write half.
type writer struct{ store launch.Store }

func (w writer) Create(ctx context.Context, tmpl launch.Template) (string, error) {
	created, err := w.store.Create(ctx, tmpl)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

func (w writer) Update(ctx context.Context, id string, tmpl launch.Template) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return launch.ErrNotFound
	}

	// The survey is carried forward from storage rather than from the
	// submission, because this form does not author one: a survey is an
	// ordered list of typed questions, which the shared form machinery has
	// no control for, and submitting an empty one would delete the
	// questions somebody wrote. The kind, definition and inventory are
	// carried forward by the store itself for a stronger reason.
	existing, err := w.store.Get(ctx, numeric)
	if err != nil {
		return err
	}
	tmpl.ID = numeric
	tmpl.Survey = existing.Survey
	tmpl.RequiredCaps = existing.RequiredCaps
	return w.store.Update(ctx, tmpl)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return launch.ErrNotFound
	}
	return w.store.Delete(ctx, numeric)
}

// projector turns a template into the rows, forms and submissions the
// shared view machinery reads.
func projector() view.Projector[launch.Template] {
	return view.Projector[launch.Template]{
		Row: func(tmpl launch.Template) view.Row {
			return view.Row{
				ID: strconv.Itoa(tmpl.ID),
				// The ids the cells link to; their names are in Cells, so
				// no column renders a bare foreign key
				// (FAILURE_PATTERNS.md #107).
				Refs: map[string]string{
					"inventory":    strconv.Itoa(tmpl.InventoryID),
					"organization": strconv.Itoa(tmpl.OrganizationID),
				},
				Cells: view.Cells{
					"name":               tmpl.Name,
					"description":        tmpl.Description,
					"kind":               tmpl.KindName,
					"definition":         tmpl.Definition,
					"inventory":          tmpl.InventoryName,
					"organization":       tmpl.OrganizationName,
					"prompts":            strings.Join(tmpl.Prompts, ", "),
					"allow_simultaneous": yesNo(tmpl.AllowSimultaneous),
					"survey":             surveySummary(tmpl.Survey),
				},
			}
		},
		Form: func(tmpl launch.Template) map[string]string {
			return map[string]string{
				"name":               tmpl.Name,
				"description":        tmpl.Description,
				"kind":               tmpl.KindName,
				"definition":         tmpl.Definition,
				"inventory":          strconv.Itoa(tmpl.InventoryID),
				"prompts":            strings.Join(tmpl.Prompts, ","),
				"allow_simultaneous": yesNo(tmpl.AllowSimultaneous),
			}
		},
		Bind: func(v view.Values) (launch.Template, view.FieldErrors) {
			errs := view.FieldErrors{}

			inventoryID, err := strconv.Atoi(strings.TrimSpace(v.Get("inventory")))
			if err != nil || inventoryID < 1 {
				// Blamed on the field rather than answered with a 500. A
				// template with no inventory is the one thing the store
				// refuses outright, so catching it here is what turns a
				// refusal into a message next to the control.
				errs.Add("inventory", "Choose the inventory this template runs against.")
				return launch.Template{}, errs
			}

			return launch.Template{
				Name:              v.Get("name"),
				Description:       v.Get("description"),
				KindName:          strings.TrimSpace(v.Get("kind")),
				Definition:        strings.TrimSpace(v.Get("definition")),
				InventoryID:       inventoryID,
				Prompts:           v.Selected("prompts"),
				AllowSimultaneous: v.Bool("allow_simultaneous"),
			}, errs
		},
	}
}

// surveySummary says what a survey asks, in the two facts a list column can
// carry: whether it prompts at all, and how many questions.
//
// "Off" rather than a blank for a survey somebody wrote and disabled,
// because those are different states and the questions are still there.
func surveySummary(s launch.Survey) string {
	switch {
	case len(s.Questions) == 0:
		return "none"
	case !s.Enabled:
		return "off (" + strconv.Itoa(len(s.Questions)) + ")"
	default:
		return strconv.Itoa(len(s.Questions))
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// Register wires this view over the live template store.
//
// It takes five ports, which is more than any other view here, and each one
// is a section rather than a nicety: the store is the resource, the sets
// fill the inventory select, the dispatcher launches, the jobs answer "what
// has this run", and the bindings answer "who can reach it".
func Register(store launch.Store, sets inventory.SetStore, jobs dispatch.JobStore,
	dispatcher *api.Dispatcher, bindings access.Bindings) error {

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Templates",
		NavLabel: "TEMPLATES",
		// First within Resources, which is where AWX puts the same object:
		// it is what an operator touches daily, and everything else in the
		// group is something it names or something that names it.
		NavOrder: 40,
		NavGroup: view.NavGroupResources,
		Summary:  "What to run, where to run it, and how: the saved definitions this platform launches.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   fields(sets),
		Ops: view.Ops{
			List:   &apispec.ListTemplates,
			Get:    &apispec.GetTemplate,
			Create: &apispec.CreateTemplate,
			Update: &apispec.UpdateTemplate,
			Delete: &apispec.DeleteTemplate,
		},
		Actions: []view.RecordAction{
			launchAction(store, dispatcher),
			copyAction(store),
		},
		Sections: []view.Section{
			surveySection(store),
			accessSection(store, bindings),
			notificationsSection(),
			completedJobsSection(jobs),
		},
		Handlers: view.MustBind(reader{store}, writer{store}, projector()),
	})
}

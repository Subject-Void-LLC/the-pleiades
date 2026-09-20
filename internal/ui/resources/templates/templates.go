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
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "templates"

// fields drive the table, the form, the detail list, validation and the
// mobile card layout from one declaration.
func fields(sets inventory.SetStore, catalog launch.Catalog) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, Autocomplete: "off",
			Help:          "What this saved definition is called. Unique within its organization, so two tenants may both have a \"patch the edge routers\".",
			InList:        true,
			InForm:        true,
			MobilePrimary: true,
		},
		// KIND is derived, never asked. AWX's template form has no such
		// control either: you pick the content, and what it is follows.
		// This used to be a select the operator answered beside a
		// free-text definition, which made an inconsistent pair (kind
		// runbook, definition site) a submittable state and made the
		// operator perform the router's job.
		{
			Name: "kind", Label: "KIND", Kind: view.KindBadge,
			InList:     true,
			BadgeClass: kindBadge,
			Help:       "What sort of thing this runs, which decides the executor it reaches. Derived from what you chose to run, never chosen on its own.",
		},
		// RUNS is a choice over the catalog, never typed. Each option is
		// one definition the platform can resolve right now, runbooks and
		// playbooks in one list, and the submitted value carries the kind
		// with it. It used to be a free-text control asking for "the
		// runbook id or playbook path", which inverted the defining
		// property of a template form: the operator had to already know
		// what only the catalog knows, a typo saved fine and failed later
		// as a failed job, and the enumeration was one call away the
		// whole time (the Runbooks view was already built on it).
		//
		// Set once, both halves: re-pointing a saved definition at
		// different code, while it keeps its name, its grants and its job
		// history, is a copy rather than an edit.
		{
			Name: "definition", Label: "RUNS", Kind: view.KindSelect,
			Required: true, Immutable: true, InForm: true, InList: true,
			Help:    "What this template runs, chosen from everything this deployment can launch. Set once: a template that pointed at different code would keep its name, its grants and its job history.",
			Options: runsOptions(catalog),
		},
		{
			Name: "inventory", Label: "INVENTORY", Kind: view.KindSelect,
			Required: true, Immutable: true, InForm: true, InList: true,
			References: "inventories",
			Help:       "The set of devices this runs against. Required, and it is what gives every job this template launches its organization. Set once, because moving it would re-tenant every job this template goes on to launch.",
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
			Name: "allow_simultaneous", Label: "ALLOW PARALLEL RUNS", Kind: view.KindBool,
			InForm: true,
			Help:   "Whether more than one job from this template may run at once. Off by default: two runs of the same change against the same fleet is more often a mistake than an intention.",
		},
		{
			Name: "survey", Label: "SURVEY", Kind: view.KindReadOnly,
			InList: true,
			Help:   "How many questions a launching operator is asked. The questions themselves are listed below.",
		},
		{
			Name: "activity", Label: "ACTIVITY", Kind: view.KindBadge,
			InList:     true,
			BadgeClass: activityBadge,
			Help:       "This template's most recent job. A completed dispatch that never reached any device reads as failed, since \"completed\" means the fan-out finished, not that it reached anywhere.",
		},
		{
			Name: "last_ran", Label: "LAST RAN", Kind: view.KindText,
			InList: true,
			Help:   "When this template was last launched.",
		},
	}
}

// activityLabel says what a template's most recent job actually did, in
// the one word a list cell can carry.
//
// It reads FailedCount rather than trusting State alone, because a
// "completed" job (internal/ent/schema/job.go's own State comment: dispatch
// tallies finishing, not per-device execution success) with a nonzero
// FailedCount still failed to reach every device it targeted, and a green
// "completed" badge on that row would hide it.
func activityLabel(j launch.JobSummary) string {
	if j.FailedCount > 0 {
		return "failed"
	}
	return j.State
}

// activityBadge colours the Activity cell. "never run" gets a neutral
// badge, a different fact from a job that ran and failed, and every other
// value reuses the same state colouring the Completed jobs section already
// applies (sections.go's stateBadge), so a template's list row and its own
// detail page never disagree about what a state means.
func activityBadge(value string) string {
	if value == "never run" {
		return "badge-neutral"
	}
	return stateBadge(value)
}

// activityCell and lastRanCell read the same RecentJobs slice for the two
// facts the list renders about it: what happened, and when.
func activityCell(recent []launch.JobSummary) string {
	if len(recent) == 0 {
		return "never run"
	}
	return activityLabel(recent[0])
}

func lastRanCell(recent []launch.JobSummary) string {
	if len(recent) == 0 {
		return ""
	}
	return recent[0].CreatedAt.UTC().Format(time.RFC3339)
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

// runsSeparator joins a kind to its definition in the picker's submitted
// value, and splits them back apart in Bind. The kind grammar forbids a
// colon (registry keys are lowercase words), so the split is unambiguous.
const runsSeparator = ":"

// runsValue is the picker's submitted encoding of one catalog entry, the
// same kind-carrying single-value shape the Contacts view's owner control
// uses: one control, so an inconsistent pair is not a submittable state.
func runsValue(entry launch.CatalogEntry) string {
	return entry.Kind + runsSeparator + entry.Definition
}

// runsOptions offers every definition the deployment can launch, both
// kinds in one list, each labelled with what it is.
//
// This is AWX's auto-populated Playbook dropdown, adapted to a platform
// with two content sources instead of one project checkout. The option
// set IS the authorization to save: view.Validate refuses a submitted
// value that was never offered, and the store re-verifies against the
// same catalog for callers that do not come through this form, so what
// can be picked and what can be saved are one list read twice.
//
// Deliberately no typed fallback, unlike AWX's. Its own documentation
// says what the fallback is worth ("If you enter a filename that is not
// valid, the template will display an error, or cause the job to fail"),
// and the failure mode it produces here, a template that saves and then
// fails as a failed job, is the one this control exists to remove. The
// JSON API remains the escape hatch for automation that knows what it
// wants, and it is verified at create too.
func runsOptions(catalog launch.Catalog) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		entries, err := catalog.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(entries))
		for _, entry := range entries {
			label := entry.Definition
			if d, ok := launch.Lookup(entry.Kind); ok {
				label += " (" + d.Label + ")"
			}
			out = append(out, view.Option{Label: label, Value: runsValue(entry)})
		}
		return out, nil
	}
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

// recentJobsPerTemplate bounds the list page's Activity column: enough to
// show a short recent-outcome history without a row growing without bound
// for a template that has run thousands of times.
const recentJobsPerTemplate = 3

// reader adapts the launch.Store's read half.
type reader struct {
	store launch.Store
	jobs  dispatch.JobStore
}

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

	// One batched fetch for the whole page's Activity and Last Ran columns,
	// attached onto each template before it is erased into a Row, rather
	// than one job query per row (AWX_PARITY_ROADMAP.md B2's own gate).
	if err := r.attachRecentJobs(ctx, page.Items); err != nil {
		return view.Page[launch.Template]{}, err
	}
	return page, nil
}

// attachRecentJobs fills in each template's RecentJobs in place, from one
// RecentForTemplates call across the whole page.
func (r reader) attachRecentJobs(ctx context.Context, items []launch.Template) error {
	if r.jobs == nil || len(items) == 0 {
		return nil
	}

	ids := make([]int, len(items))
	for i, tmpl := range items {
		ids[i] = tmpl.ID
	}

	byTemplate, err := r.jobs.RecentForTemplates(ctx, ids, recentJobsPerTemplate)
	if err != nil {
		return err
	}

	for i := range items {
		tmpl := &items[i]
		found := byTemplate[tmpl.ID]
		if len(found) == 0 {
			continue
		}
		tmpl.RecentJobs = make([]launch.JobSummary, 0, len(found))
		for _, j := range found {
			tmpl.RecentJobs = append(tmpl.RecentJobs, launch.JobSummary{
				JobID:           j.JobID,
				State:           j.State,
				DispatchedCount: j.DispatchedCount,
				FailedCount:     j.FailedCount,
				CreatedAt:       j.CreatedAt,
			})
		}
	}
	return nil
}

func (r reader) Get(ctx context.Context, id string) (launch.Template, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return launch.Template{}, launch.ErrNotFound
	}
	tmpl, err := r.store.Get(ctx, numeric)
	if err != nil {
		return launch.Template{}, err
	}

	// Filled in the same way List's page is, on a slice of one, so a
	// template's own detail page and its row on the list agree about what
	// its Activity and Last Ran say.
	items := []launch.Template{tmpl}
	if err := r.attachRecentJobs(ctx, items); err != nil {
		return launch.Template{}, err
	}
	return items[0], nil
}

// writer adapts the write half.
type writer struct{ store launch.Store }

func (w writer) Create(ctx context.Context, tmpl launch.Template) (string, error) {
	created, err := w.store.Create(ctx, tmpl)
	if err != nil {
		return "", nameTaken(err)
	}
	return strconv.Itoa(created.ID), nil
}

// nameTaken turns the store's uniqueness refusal into a fault the form can
// render against the field that caused it, rather than the 500 and the
// plain text "internal error" an unwrapped launch.ErrExists produces. See
// the identical helper in the organizations resource, and actions.go's own
// launch.ErrExists case, which has always got this right one layer over.
func nameTaken(err error) error {
	if errors.Is(err, launch.ErrExists) {
		return view.FieldFault{Field: "name", Message: "A template with that name already exists in this organization."}
	}
	return err
}

func (w writer) Update(ctx context.Context, id string, tmpl launch.Template) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return launch.ErrNotFound
	}

	// The survey is carried forward from storage rather than from the
	// submission, because this form does not author one and submitting an
	// empty one would delete the questions somebody wrote. The survey is
	// authored by its own section's controls instead, one question at a
	// time against a list you can see, which is what an ordered list of
	// typed questions needs and what a flat record form cannot be. The
	// kind, definition and inventory are carried forward by the store
	// itself for a stronger reason.
	existing, err := w.store.Get(ctx, numeric)
	if err != nil {
		return err
	}
	tmpl.ID = numeric
	tmpl.Survey = existing.Survey
	tmpl.RequiredCaps = existing.RequiredCaps
	return nameTaken(w.store.Update(ctx, tmpl))
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
					"allow_simultaneous": yesNo(tmpl.AllowSimultaneous),
					"survey":             surveySummary(tmpl.Survey),
					"activity":           activityCell(tmpl.RecentJobs),
					"last_ran":           lastRanCell(tmpl.RecentJobs),
				},
			}
		},
		// The edit form's prefill: the static fields here, plus this
		// template's own kind fields from defaultsValues (defaults.go),
		// which is where "prompts" now lives, one checkbox per field
		// instead of the one control this used to be.
		//
		// allow_simultaneous is "true"/absent rather than yesNo's
		// "yes"/"no": the checkbox control only renders `checked` for the
		// literal string "true" (view/field.templ), so prefilling "yes"
		// left every KindBool control unchecked regardless of the stored
		// value. A template saved with AllowSimultaneous true would open
		// for editing showing it off, and saving with nothing else changed
		// would silently turn it off for real -- the same class of defect
		// view.Field.Immutable exists to catch, just not one it covers,
		// since this field is not immutable, it was just prefilled wrong.
		Form: func(tmpl launch.Template) map[string]string {
			values := map[string]string{
				"name":        tmpl.Name,
				"description": tmpl.Description,
				"inventory":   strconv.Itoa(tmpl.InventoryID),
			}
			if tmpl.AllowSimultaneous {
				values["allow_simultaneous"] = "true"
			}
			for name, value := range defaultsValues(tmpl) {
				values[name] = value
			}
			return values
		},
		Bind: func(v view.Values) (launch.Template, view.FieldErrors) {
			errs := view.FieldErrors{}

			// Both the inventory and the definition are Immutable, so an
			// edit form renders neither and an edit submission carries
			// neither; Update reads both from storage. Parsing them
			// unconditionally refused every template edit.
			var inventoryID int
			var kindName, definition string
			if !v.Editing() {
				parsed, err := strconv.Atoi(strings.TrimSpace(v.Get("inventory")))
				if err != nil || parsed < 1 {
					// Blamed on the field rather than answered with a 500. A
					// template with no inventory is the one thing the store
					// refuses outright, so catching it here is what turns a
					// refusal into a message next to the control.
					errs.Add("inventory", "Choose the inventory this template runs against.")
					return launch.Template{}, errs
				}
				inventoryID = parsed

				// The picker's value carries both halves; the kind is derived
				// from the choice, never submitted on its own. view.Validate
				// has already refused a value the catalog never offered, so a
				// malformed one here means the option set itself was misbuilt,
				// which is a field error rather than a panic.
				kind, def, found := strings.Cut(strings.TrimSpace(v.Get("definition")), runsSeparator)
				if !found || kind == "" || def == "" {
					errs.Add("definition", "Choose what this template runs.")
					return launch.Template{}, errs
				}
				kindName, definition = kind, def
			}

			// A create submission carries none of these controls at all
			// (defaultsFields only resolves on an edit), so this reads as
			// empty for one without needing its own branch: Values answers
			// empty for a name its descriptor never declared.
			defaults, prompts, defaultsErrs := bindDefaults(v)
			for name, messages := range defaultsErrs {
				for _, m := range messages {
					errs.Add(name, m)
				}
			}

			return launch.Template{
				Name:              v.Get("name"),
				Description:       v.Get("description"),
				KindName:          kindName,
				Definition:        definition,
				InventoryID:       inventoryID,
				Defaults:          defaults,
				Prompts:           prompts,
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
	dispatcher *api.Dispatcher, bindings access.Bindings, catalog launch.Catalog,
	creds credentials, schedules schedule.Store) error {

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Templates",
		NavLabel: "TEMPLATES",
		// First within Resources, which is where AWX puts the same object:
		// it is what an operator touches daily, and everything else in the
		// group is something it names or something that names it.
		NavOrder:         40,
		NavGroup:         view.NavGroupResources,
		Summary:          "What to run, where to run it, and how: the saved definitions this platform launches.",
		Status:           view.StatusImplemented,
		IDField:          "name",
		StatusBadgeField: "activity",
		Fields:           fields(sets, catalog),
		// One control and one checkbox per field of this template's own
		// kind, resolved per record because the field set is per kind
		// (defaults.go). Never offered on create: the kind is not chosen
		// until the RUNS picker's submission is parsed.
		FieldsFor: defaultsFields(store),
		Ops: view.Ops{
			List:   &apispec.ListTemplates,
			Get:    &apispec.GetTemplate,
			Create: &apispec.CreateTemplate,
			Update: &apispec.UpdateTemplate,
			Delete: &apispec.DeleteTemplate,
		},
		Actions: []view.RecordAction{
			launchAction(store, dispatcher, creds),
			bindCredentialsAction(creds),
			copyAction(store),
			addQuestionAction(store, dispatcher.AllowsProgramContent()),
		},
		Sections: []view.Section{
			surveySection(store, dispatcher.AllowsProgramContent()),
			schedulesSection(store, schedules),
			accessSection(store, bindings),
			notificationsSection(),
			jobsSection(jobs),
		},
		Handlers: view.MustBind(reader{store: store, jobs: jobs}, writer{store}, projector()),
	})
}

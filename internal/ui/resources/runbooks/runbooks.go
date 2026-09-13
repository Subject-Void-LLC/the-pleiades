// Package runbooks is the Runbooks view resource: a catalog of what can be
// dispatched.
//
// It is read-only, and deliberately so rather than for want of time.
// Runbooks arrive from RUNBOOK_DIR and, eventually, from GitOps sync. A
// write path would open a new caller-controlled filesystem surface on the
// control plane in order to duplicate a job that version control already
// does better, so this resource supplies a Reader and no Writer at all --
// which is the entire reason those are separate interfaces.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package runbooks

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	runbookkind "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "runbooks"

var fields = []view.Field{
	{
		Name: "name", Label: "NAME", Kind: view.KindText,
		InList: true, MobilePrimary: true, Sortable: true,
		Help: "The runbook's own title, from its name: key. Falls back to the id.",
	},
	{
		Name: "id", Label: "RUNBOOK", Kind: view.KindText,
		InList: true, Sortable: true,
		Help: "The id this runbook is dispatched by.",
	},
	{
		Name: "category", Label: "CATEGORY", Kind: view.KindText, InList: true,
		Help: "The bucket this runbook is filed under.",
	},
	{
		Name: "labels", Label: "LABELS", Kind: view.KindTags, InList: true,
		Help: "Free-form markers for filtering. Not Ansible tags, which select tasks at run time.",
	},
	{
		Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
		Help: "What this runbook does.",
	},
	{
		Name: "capabilities", Label: "REQUIRES", Kind: view.KindTags, InList: true,
		Help: "Every capability a device must have for this runbook to run against it.",
	},
	{
		Name: "interruptible", Label: "INTERRUPTIBLE", Kind: view.KindBadge,
		InList: true, BadgeClass: interruptibleBadge,
		Help: "Whether a run may be aborted partway, or must finish once started.",
	},
	{Name: "capability_count", Label: "REQUIREMENTS", Kind: view.KindReadOnly},
}

// interruptibleBadge colours the answer rather than only stating it. The
// word is rendered inside the badge too, so nothing here is encoded in
// colour alone.
func interruptibleBadge(value string) string {
	if value == "yes" {
		return "badge-ok"
	}
	return "badge-changed"
}

// reader adapts the runbook.Source port.
//
// Source.List returns ids rather than compiled runbooks, on the grounds
// that compiling every runbook to answer "what can I run" makes opening a
// list cost as much as running one. This reader honours that: it pages over
// the ids and compiles only the ones on the page being rendered.
type reader struct{ source runbook.Source }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[*runbook.Runbook], error) {
	ids, err := r.source.List(ctx)
	if err != nil {
		return view.Page[*runbook.Runbook]{}, err
	}
	sort.Strings(ids)

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	// Keyset paging over a sorted, in-memory slice. The cursor is the last
	// id of the previous page rather than an index, for the same reason
	// every other list here pages that way: an offset into a directory
	// listing that gained a file between two requests silently skips one.
	start := 0
	if q.Cursor != "" {
		start = sort.SearchStrings(ids, q.Cursor)
		if start < len(ids) && ids[start] == q.Cursor {
			start++
		}
	}
	if start > len(ids) {
		start = len(ids)
	}

	end := min(start+limit, len(ids))

	var page view.Page[*runbook.Runbook]
	for _, id := range ids[start:end] {
		rb, err := r.source.Get(ctx, id)
		if err != nil {
			// One unreadable runbook must not blank the whole catalog. It
			// is skipped rather than propagated, because a directory
			// holding a file that does not compile is an ordinary state
			// for this source and the other entries are still true.
			continue
		}
		if !matches(rb, q.Search) {
			continue
		}
		page.Items = append(page.Items, rb)
	}
	if end < len(ids) {
		page.NextCursor = ids[end-1]
	}
	return page, nil
}

// matches is the catalog filter: one box that searches title, id, category
// and labels together.
//
// One box rather than a field per axis, deliberately. A reader looking for
// the patching runbook types "patch" and does not first decide whether that
// is its name, its category or one of its labels -- and a filter that made
// them choose would be one they got wrong half the time. AWX's own template
// search behaves this way for the same reason.
//
// Filtering happens after the page is compiled rather than before, which is
// an honest limitation: with a filter applied, a page can come back shorter
// than its limit while more matches exist further on. The alternative is
// compiling every runbook in the directory on every keystroke, which is the
// cost Source.List's own doc comment exists to avoid. A directory large
// enough for this to bite is one that needs an index, not a bigger loop.
func matches(rb *runbook.Runbook, search string) bool {
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return true
	}

	haystack := []string{rb.ID, rb.Name, rb.Category, rb.Description}
	haystack = append(haystack, rb.Labels...)
	for _, field := range haystack {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}

func (r reader) Get(ctx context.Context, id string) (*runbook.Runbook, error) {
	return r.source.Get(ctx, id)
}

// templateAction turns a runbook in the catalog into a saved template.
//
// It replaces the Run control that used to live here, and the replacement
// is the point rather than a rename. Running from the catalog meant
// prompting for a target group and nothing else, which is the whole launch
// surface this phase exists to replace: a group name has no tenant, so the
// job it produced belonged to no organization, and there was nowhere to
// record limits, verbosity, a survey, or which fields a launcher may
// change. Those all belong to a template, so the catalog's job is to be a
// catalog and launching has one home.
//
// The action lives on the runbook because that is where an operator already
// is. Having found the runbook they want, being sent to an empty template
// form to retype its id is the kind of step that makes people keep a text
// file of ids beside the UI.
func templateAction(store launch.Store, sets inventory.SetStore) view.RecordAction {
	return view.RecordAction{
		Name:     "template",
		Label:    "Create template",
		Heading:  "Create a template from this runbook",
		Endpoint: &apispec.CreateTemplate,
		Fields: []view.Field{
			{
				Name: "name", Label: "TEMPLATE NAME", Kind: view.KindText,
				Required: true, MaxLen: 253, Autocomplete: "off", InForm: true,
				Help: "What the saved definition is called. Unique within the organization the inventory belongs to.",
			},
			{
				Name: "inventory", Label: "INVENTORY", Kind: view.KindSelect,
				Required: true, InForm: true,
				Help:    "The set of devices it runs against. Required, and it is what gives every job this template launches its organization.",
				Options: inventoryOptions(sets),
			},
		},
		Submit: func(ctx context.Context, runbookID string, v view.Values) (string, view.FieldErrors, error) {
			inventoryID, err := strconv.Atoi(strings.TrimSpace(v.Get("inventory")))
			if err != nil || inventoryID < 1 {
				return "", view.FieldErrors{"inventory": {"Choose the inventory this template runs against."}}, nil
			}

			created, err := store.Create(ctx, launch.Template{
				Name:        v.Get("name"),
				KindName:    runbookkind.Kind,
				Definition:  runbookID,
				InventoryID: inventoryID,
				// No defaults and no prompts. A template created from the
				// catalog is the smallest honest one: it says what to run
				// and where, and everything else is edited on the template
				// itself, where the form knows which fields its kind has.
			})
			switch {
			case errors.Is(err, launch.ErrExists):
				return "", view.FieldErrors{"name": {"A template with that name already exists in this organization."}}, nil
			case errors.Is(err, launch.ErrInvalidTemplate):
				return "", view.FieldErrors{"name": {"This runbook id is not one a template can name."}}, nil
			case errors.Is(err, launch.ErrNotFound):
				return "", view.FieldErrors{"inventory": {"That inventory no longer exists. Reload the form."}}, nil
			case err != nil:
				return "", nil, err
			}

			// Straight to the template that was just created, because the
			// next thing anybody wants is to launch it or to open what it
			// runs with.
			return "/ui/templates/" + strconv.Itoa(created.ID), nil, nil
		},
	}
}

// inventoryOptions offers the inventories a template may target, qualified
// by tenant: two organizations may each have a "production", and picking
// the wrong one points a template at the wrong fleet.
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
				label = set.Name + " (" + set.OrganizationName + ")"
			}
			out = append(out, view.Option{Label: label, Value: strconv.Itoa(set.ID)})
		}
		return out, nil
	}
}

// Register wires this view over the live runbook source, the template store
// its one action writes to, and the inventories that action offers.
func Register(source runbook.Source, store launch.Store, sets inventory.SetStore) error {
	projector := view.Projector[*runbook.Runbook]{
		Row: func(rb *runbook.Runbook) view.Row {
			if rb == nil {
				return view.Row{}
			}
			names := make([]string, 0, len(rb.Required))
			for _, c := range rb.Required {
				names = append(names, string(c))
			}
			return view.Row{ID: rb.ID, Cells: view.Cells{
				"id":               rb.ID,
				"name":             rb.Name,
				"description":      rb.Description,
				"category":         rb.Category,
				"labels":           strings.Join(rb.Labels, ", "),
				"capabilities":     strings.Join(names, ", "),
				"interruptible":    yesNo(rb.Interruptible),
				"capability_count": strconv.Itoa(len(names)),
			}}
		},
		// No Form and no Bind: with no Writer, Bind never reads either,
		// and supplying them would suggest a write path that does not
		// exist.
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Runbooks",
		NavLabel: "RUNBOOKS",
		// After the two objects a template names, because this is the
		// catalog a template points into rather than a thing an operator
		// launches directly.
		NavOrder: 60,
		NavGroup: view.NavGroupResources,
		Summary:  "Every runbook this control plane can dispatch.",
		Status:   view.StatusImplemented,
		IDField:  "id",
		// The record page is headed by what the runbook is called, not by
		// the identifier it is addressed at. This is the one implemented
		// view whose identifier is not already a name, so it is the one
		// that needed saying: every other view's IDField is its name, and
		// Descriptor.TitleField falls back to IDField for all of them.
		NameField: "name",
		Fields:    fields,
		Ops: view.Ops{
			List: &apispec.ListRunbooks,
			Get:  &apispec.GetRunbook,
			// No Create, Update or Delete. Runbooks come from a directory
			// and from GitOps; a write path here would be a second,
			// unversioned way to change what this platform executes.
		},
		Actions:  []view.RecordAction{templateAction(store, sets)},
		Sections: []view.Section{templatesSection(store)},
		Handlers: view.MustBind[*runbook.Runbook](reader{source}, nil, projector),
	})
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

package templates

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file is a template's detail page below its own fields: the survey it
// asks, who can reach it, what it has run, and the one section that is
// honest about having nothing behind it yet.
//
// Stacked sections with a page index rather than tabs, for the reason
// already settled here: tabs collide with the /{resource}/{id}/{action}
// route space, and the real ARIA tab pattern needs JavaScript this UI's
// content security policy has no 'unsafe-inline' for.

// sectionLimit bounds every section's query. A detail page is a summary,
// not an export.
const sectionLimit = 50

// surveySection lists the questions a launching operator is asked.
//
// It renders the questions rather than the answers, and the distinction is
// the security-relevant one: an answer to a password question is encrypted
// at rest and redacted on the way out, so a page that listed answers would
// be a page arguing about which of them it may show. What a survey asks is
// not secret; what somebody answered is.
func surveySection(store launch.Store) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Survey",
		Summary: "What a launching operator is asked, in the order they are asked it. Answers merge into extra variables.",
		Empty:   "This template asks nothing at launch. Every value it runs with is the one it was saved with, or one a launch may override.",
		Fields: []view.Field{
			{Name: "variable", Label: "VARIABLE", Kind: view.KindText, InList: true, MobilePrimary: true},
			{Name: "label", Label: "QUESTION", Kind: view.KindText, InList: true},
			{Name: "type", Label: "TYPE", Kind: view.KindText, InList: true},
			{Name: "required", Label: "REQUIRED", Kind: view.KindBadge, InList: true, BadgeClass: requiredBadge},
			{Name: "choices", Label: "CHOICES", Kind: view.KindText, InList: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			tmpl, ok := load(ctx, store, parentID)
			if !ok {
				return nil, nil
			}
			if !tmpl.Survey.Enabled {
				// A survey somebody wrote and turned off asks nothing, and
				// saying so is different from having no survey. The empty
				// state cannot carry that, so the row does.
				if len(tmpl.Survey.Questions) == 0 {
					return nil, nil
				}
			}

			rows := make([]view.Row, 0, len(tmpl.Survey.Questions))
			for _, q := range tmpl.Survey.Questions {
				rows = append(rows, view.Row{
					ID: q.Variable,
					Cells: view.Cells{
						"variable": q.Variable,
						"label":    q.Label,
						"type":     string(q.Type),
						"required": yesNo(q.Required),
						"choices":  strings.Join(q.Choices, ", "),
					},
				})
			}
			return rows, nil
		},
	}
}

// requiredBadge marks the questions that will refuse a launch if left
// blank, so the ones that stop work are visible at a glance.
func requiredBadge(value string) string {
	if value == "yes" {
		return "badge-changed"
	}
	return "badge-ok"
}

// accessSection lists the grants that reach this template.
//
// Derived rather than direct, and the summary says so on the page. There is
// no role binding at template scope: the RBAC hierarchy runs
// system, organization, inventory, group, device, so who may reach a
// template is decided by the grants on the organization it belongs to and
// the inventory it runs against. Rendering an empty per-template table
// would read as "nobody has access", which is the opposite of true.
func accessSection(store launch.Store, bindings access.Bindings) view.Section {
	return view.Section{
		Status: view.StatusImplemented,
		Title:  "Access",
		Summary: "The role bindings that reach this template, through the organization it belongs to and the " +
			"inventory it runs against. There is no grant at template scope: access is inherited, and a deny " +
			"at a narrower level beats an allow at a broader one.",
		Empty: "No grant names this template's organization or its inventory. Access to it may still come from a system-wide grant, which the Access view lists in full.",
		Fields: []view.Field{
			{Name: "team", Label: "TEAM", Kind: view.KindText, InList: true, MobilePrimary: true, References: "teams"},
			{Name: "role", Label: "ROLE", Kind: view.KindText, InList: true},
			{Name: "effect", Label: "EFFECT", Kind: view.KindBadge, InList: true, BadgeClass: effectBadge},
			{Name: "scope_type", Label: "AT", Kind: view.KindText, InList: true},
			{Name: "scope", Label: "TARGET", Kind: view.KindText, InList: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			tmpl, ok := load(ctx, store, parentID)
			if !ok {
				return nil, nil
			}

			// Two queries rather than one, because scope_id is polymorphic:
			// its values are per level, so organization 3 and inventory 3
			// are different targets and a single query over both ids would
			// return whichever grants happened to share a number.
			var rows []view.Row
			for _, target := range []struct {
				scopeType auth.ScopeType
				id        int
			}{
				{auth.ScopeOrganization, tmpl.OrganizationID},
				{auth.ScopeInventory, tmpl.InventoryID},
			} {
				if target.id <= 0 {
					continue
				}
				found, err := bindings.ListBindings(ctx, access.BindingQuery{
					Query:      access.Query{Limit: sectionLimit},
					ScopeTypes: []auth.ScopeType{target.scopeType},
					ScopeID:    target.id,
				})
				if err != nil {
					return nil, err
				}
				for _, b := range found {
					rows = append(rows, bindingRow(b))
				}
			}
			return rows, nil
		},
	}
}

// bindingRow projects one grant. It renders the team's and the target's
// names rather than their ids, for the reason the Grants view itself does:
// a table of primary keys makes the reader do the join.
func bindingRow(b access.Binding) view.Row {
	return view.Row{
		ID: strconv.Itoa(b.ID),
		Refs: map[string]string{
			"team": strconv.Itoa(b.TeamID),
		},
		Cells: view.Cells{
			"team":       b.TeamName,
			"role":       string(b.Role),
			"effect":     string(b.Effect),
			"scope_type": string(b.ScopeType),
			"scope":      b.ScopeName,
		},
	}
}

// effectBadge colours a deny differently from an allow, because the two
// mean opposite things and a table of one-word cells is exactly where that
// gets misread.
func effectBadge(value string) string {
	if value == string(auth.EffectDeny) {
		return "badge-failed"
	}
	return "badge-ok"
}

// notificationsSection is declared, not implemented.
//
// There is no notification entity in this build. An empty table here would
// say "no notification policies are configured", which is indistinguishable
// from a working section with nothing in it, and this project has shipped
// that ambiguity twice. The declared panel says which of the two it is, and
// names the phase that owns the gap.
func notificationsSection() view.Section {
	return view.Section{
		Title:   "Notifications",
		Summary: "Who is told when a job from this template starts, succeeds or fails.",
		Fields: []view.Field{
			{Name: "target", Label: "TARGET", Kind: view.KindText, InList: true, MobilePrimary: true},
			{Name: "on", Label: "ON", Kind: view.KindText, InList: true},
		},
		Empty: "Notification policies have no backing entity in this build. The Notification Engine owns them, " +
			"and until it lands nothing is sent when a job from this template finishes.",
	}
}

// completedJobsSection is what this template has actually run.
//
// It is the reason Job carries an indexed template_id: "what did this
// template run" is the question somebody has on this page, and answering it
// by filtering a page of the global job list would scan the fastest-growing
// table here to find a handful of rows.
func jobsSection(jobs dispatch.JobStore) view.Section {
	return view.Section{
		Status: view.StatusImplemented,
		// "Jobs", as AWX names the same tab, and not "Completed jobs":
		// ListForTemplate returns every job this template has produced
		// whatever state it is in, so the old title was wrong about its
		// own contents as well as about the word the audience arrives
		// with. A running job is the one somebody most wants to find here.
		Title:   "Jobs",
		Summary: "What this template has run, newest first, whatever state it reached.",
		Empty:   "This template has not been launched yet.",
		Fields: []view.Field{
			{Name: "job", Label: "JOB", Kind: view.KindText, InList: true, MobilePrimary: true, References: "jobs"},
			{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true, BadgeClass: stateBadge},
			{Name: "actor", Label: "LAUNCHED BY", Kind: view.KindText, InList: true},
			{Name: "dispatched", Label: "DISPATCHED", Kind: view.KindText, InList: true},
			{Name: "failed", Label: "FAILED", Kind: view.KindText, InList: true},
			{Name: "created", Label: "WHEN", Kind: view.KindText, InList: true},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			id, err := strconv.Atoi(parentID)
			if err != nil || id < 1 {
				// A section under no record must not answer with every job
				// in the deployment, which is what an unfiltered query
				// would do.
				return nil, nil
			}

			found, err := jobs.ListForTemplate(ctx, id, sectionLimit)
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, len(found))
			for _, j := range found {
				rows = append(rows, view.Row{
					ID:   j.JobID,
					Refs: map[string]string{"job": j.JobID},
					Cells: view.Cells{
						"job":        j.JobID,
						"state":      j.State,
						"actor":      j.Actor,
						"dispatched": strconv.Itoa(j.DispatchedCount),
						"failed":     strconv.Itoa(j.FailedCount),
						"created":    j.CreatedAt.UTC().Format(time.RFC3339),
					},
				})
			}
			return rows, nil
		},
	}
}

// stateBadge colours a job's lifecycle position. The word is rendered
// inside the badge too, so nothing here is encoded in colour alone.
func stateBadge(state string) string {
	switch state {
	case "completed":
		return "badge-ok"
	case "failed":
		return "badge-failed"
	default:
		return "badge-changed"
	}
}

// load reads the template a section hangs off, treating a missing or
// malformed parent as no rows rather than as an error.
//
// A section under no record legitimately happens: the same descriptor
// renders on the collection page, where there is no parent at all. Failing
// there would break a list page over a table that does not belong on it.
func load(ctx context.Context, store launch.Store, parentID string) (launch.Template, bool) {
	id, err := strconv.Atoi(parentID)
	if err != nil || id < 1 {
		return launch.Template{}, false
	}
	tmpl, err := store.Get(ctx, id)
	if err != nil {
		return launch.Template{}, false
	}
	return tmpl, true
}

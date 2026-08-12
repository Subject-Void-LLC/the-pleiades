package grants

import (
	"context"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file is the per-object Access section: the grants on one record,
// rendered under that record.
//
// Both this and the global Access table exist because they answer different
// questions. The table is the auditor's one page, sorted by nothing in
// particular and covering the whole deployment: "who reaches what". The
// section answers "who reaches *this*", which is the question somebody has
// while looking at the record, and it means granting access does not
// require leaving the page and reconstructing which target you were on.
// AWX puts an Access tab on every object for the same reason.
//
// They read the same access.Bindings port, filtered, so the two cannot
// disagree about what a grant says. A section with its own query, or its
// own rendering of a role and an effect, would be a second answer to a
// question that already has one.
//
// It lives here rather than in each record's own package because a grant's
// presentation belongs to the view that owns grants. Two copies of the
// column set would drift the first time a column changed, and the drift
// would be invisible: both pages would render, and they would say different
// things about the same row.

// sectionFields are the section's columns.
//
// Deliberately not the same list as the table's. The table names the target
// in its GRANTED AT column because it spans every record; here the target is
// the page you are on, so repeating it in every row would be one column of
// the same value. What the section adds instead is nothing: it drops the
// noise and keeps what varies.
var sectionFields = []view.Field{
	{
		Name: "team", Label: "TEAM", Kind: view.KindText,
		InList: true, MobilePrimary: true, References: "teams",
	},
	{Name: "role", Label: "ROLE", Kind: view.KindText, InList: true},
	{Name: "effect", Label: "EFFECT", Kind: view.KindBadge, InList: true, BadgeClass: effectBadge},
	{Name: "scope_type", Label: "SCOPE", Kind: view.KindBadge, InList: true, BadgeClass: scopeBadge},
}

// sectionRow projects one grant for a section.
func sectionRow(b access.Binding) view.Row {
	return view.Row{
		ID:   strconv.Itoa(b.ID),
		Refs: map[string]string{"team": strconv.Itoa(b.TeamID)},
		Cells: view.Cells{
			"team":       b.TeamName,
			"role":       string(b.Role),
			"effect":     string(b.Effect),
			"scope_type": string(b.ScopeType),
		},
	}
}

// SectionForScope is the Access section of a record that grants are made
// *against*: an organization, an inventory, a group or a device.
//
// scopeType names the level, and the section resolves the record's own id
// from the page it is on. Both are needed together: scope_id carries no
// foreign key and its values are per level, so an id without a level would
// put a device's grants on an organization's page.
func SectionForScope(s access.Bindings, scopeType auth.ScopeType, noun string) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Access",
		Summary: "The role bindings that grant access to this " + noun + ". A deny here beats an allow at a broader level.",
		Fields:  sectionFields,
		Empty:   "No grants name this " + noun + " directly. Access to it may still come from a broader grant, which the Access view lists in full.",
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			// A section under no record must not answer with every grant in
			// the deployment, which is what an unfiltered query would do.
			id, err := strconv.Atoi(parentID)
			if err != nil || id < 1 {
				return nil, nil
			}

			bindings, err := s.ListBindings(ctx, access.BindingQuery{
				Query:      access.Query{Limit: 200},
				ScopeTypes: []auth.ScopeType{scopeType},
				ScopeID:    id,
			})
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, len(bindings))
			for _, b := range bindings {
				rows = append(rows, sectionRow(b))
			}
			return rows, nil
		},
	}
}

// SectionForTeam is the Access section of a team: what this team reaches,
// rather than who reaches it.
//
// The other direction, and the reason it is a separate builder rather than
// a flag. A team is the holder of a grant, never its target, so the same
// section rendered on a team's page would always be empty and would read as
// "this team has no access" rather than as "you are looking at the wrong
// end of the relationship".
func SectionForTeam(s access.Bindings) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Access",
		Summary: "What this team reaches. Roles are granted to teams, never to a person, so this is every permission its members hold.",
		Fields:  teamSectionFields,
		Empty:   "This team holds no grants, so its members reach nothing through it.",
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			id, err := strconv.Atoi(parentID)
			if err != nil || id < 1 {
				return nil, nil
			}

			bindings, err := s.ListBindings(ctx, access.BindingQuery{
				Query:   access.Query{Limit: 200},
				TeamIDs: []int{id},
			})
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, len(bindings))
			for _, b := range bindings {
				rows = append(rows, view.Row{
					ID: strconv.Itoa(b.ID),
					Cells: view.Cells{
						"granted_at": grantedAt(b),
						"role":       string(b.Role),
						"effect":     string(b.Effect),
						"scope_type": string(b.ScopeType),
					},
				})
			}
			return rows, nil
		},
	}
}

// teamSectionFields drop the team column, which on a team's own page would
// be the page's own name repeated once per row, and put back the target,
// which is what actually varies here.
var teamSectionFields = []view.Field{
	{
		Name: "granted_at", Label: "GRANTED AT", Kind: view.KindText,
		InList: true, MobilePrimary: true,
	},
	{Name: "role", Label: "ROLE", Kind: view.KindText, InList: true},
	{Name: "effect", Label: "EFFECT", Kind: view.KindBadge, InList: true, BadgeClass: effectBadge},
	{Name: "scope_type", Label: "SCOPE", Kind: view.KindBadge, InList: true, BadgeClass: scopeBadge},
}

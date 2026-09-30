// Package grants is the Access view: the role bindings themselves.
//
// This is the page an auditor arrives at. Everything else in the Access
// group describes who exists; this describes what they can reach, and it is
// the only table in the platform whose rows are permissions.
//
// It carries a rendered provenance column for that reason. "team 4,
// operator, inventory 7, allow" tells a reader nothing unless they already
// know what inventory 7 is, and a permissions table nobody can read is one
// nobody audits. Spacelift calls the equivalent column "Granted via" and it
// is the single most copyable thing in their access model.
//
// The view is named "access" in the navigation and "grants" as a package,
// because internal/access already owns the word for the administration
// surface as a whole and two packages called access would be one too many.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package grants

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "access"

// store is the slice of the access surface this view adapts.
type store interface {
	access.Bindings
	access.Teams
}

// effectBadge colours a grant by what it does. Deny is the stronger
// statement, so it renders as the loud one: an explicit Deny at any level
// beats an Allow at a broader one, and a reader scanning for why somebody
// cannot reach something is looking for exactly these rows.
func effectBadge(effect string) string {
	if effect == string(auth.EffectDeny) {
		return "badge-failed"
	}
	return "badge-ok"
}

// scopeBadge marks how far a grant reaches. System scope is the one worth
// seeing from across the room.
func scopeBadge(scope string) string {
	if scope == string(auth.ScopeSystem) {
		return "badge-changed"
	}
	return "badge-neutral"
}

func fields(s store) []view.Field {
	return []view.Field{
		{
			Name: "team", Label: "TEAM", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true,
			References: "teams",
			Help:       "Roles are granted to teams, never to a person.",
			Options:    teamOptions(s),
		},
		{
			Name: "role", Label: "ROLE", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true,
			Help:    "What the team may do where this grant applies.",
			Options: staticOptions("viewer", "operator", "admin"),
		},
		{
			Name: "scope_type", Label: "SCOPE", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true, BadgeClass: scopeBadge,
			Help:    "How far the grant reaches. System covers every organization.",
			Options: staticOptions("system", "organization", "inventory", "group", "device"),
		},
		{
			Name: "scope_id", Label: "TARGET ID", Kind: view.KindNumber, InForm: true,
			Help: "The record this covers. Leave empty for system scope, which names no target.",
		},
		{
			Name: "effect", Label: "EFFECT", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true, BadgeClass: effectBadge,
			Help:    "An explicit deny at any level beats an allow at a broader one.",
			Options: staticOptions("allow", "deny"),
		},
		{
			// The primary field, and not team: a referencing cell links to
			// what it names, so a team primary linked every row to its team
			// and none to its grant, whose page holds Edit and Delete.
			Name: "granted_at", Label: "GRANTED AT", Kind: view.KindReadOnly, InList: true, MobilePrimary: true,
			Help: "Where this grant sits, in words.",
		},
	}
}

// staticOptions offers a fixed vocabulary. The values are the wire values,
// so what a reader picks is exactly what is stored and later resolved.
func staticOptions(values ...string) func(context.Context) ([]view.Option, error) {
	return func(context.Context) ([]view.Option, error) {
		out := make([]view.Option, 0, len(values))
		for _, v := range values {
			out = append(out, view.Option{Label: v, Value: v})
		}
		return out, nil
	}
}

// teamOptions names each team, so the control offers a name rather than
// asking somebody to remember a primary key.
func teamOptions(s store) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		teams, err := s.ListTeams(ctx, access.TeamQuery{Query: access.Query{Limit: 200}})
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(teams))
		for _, t := range teams {
			out = append(out, view.Option{Label: t.Name, Value: strconv.Itoa(t.ID)})
		}
		return out, nil
	}
}

// grantedAt renders where a grant sits, in words.
//
// This used to render "inventory 7", and argued that resolving the name
// would cost a query per row. That was true and the conclusion was wrong:
// the answer to an expensive per-row join is a batch, not a primary key on
// the page. The store now resolves the page's targets with one query per
// scope type, and an auditor reading this column can act on it.
//
// A deleted target is named as deleted rather than left blank. The scope
// column carries no foreign key by design, so a grant naming a record that
// has since been removed is a real state, and "granted nowhere" and
// "granted at something that is gone" are different findings.
func grantedAt(b access.Binding) string {
	switch {
	case b.SystemWide():
		return "Everywhere (system)"
	case b.ScopeName != "":
		return string(b.ScopeType) + " " + b.ScopeName
	default:
		return string(b.ScopeType) + " " + strconv.Itoa(b.ScopeID) + " (deleted)"
	}
}

type reader struct{ store store }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[access.Binding], error) {
	limit := q.PageSize()
	after, _ := strconv.Atoi(q.Cursor)
	// One more than asked for, for the reason the Organizations reader
	// gives: a next page has to be observed, not inferred from a page that
	// came back non-empty.
	bindings, err := r.store.ListBindings(ctx, access.BindingQuery{
		Query: access.Query{After: after, Limit: limit + 1},
	})
	if err != nil {
		return view.Page[access.Binding]{}, err
	}
	page := view.Page[access.Binding]{Items: bindings}
	if len(bindings) > limit {
		page.Items = bindings[:limit]
		page.NextCursor = strconv.Itoa(bindings[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (access.Binding, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.Binding{}, access.ErrNotFound
	}
	return r.store.GetBinding(ctx, numeric)
}

type writer struct{ store store }

func (w writer) Create(ctx context.Context, b access.Binding) (string, error) {
	created, err := w.store.CreateBinding(ctx, b)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

// Update changes role and effect only.
//
// The scope and the team are carried forward from storage, because
// re-pointing a grant is indistinguishable from revoking one and issuing
// another, and an audit trail showing a grant quietly changing what it
// covers is one nobody can reconstruct.
func (w writer) Update(ctx context.Context, id string, b access.Binding) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	b.ID = numeric
	return w.store.UpdateBinding(ctx, b)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	return w.store.DeleteBinding(ctx, numeric)
}

// Register wires the Access view over the access store.
func Register(s store) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Access",
		NavLabel: "ACCESS",
		NavGroup: view.NavGroupAccess,
		// Last within Access: it is the thing the other three exist to make
		// expressible, so it reads after them.
		NavOrder: 120,
		Summary:  "Who reaches what. An explicit deny at any level beats an allow at a broader one.",
		Status:   view.StatusImplemented,
		IDField:  "granted_at",
		Fields:   fields(s),
		Ops: view.Ops{
			List:   &apispec.ListBindings,
			Get:    &apispec.GetBinding,
			Create: &apispec.CreateBinding,
			Update: &apispec.UpdateBinding,
			Delete: &apispec.DeleteBinding,
		},
		Handlers: view.MustBind[access.Binding](reader{s}, writer{s}, view.Projector[access.Binding]{
			Row: func(b access.Binding) view.Row {
				target := ""
				if !b.SystemWide() {
					target = strconv.Itoa(b.ScopeID)
				}
				return view.Row{
					ID: strconv.Itoa(b.ID),
					// The id the team cell links to; the name it shows is
					// in Cells.
					Refs: map[string]string{"team": strconv.Itoa(b.TeamID)},
					Cells: view.Cells{
						"team":       b.TeamName,
						"role":       string(b.Role),
						"scope_type": string(b.ScopeType),
						"scope_id":   target,
						"effect":     string(b.Effect),
						"granted_at": grantedAt(b),
					},
				}
			},
			Form: func(b access.Binding) map[string]string {
				target := ""
				if !b.SystemWide() {
					target = strconv.Itoa(b.ScopeID)
				}
				return map[string]string{
					"team":       strconv.Itoa(b.TeamID),
					"role":       string(b.Role),
					"scope_type": string(b.ScopeType),
					"scope_id":   target,
					"effect":     string(b.Effect),
				}
			},
			Bind: func(v view.Values) (access.Binding, view.FieldErrors) {
				errs := view.FieldErrors{}

				team, err := strconv.Atoi(strings.TrimSpace(v.Get("team")))
				if err != nil || team < 1 {
					errs.Add("team", "Choose the team this grant is held by.")
				}
				scopeType := auth.ScopeType(strings.TrimSpace(v.Get("scope_type")))

				// The two halves of the FAILURE_PATTERNS #99 invariant,
				// checked here as well as in the store so the message lands
				// on the control that is wrong rather than as a page-level
				// refusal with every field left unmarked.
				var scopeID int
				raw := strings.TrimSpace(v.Get("scope_id"))
				switch {
				case scopeType == auth.ScopeSystem && raw != "":
					errs.Add("scope_id", "A system grant covers everything, so it names no target. Leave this empty.")
				case scopeType != auth.ScopeSystem && raw == "":
					errs.Add("scope_id", "Name the record this grant covers.")
				case raw != "":
					parsed, err := strconv.Atoi(raw)
					if err != nil || parsed < 1 {
						errs.Add("scope_id", "A target id is a positive number.")
					}
					scopeID = parsed
				}

				return access.Binding{
					TeamID:    team,
					Role:      auth.Role(strings.TrimSpace(v.Get("role"))),
					ScopeType: scopeType,
					ScopeID:   scopeID,
					Effect:    auth.Effect(strings.TrimSpace(v.Get("effect"))),
				}, errs
			},
		}),
	})
}

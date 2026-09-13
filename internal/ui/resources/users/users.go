// Package users is the Users view: the identities a token's subject
// resolves against.
//
// A user here is deliberately almost nothing: this view records that a
// subject is known to this deployment rather than issuing a credential. The
// email is the join key, which is the whole reason the record exists:
// internal/auth's team lookup matches a token's subject against it to find
// the teams whose grants apply, and a local sign-in matches the same column.
//
// A local password is deliberately NOT shown or edited here, even though
// Phase 79 added one. It lives on its own entity so that nothing projected
// from a User can carry a hash, and it is administered from the controller's
// own subcommands and from the signed-in caller's account page, neither of
// which needs the access:write scope this view is gated on.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package users

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "users"

var fields = []view.Field{
	{
		Name: "email", Label: "EMAIL", Kind: view.KindText,
		Required: true, MaxLen: 320, Autocomplete: "email",
		InList: true, InForm: true, MobilePrimary: true, Sortable: true,
		Help: "Matched against a token's subject to decide which teams a caller belongs to. Lowercased on save.",
	},
	// No References: a user belongs to several teams and a single cell
	// cannot link to more than one record. The names still render, because
	// the point is that a reader should not meet a primary key.
	{Name: "teams", Label: "TEAMS", Kind: view.KindReadOnly, InList: true},
	{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
}

type reader struct{ store access.Users }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[access.User], error) {
	limit := q.PageSize()
	after, _ := strconv.Atoi(q.Cursor)
	// One more than asked for, for the reason the Organizations reader
	// gives: a next page has to be observed, not inferred from a page that
	// came back non-empty.
	users, err := r.store.ListUsers(ctx, access.Query{
		After: after, Limit: limit + 1, Search: strings.TrimSpace(q.Search),
	})
	if err != nil {
		return view.Page[access.User]{}, err
	}
	page := view.Page[access.User]{Items: users}
	if len(users) > limit {
		page.Items = users[:limit]
		page.NextCursor = strconv.Itoa(users[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (access.User, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.User{}, access.ErrNotFound
	}
	return r.store.GetUser(ctx, numeric)
}

type writer struct{ store access.Users }

func (w writer) Create(ctx context.Context, user access.User) (string, error) {
	created, err := w.store.CreateUser(ctx, user)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

func (w writer) Update(ctx context.Context, id string, user access.User) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	user.ID = numeric
	return w.store.UpdateUser(ctx, user)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	return w.store.DeleteUser(ctx, numeric)
}

// Register wires the Users view over the access store.
func Register(store access.Users) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Users",
		NavLabel: "USERS",
		NavGroup: view.NavGroupAccess,
		// AWX's user tabs. Roles is deliberately absent and its absence is
		// the point: this platform grants roles to teams and never to a
		// person, so a user's reach is the union of their teams' grants.
		// Rendering a per-user roles tab would invite somebody to look for
		// a grant that cannot exist.
		Sections: []view.Section{
			view.Planned("Teams",
				"The teams this person belongs to. Their access is the union of what those teams hold.",
				"Membership is an edge on the team rather than a listing on the user, and no port reads it from this side yet.",
				[]view.Field{
					{Name: "team", Label: "TEAM", Kind: view.KindText, InList: true, MobilePrimary: true, References: "teams"},
					{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true, References: "organizations"},
				}),
			view.Planned("Sessions",
				"Where this person is currently signed in.",
				"Sessions are rows this UI reads only for the caller's own request; enumerating another account's is a capability nothing here has.",
				[]view.Field{
					{Name: "started", Label: "STARTED", Kind: view.KindTimestamp, InList: true, MobilePrimary: true},
					{Name: "expires", Label: "EXPIRES", Kind: view.KindTimestamp, InList: true},
					{Name: "source", Label: "SOURCE", Kind: view.KindText, InList: true},
				}),
		},
		NavOrder: 110,
		Summary:  "Known identities. Membership of a team is what grants anything.",
		Status:   view.StatusImplemented,
		IDField:  "email",
		Fields:   fields,
		Ops: view.Ops{
			List:   &apispec.ListUsers,
			Get:    &apispec.GetUser,
			Create: &apispec.CreateUser,
			Update: &apispec.UpdateUser,
			Delete: &apispec.DeleteUser,
		},
		Handlers: view.MustBind[access.User](reader{store}, writer{store}, view.Projector[access.User]{
			Row: func(user access.User) view.Row {
				return view.Row{
					ID: strconv.Itoa(user.ID),
					Cells: view.Cells{
						"email": user.Email,
						// Names, not ids. The store already loads the team
						// rows to populate TeamIDs, so this costs nothing
						// and stops the page asking a reader to know which
						// team is 4.
						"teams":   strings.Join(user.TeamNames, ", "),
						"created": user.CreatedAt.UTC().Format("2006-01-02 15:04"),
					},
				}
			},
			Form: func(user access.User) map[string]string {
				return map[string]string{"email": user.Email}
			},
			Bind: func(v view.Values) (access.User, view.FieldErrors) {
				errs := view.FieldErrors{}
				email := strings.TrimSpace(v.Get("email"))
				switch {
				case email == "":
					errs.Add("email", "A user needs an email address.")
				case !strings.Contains(email, "@"):
					// Checked here as well as in the store so the message
					// lands on the control rather than as a page-level
					// failure with the field left unmarked.
					errs.Add("email", "That is not an email address.")
				}
				return access.User{Email: email}, errs
			},
		}),
	})
}

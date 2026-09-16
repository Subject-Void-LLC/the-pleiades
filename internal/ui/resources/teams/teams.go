// Package teams is the Teams view: the principals a role is ever granted
// to.
//
// Never a user directly. PLAN.md Section 18.2 gives the rule and the reason
// in one line: granting to a person produces orphaned permissions the moment
// that person leaves, because the grant outlives the reason it was made. A
// team is a durable statement about a function, and membership is the
// revocable part.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package teams

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/contacts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/grants"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "teams"

// store is the slice of the access surface this view adapts.
type store interface {
	access.Teams
	access.Organizations
	access.Users
	access.Bindings
	access.Contacts
}

// fields declare the shape. The organization is a select rather than a
// number field for the reason the Inventories form gives about its own: a
// control asking somebody to type a primary key is a control that will
// receive the wrong primary key.
func fields(s store) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, Autocomplete: "off",
			InList: true, InForm: true, MobilePrimary: true,
			Help: "What this team does, rather than who is currently in it.",
		},
		{
			Name: "organization", Label: "ORGANIZATION", Kind: view.KindSelect,
			Required: true, Immutable: true, InList: true, InForm: true,
			References: "organizations",
			// The help text said "set once" from the day this shipped, and
			// the edit form rendered the control anyway. Update has always
			// read the organization from storage, so what a reader could do
			// was pick a different tenant, submit, be told it saved, and
			// have every grant the team holds stay exactly where it was.
			// Immutable is what makes the sentence true rather than
			// aspirational.
			Help:    "Set once. Moving a team between tenants would silently re-scope every grant it holds.",
			Options: organizationOptions(s),
		},
		{
			Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
			MaxLen: 2000, InForm: true,
			Help: "What this team is responsible for, as opposed to who is currently in it. A reviewer needs this to judge whether the team's permissions are proportionate.",
		},
		{
			Name: "users", Label: "MEMBERS", Kind: view.KindLookup, InList: true, InForm: true,
			Help:    "Chosen from the known identities. Replacing this list replaces the membership, so deselecting somebody removes every permission their membership carried.",
			Options: userOptions(s),
		},
		{
			Name: "attested", Label: "ATTESTED", Kind: view.KindReadOnly, InList: true,
			Help: "Who last confirmed this team's ownership is current, and when. Set by the Attest action, never by this form.",
		},
		{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
	}
}

// userOptions offers every known identity by email.
//
// By email rather than by id, which is the entire point of the change: the
// previous control asked for "member user ids, comma separated", and a person
// filling that in cannot tell whether 7 is the right person. A wrong id there
// grants somebody else every permission the team holds, and nothing about the
// stored result looks wrong afterwards.
func userOptions(s store) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		users, err := s.ListUsers(ctx, access.Query{Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(users))
		for _, u := range users {
			out = append(out, view.Option{Label: u.Email, Value: strconv.Itoa(u.ID)})
		}
		return out, nil
	}
}

// organizationOptions offers every organization as a choice.
func organizationOptions(s store) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		orgs, err := s.ListOrganizations(ctx, access.Query{Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(orgs))
		for _, org := range orgs {
			out = append(out, view.Option{Label: org.Name, Value: strconv.Itoa(org.ID)})
		}
		return out, nil
	}
}

// attestAction offers the confirmation as a button on the record, for the
// reason the Organizations view's own gives about where the subject comes
// from. It matters more here: a team is what a role is granted to, so a team
// with no attested owner is a live set of permissions with nobody
// accountable for it.
func attestAction(s store) view.RecordAction {
	return view.RecordAction{
		Name:     "attest",
		Label:    "Attest",
		Endpoint: &apispec.AttestTeam,
		Submit: func(ctx context.Context, id string, _ view.Values) (string, view.FieldErrors, error) {
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", nil, access.ErrNotFound
			}
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil {
				return "", nil, fmt.Errorf("attesting team %d: no identity on the request", numeric)
			}
			if err := s.AttestTeam(ctx, numeric, identity.Subject); err != nil {
				return "", nil, err
			}
			// An empty redirect, which sends the caller back to this record
			// through the handler's own resourcePath. These four used to
			// build the path by hand and every one of them left off the
			// UI's mount prefix, so a successful write redirected to
			// /templates/1 rather than /ui/templates/1 and answered the
			// operator with a 404 after the save had already happened.
			// The handler knows the prefix; a call site does not.
			return "", nil, nil
		},
	}
}

type reader struct{ store store }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[access.Team], error) {
	limit := q.PageSize()
	after, _ := strconv.Atoi(q.Cursor)
	// One more than asked for, for the reason the Organizations reader
	// gives: a next page has to be observed, not inferred from a page that
	// came back non-empty.
	teams, err := r.store.ListTeams(ctx, access.TeamQuery{
		Query: access.Query{After: after, Limit: limit + 1, Search: strings.TrimSpace(q.Search)},
	})
	if err != nil {
		return view.Page[access.Team]{}, err
	}
	page := view.Page[access.Team]{Items: teams}
	if len(teams) > limit {
		page.Items = teams[:limit]
		page.NextCursor = strconv.Itoa(teams[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (access.Team, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.Team{}, access.ErrNotFound
	}
	return r.store.GetTeam(ctx, numeric)
}

type writer struct{ store store }

func (w writer) Create(ctx context.Context, team access.Team) (string, error) {
	created, err := w.store.CreateTeam(ctx, team)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

func (w writer) Update(ctx context.Context, id string, team access.Team) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	// The organization comes from storage rather than the submission, for
	// the reason the field's own help text gives.
	current, err := w.store.GetTeam(ctx, numeric)
	if err != nil {
		return err
	}
	current.Name, current.UserIDs = team.Name, team.UserIDs
	current.Description = team.Description
	return w.store.UpdateTeam(ctx, current)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	return w.store.DeleteTeam(ctx, numeric)
}

// Register wires the Teams view over the access store.
func Register(s store) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Teams",
		NavLabel: "TEAMS",
		NavGroup: view.NavGroupAccess,
		NavOrder: 100,
		Summary:  "The principals roles are granted to. Never a person directly.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   fields(s),
		// What this team reaches, on the team. The other direction from an
		// organization's own section: a team holds grants, it is never the
		// target of one.
		// What this team reaches, and who answers for it. A team is where
		// the two questions meet: it is the holder of every grant its
		// members have, so a team with permissions and no named owner is
		// the single finding an access review most wants surfaced, and
		// putting both on one page is what makes it visible.
		Sections: []view.Section{
			grants.SectionForTeam(s),
			contacts.SectionForTeam(s),
			view.Planned("Members",
				"Who is in this team, and therefore who holds everything its Access tab lists.",
				"Membership is an edge on the team and no port lists it, which is why the Access tab shows what the team reaches rather than who reaches through it.",
				[]view.Field{
					{Name: "user", Label: "USER", Kind: view.KindText, InList: true, MobilePrimary: true, References: "users"},
					{Name: "email", Label: "EMAIL", Kind: view.KindText, InList: true},
					{Name: "added", Label: "ADDED", Kind: view.KindTimestamp, InList: true},
				}),
		},
		Ops: view.Ops{
			List:   &apispec.ListTeams,
			Get:    &apispec.GetTeam,
			Create: &apispec.CreateTeam,
			Update: &apispec.UpdateTeam,
			Delete: &apispec.DeleteTeam,
		},
		Actions: []view.RecordAction{attestAction(s)},
		Handlers: view.MustBind[access.Team](reader{s}, writer{s}, view.Projector[access.Team]{
			Row: func(team access.Team) view.Row {
				return view.Row{
					ID: strconv.Itoa(team.ID),
					Cells: view.Cells{
						"name": team.Name,
						// The name, never the id: a column of primary
						// keys asks the reader to remember that
						// organization 1 is Network.
						"organization": team.OrganizationName,
						"description":  team.Description,
						// A count in the list and the ids in the form: the
						// two projectors answer different questions, and a
						// column of primary keys is unreadable where the
						// count is the thing being scanned for.
						"users":    strconv.Itoa(len(team.UserIDs)),
						"attested": team.Attested.Describe(),
						"created":  team.CreatedAt.UTC().Format("2006-01-02 15:04"),
					},
				}
			},
			Form: func(team access.Team) map[string]string {
				return map[string]string{
					"name":         team.Name,
					"organization": strconv.Itoa(team.OrganizationID),
					"description":  team.Description,
					// Comma separated, which is what FormModel.IsSelected
					// splits to decide which options render selected.
					"users": joinIDs(team.UserIDs),
				}
			},
			Bind: func(v view.Values) (access.Team, view.FieldErrors) {
				errs := view.FieldErrors{}
				name := strings.TrimSpace(v.Get("name"))
				if name == "" {
					errs.Add("name", "A team needs a name.")
				}
				// Immutable, so an edit never carries one and Update reads
				// it from storage. Parsing it unconditionally refused every
				// team edit with an error naming a control the form did not
				// render.
				var org int
				if !v.Editing() {
					parsed, err := strconv.Atoi(strings.TrimSpace(v.Get("organization")))
					if err != nil || parsed < 1 {
						errs.Add("organization", "Choose the organization this team belongs to.")
					}
					org = parsed
				}
				// Selected, not Tags: a multi-select submits one value per
				// chosen option, and reading it through the singular
				// accessor would persist the first member and silently drop
				// the rest.
				users, bad := parseIDs(v.Selected("users"))
				if bad != "" {
					errs.Add("users", "That is not a known identity: "+bad+".")
				}
				return access.Team{
					Name:           name,
					OrganizationID: org,
					Description:    strings.TrimSpace(v.Get("description")),
					UserIDs:        users,
				}, errs
			},
		}),
	})
}

// joinIDs renders an id list for a tags control.
func joinIDs(ids []int) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.Itoa(id))
	}
	return strings.Join(out, ", ")
}

// parseIDs reads a tags control back into ids, naming the first value that
// is not one rather than silently dropping it. A membership list that
// quietly discarded a typo would remove somebody from a team without saying
// so.
func parseIDs(values []string) ([]int, string) {
	out := make([]int, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		id, err := strconv.Atoi(trimmed)
		if err != nil || id < 1 {
			return nil, strconv.Quote(trimmed)
		}
		out = append(out, id)
	}
	return out, ""
}

// Package contacts is the Contacts view: who is accountable for a tenant or
// a team, and how to reach them when that tenant's automation is doing
// something nobody expected at three in the morning.
//
// It is the readable half of an entity that already existed. The store, the
// exactly-one-owner invariant and the five API endpoints all shipped before
// this package, which meant the accountability record could be written over
// HTTP and read nowhere: the deployment-wide question an access review
// actually asks, "which tenants have nobody named against them", had no page
// that answered it.
//
// The deployment-wide list is this view; the per-owner list is the section
// in section.go, rendered on the record it belongs to. Both read the same
// port, so the two cannot disagree about who is accountable for what.
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package contacts

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "contacts"

// store is the slice of the access surface this view adapts: the contacts
// it administers, plus the two owner types its form offers as choices.
type store interface {
	access.Contacts
	access.Organizations
	access.Teams
}

// fields declare the one shape driving the table, the form and the
// validation.
//
// Every control declares autocomplete "off", which is a deliberate reading
// of WCAG SC 1.3.5 rather than an omission of it. The success criterion is
// about identifying the purpose of an input collecting information about
// *the user*, so that a browser can fill in their own name and their own
// telephone number. This form collects somebody else's: an escalation rota's
// number, a security officer's address. Tagging those "tel" and "email"
// would invite a browser to offer the operator's own details as the answer,
// and an escalation path filled in with the details of whoever happened to
// create the record is the exact failure the entity exists to prevent.
func fields(s store) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, Autocomplete: "off",
			InList: true, InForm: true, MobilePrimary: true, Sortable: true,
			Help: "A rota is a better answer than a person. An individual's name in an escalation field is a page that goes unanswered the week they are on leave.",
		},
		{
			Name: "role", Label: "ROLE", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true,
			Options: roleOptions,
			Help:    "What this contact is for. The owner is who an access review asks about; the escalation contact is who gets woken, and they are frequently not the same person.",
		},
		{
			Name: "owner", Label: "ACCOUNTABLE FOR", Kind: view.KindSelect,
			Required: true, Immutable: true, InForm: true,
			Options: ownerOptions(s),
			Help:    "The organization or team this contact answers for. Set once: moving a contact between owners is indistinguishable from deleting one and creating another, and an accountability record that quietly changed what it was accountable for would defeat the attestation beside it.",
		},
		{
			Name: "accountable_for", Label: "ACCOUNTABLE FOR", Kind: view.KindReadOnly, InList: true,
			Help: "Which record this contact answers for.",
		},
		{
			Name: "email", Label: "EMAIL", Kind: view.KindText,
			MaxLen: 320, Autocomplete: "off", InForm: true,
			Help: "Optional on its own, but a contact needs at least one of an email, a phone number or a URL.",
		},
		{
			Name: "phone", Label: "PHONE", Kind: view.KindText,
			MaxLen: 100, Autocomplete: "off", InForm: true,
			Help: "Include the country code. A number that only dials from inside one office is not an escalation path.",
		},
		{
			Name: "url", Label: "URL", Kind: view.KindText,
			MaxLen: 2000, Autocomplete: "off", InForm: true,
			Help: "A rota or paging system, where one exists. Frequently the only durable answer, since the person on call changes and the URL does not.",
		},
		{
			Name: "reach", Label: "REACH", Kind: view.KindReadOnly, InList: true,
			Help: "Every channel recorded for this contact.",
		},
		{
			Name: "notes", Label: "NOTES", Kind: view.KindLongText,
			MaxLen: 2000, InForm: true,
			Help: "When this is the right contact, and when it is not. Escalation information that is only a phone number is escalation information somebody will misuse.",
		},
		{
			Name: "order", Label: "ORDER", Kind: view.KindNumber,
			InList: true, InForm: true, Sortable: true,
			Help: "Who is tried first within one owner, lowest first. This is the whole point of an escalation path and cannot be inferred from a role or a creation date.",
		},
		{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
	}
}

// roleOptions offers the four roles in the order they matter during an
// incident, which is the order the domain declares them in rather than
// alphabetical.
func roleOptions(context.Context) ([]view.Option, error) {
	roles := access.ContactRoles()
	out := make([]view.Option, 0, len(roles))
	for _, role := range roles {
		out = append(out, view.Option{Label: string(role), Value: string(role)})
	}
	return out, nil
}

// The owner select's value encodes which kind of record was chosen as well
// as which one, because the id alone is ambiguous: organization 3 and team 3
// both exist and are different owners.
const (
	ownerOrganization = "organization"
	ownerTeam         = "team"
)

// ownerValue builds one owner option's submitted value.
func ownerValue(kind string, id int) string { return kind + ":" + strconv.Itoa(id) }

// ownerOptions offers every organization and every team as one list.
//
// One control rather than two, and that is the safety argument rather than a
// layout preference. A contact answers for exactly one record, never both
// and never neither, and two selects make both of the invalid states
// something a person can submit and a Bind function has to catch. Here the
// invalid states are unreachable: a select carries one value, Required makes
// it present, and view.Validate refuses any value that is not one of the
// options this function offered. The invariant is enforced by the shape of
// the control instead of by remembering to check it.
//
// Each option says which kind of record it is, because an organization and a
// team can share a name as readily as they can share an id.
func ownerOptions(s store) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		orgs, err := s.ListOrganizations(ctx, access.Query{Limit: 200})
		if err != nil {
			return nil, err
		}
		teams, err := s.ListTeams(ctx, access.TeamQuery{Query: access.Query{Limit: 200}})
		if err != nil {
			return nil, err
		}

		out := make([]view.Option, 0, len(orgs)+len(teams))
		for _, org := range orgs {
			out = append(out, view.Option{
				Label: org.Name + " (organization)",
				Value: ownerValue(ownerOrganization, org.ID),
			})
		}
		for _, team := range teams {
			out = append(out, view.Option{
				Label: team.Name + " (team)",
				Value: ownerValue(ownerTeam, team.ID),
			})
		}
		return out, nil
	}
}

// parseOwner reads a submitted owner back into the pair of ids the domain
// holds, returning zeroes for anything it does not recognise.
//
// It refuses rather than trusts, even though view.Validate has already
// checked the value against the offered options and the control is absent
// from the edit form entirely. Two zeroes reach the store as a contact
// naming no owner, which validateContact refuses by name, so the failure
// mode of a malformed value is a refusal rather than a contact silently
// attached to organization 0.
func parseOwner(raw string) (organizationID, teamID int) {
	kind, rest, found := strings.Cut(strings.TrimSpace(raw), ":")
	if !found {
		return 0, 0
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id < 1 {
		return 0, 0
	}
	switch kind {
	case ownerOrganization:
		return id, 0
	case ownerTeam:
		return 0, id
	default:
		return 0, 0
	}
}

// reach renders every channel a contact records, for a column that answers
// "can I actually get hold of them" without opening the record.
//
// The channels are labelled rather than concatenated. A bare list of a
// string, a number and a URL is three values a reader has to classify by
// looking at them, and an address and a rota URL are not always
// distinguishable at a glance.
func reach(c access.Contact) string {
	var parts []string
	for _, channel := range []struct{ label, value string }{
		{"email", c.Email},
		{"phone", c.Phone},
		{"url", c.URL},
	} {
		if strings.TrimSpace(channel.value) != "" {
			parts = append(parts, channel.label+" "+channel.value)
		}
	}
	if len(parts) == 0 {
		// Unreachable through this UI, since the store refuses a contact
		// with no channel. Said out loud rather than left blank because a
		// blank cell here reads as a rendering gap, and if one ever does
		// arrive (an import, a hand-written row) it is a finding rather
		// than a cosmetic problem.
		return "No way to reach them"
	}
	return strings.Join(parts, ", ")
}

type reader struct{ store access.Contacts }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[access.Contact], error) {
	limit := q.PageSize()
	after, _ := strconv.Atoi(q.Cursor)
	// One more than asked for, for the reason the Organizations reader
	// gives: a next page has to be observed, not inferred from a page that
	// came back non-empty.
	//
	// No owner is named, which is the one caller the store's doc comment
	// permits to do that: this is the deployment-wide management list, and
	// the question it exists to answer spans every tenant.
	contacts, err := r.store.ListContacts(ctx, access.ContactQuery{
		Query: access.Query{After: after, Limit: limit + 1, Search: strings.TrimSpace(q.Search)},
	})
	if err != nil {
		return view.Page[access.Contact]{}, err
	}

	page := view.Page[access.Contact]{Items: contacts}
	if len(contacts) > limit {
		page.Items = contacts[:limit]
		page.NextCursor = strconv.Itoa(contacts[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (access.Contact, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.Contact{}, access.ErrNotFound
	}
	return r.store.GetContact(ctx, numeric)
}

type writer struct{ store access.Contacts }

func (w writer) Create(ctx context.Context, contact access.Contact) (string, error) {
	created, err := w.store.CreateContact(ctx, contact)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

// Update carries the owner forward from storage rather than from the
// submission, which is what makes the field's immutability true rather than
// merely rendered. The edit form does not offer the control and the store
// would ignore a submitted owner anyway; this is the third of the three
// agreeing, so a caller that reached this method directly cannot move a
// contact between tenants either.
func (w writer) Update(ctx context.Context, id string, contact access.Contact) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	current, err := w.store.GetContact(ctx, numeric)
	if err != nil {
		return err
	}
	contact.ID = numeric
	contact.OrganizationID, contact.TeamID = current.OrganizationID, current.TeamID
	return w.store.UpdateContact(ctx, contact)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	return w.store.DeleteContact(ctx, numeric)
}

// Register wires the Contacts view over the access store.
func Register(s store) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Contacts",
		NavLabel: "CONTACTS",
		NavGroup: view.NavGroupAccess,
		// After Users and before the deployment-wide Access table: a
		// contact hangs off an organization or a team, so it reads after
		// both, and the grants table stays where the group ends because it
		// is the page an auditor finishes on.
		NavOrder: 115,
		Summary:  "Who is accountable for a tenant or a team, and how to reach them.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   fields(s),
		Ops: view.Ops{
			List:   &apispec.ListContacts,
			Get:    &apispec.GetContact,
			Create: &apispec.CreateContact,
			Update: &apispec.UpdateContact,
			Delete: &apispec.DeleteContact,
		},
		Handlers: view.MustBind[access.Contact](reader{s}, writer{s}, view.Projector[access.Contact]{
			Row: func(c access.Contact) view.Row {
				return view.Row{
					ID: strconv.Itoa(c.ID),
					Cells: view.Cells{
						"name": c.Name,
						"role": string(c.Role),
						// The owner named rather than numbered, resolved
						// by the store off a row it had already loaded.
						// No References: the target is polymorphic, and a
						// field can name one view, so this renders the
						// words without the link rather than linking to
						// the wrong resource half the time.
						"accountable_for": c.Owner(),
						// The write control's own cell. Absent from the
						// list and the edit form, present here so the
						// create form redisplays the chosen owner after a
						// validation failure instead of resetting it.
						"owner":   ownerCell(c),
						"email":   c.Email,
						"phone":   c.Phone,
						"url":     c.URL,
						"reach":   reach(c),
						"notes":   c.Notes,
						"order":   strconv.Itoa(c.Order),
						"created": c.CreatedAt.UTC().Format("2006-01-02 15:04"),
					},
				}
			},
			// Every field the edit form offers. The owner is deliberately
			// absent: it is Immutable, so the edit form never renders it,
			// and supplying it here would be a value prepared for a control
			// that does not exist.
			Form: func(c access.Contact) map[string]string {
				return map[string]string{
					"name":  c.Name,
					"role":  string(c.Role),
					"email": c.Email,
					"phone": c.Phone,
					"url":   c.URL,
					"notes": c.Notes,
					"order": strconv.Itoa(c.Order),
				}
			},
			Bind: func(v view.Values) (access.Contact, view.FieldErrors) {
				errs := view.FieldErrors{}

				name := strings.TrimSpace(v.Get("name"))
				if name == "" {
					errs.Add("name", "A contact needs a name.")
				}

				// At least one channel, checked here as well as in the
				// store so the message lands on a control rather than as a
				// page-level refusal with every field left unmarked. No
				// single field is required, so the message goes on the
				// first of the three: an error summary that named all
				// three would say each one is missing, when the truth is
				// that any one of them would do.
				email := strings.TrimSpace(v.Get("email"))
				phone := strings.TrimSpace(v.Get("phone"))
				url := strings.TrimSpace(v.Get("url"))
				if email == "" && phone == "" && url == "" {
					errs.Add("email", "Give at least one of an email, a phone number or a URL. A contact nobody can reach records that somebody is responsible without recording how to tell them.")
				}

				// The owner is absent on an edit, because the control is,
				// and Update replaces it from storage. On a create,
				// view.Validate has already refused an empty or unoffered
				// value, so what arrives here is one of the options this
				// view built.
				organizationID, teamID := parseOwner(v.Get("owner"))

				return access.Contact{
					Name:           name,
					Role:           access.ContactRole(strings.TrimSpace(v.Get("role"))),
					Email:          email,
					Phone:          phone,
					URL:            url,
					Notes:          strings.TrimSpace(v.Get("notes")),
					Order:          v.Int("order"),
					OrganizationID: organizationID,
					TeamID:         teamID,
				}, errs
			},
		}),
	})
}

// ownerCell renders a stored contact's owner back into the value its select
// submits, so a redisplayed create form comes back with the same choice.
func ownerCell(c access.Contact) string {
	if c.OwnedByOrganization() {
		return ownerValue(ownerOrganization, c.OrganizationID)
	}
	return ownerValue(ownerTeam, c.TeamID)
}

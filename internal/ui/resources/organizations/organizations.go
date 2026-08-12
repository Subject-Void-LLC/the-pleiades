// Package organizations is the Organizations view: the tenancy boundary
// everything ownable belongs to.
//
// It is the first of the four access views and the one the others depend on.
// Until it existed nothing in this repository could create an Organization,
// which is why the Inventories form's required organization select rendered
// with no options and refused every submission (FAILURE_PATTERNS.md #101).
//
// It is reachable only because internal/ui/resources/registrars.go names it
// (FAILURE_PATTERNS.md #52).
package organizations

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources/grants"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "organizations"

// fields declare the one shape driving the table, the form and the
// validation.
var fields = []view.Field{
	{
		Name: "name", Label: "NAME", Kind: view.KindText,
		Required: true, MaxLen: 253, Autocomplete: "off",
		InList: true, InForm: true, MobilePrimary: true, Sortable: true,
		Help: "Unique across the deployment. Everything ownable belongs to exactly one organization.",
	},
	{
		Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
		MaxLen: 2000, InForm: true,
		Help: "What this tenant is, in a sentence. A list of names is unreadable at the point where four of them are a variation of \"platform\".",
	},
	{
		Name: "classification", Label: "CLASSIFICATION", Kind: view.KindSelect,
		InList: true, InForm: true, BadgeClass: classificationBadge,
		Help:    "This tenant's own marking, separate from the deployment banner. Leave unmarked if no marking applies: unmarked and unclassified are different statements.",
		Options: classificationOptions,
	},
	{
		Name: "change_window", Label: "CHANGE WINDOW", Kind: view.KindText,
		MaxLen: 200, Autocomplete: "off", InForm: true,
		Help: "When this tenant permits automation to run, for example \"Sat 02:00-06:00 UTC\". Recorded for a human to read: nothing enforces it yet.",
	},
	{
		Name: "frozen", Label: "FROZEN", Kind: view.KindBool,
		InList: true, InForm: true, BadgeClass: frozenBadge,
		Help: "An operator-declared stop. Distinct from having no change window: this one is a decision somebody made and can be asked about.",
	},
	{
		Name: "freeze_reason", Label: "FREEZE REASON", Kind: view.KindText,
		MaxLen: 500, Autocomplete: "off", InForm: true,
		Help: "Why the freeze is in place, and ideally what lifts it.",
	},
	{
		Name: "cost_centre", Label: "COST CENTRE", Kind: view.KindText,
		MaxLen: 100, Autocomplete: "off", InForm: true,
		Help: "Opaque reference into your own system of record. Never validated here: checking somebody else's key format is a promise about their system this one cannot keep.",
	},
	{
		Name: "ticket_key", Label: "TICKET KEY", Kind: view.KindText,
		MaxLen: 100, Autocomplete: "off", InForm: true,
		Help: "Opaque reference into your own ticketing system.",
	},
	{
		Name: "cmdb_id", Label: "CMDB ID", Kind: view.KindText,
		MaxLen: 100, Autocomplete: "off", InForm: true,
		Help: "Opaque reference into your own CMDB.",
	},
	{
		Name: "attested", Label: "ATTESTED", Kind: view.KindReadOnly, InList: true,
		Help: "Who last confirmed this tenant's contacts and escalation path are current, and when. Set by the Attest action, never by this form.",
	},
	{Name: "teams", Label: "TEAMS", Kind: view.KindReadOnly, InList: true},
	{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
}

// classificationOptions offers the six markings and the unmarked case.
//
// The empty option is first and is named rather than blank, because a
// control whose first entry is empty reads as "not filled in yet" when the
// intended meaning is a deliberate absence of marking.
func classificationOptions(context.Context) ([]view.Option, error) {
	out := []view.Option{{Label: "Unmarked", Value: ""}}
	for _, c := range access.Classifications() {
		out = append(out, view.Option{Label: string(c), Value: string(c)})
	}
	return out, nil
}

// classificationBadge paints a marking with the banner palette that already
// exists for it, so a tenant's marking and the deployment banner cannot
// render the same word in two different colours.
//
// Built by lookup rather than by concatenating "banner-" onto the value.
// The concatenating version returned a class for every input it was given,
// including inputs nobody declared, which is a caller-controlled string
// reaching a class attribute: the exact thing view.ValidBadgeClasses is a
// closed set to prevent. Anything unrecognised renders neutral.
func classificationBadge(marking string) string {
	if access.ValidClassification(access.Classification(marking)) && marking != "" {
		return "banner-" + marking
	}
	return "badge-neutral"
}

// frozenBadge marks a stopped tenant loudly. A freeze is the state somebody
// scanning this list is looking for.
func frozenBadge(frozen string) string {
	if frozen == "true" {
		return "badge-failed"
	}
	return "badge-neutral"
}

// attestAction offers the confirmation as a button on the record.
//
// A button rather than a pair of form fields, and that is the design rather
// than a shortcut. The subject comes from the request's identity, exactly as
// a dispatch's actor does: a control somebody could type another person's
// name into would produce a record that says a colleague confirmed something
// they have never seen. There is no prompt because there is nothing to ask.
func attestAction(store access.Organizations) view.RecordAction {
	return view.RecordAction{
		Name:     "attest",
		Label:    "Attest",
		Endpoint: &apispec.AttestOrganization,
		Submit: func(ctx context.Context, id string, _ view.Values) (string, view.FieldErrors, error) {
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", nil, access.ErrNotFound
			}
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil {
				// Refused rather than recorded as anonymous. An unsigned
				// attestation is the one thing this must never store,
				// because it reads as confirmed.
				return "", nil, fmt.Errorf("attesting organization %d: no identity on the request", numeric)
			}
			if err := store.AttestOrganization(ctx, numeric, identity.Subject); err != nil {
				return "", nil, err
			}
			return "/organizations/" + id, nil, nil
		},
	}
}

// reader adapts access.Organizations onto the view layer's Reader.
type reader struct{ store access.Organizations }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[access.Organization], error) {
	limit := q.PageSize()
	after, _ := strconv.Atoi(q.Cursor)
	// One more than asked for, so a next page is observed rather than
	// inferred from a page that came back non-empty. Inferring it offers a
	// next page from every list that has any rows at all, including the
	// last one, which is what put a Next page control under a table of two.
	orgs, err := r.store.ListOrganizations(ctx, access.Query{
		After: after, Limit: limit + 1, Search: strings.TrimSpace(q.Search),
	})
	if err != nil {
		return view.Page[access.Organization]{}, err
	}

	page := view.Page[access.Organization]{Items: orgs}
	if len(orgs) > limit {
		page.Items = orgs[:limit]
		page.NextCursor = strconv.Itoa(orgs[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (access.Organization, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.Organization{}, access.ErrNotFound
	}
	return r.store.GetOrganization(ctx, numeric)
}

// writer adapts the same port onto the view layer's Writer.
type writer struct{ store access.Organizations }

func (w writer) Create(ctx context.Context, org access.Organization) (string, error) {
	created, err := w.store.CreateOrganization(ctx, org)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(created.ID), nil
}

func (w writer) Update(ctx context.Context, id string, org access.Organization) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	org.ID = numeric
	return w.store.UpdateOrganization(ctx, org)
}

func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return access.ErrNotFound
	}
	return w.store.DeleteOrganization(ctx, numeric)
}

// store is the slice of the access surface this view adapts: the
// organizations it administers, plus the grants its detail page renders as
// a section.
//
// Widened from access.Organizations rather than taking the whole Store, for
// the reason the composed port's own doc comment gives: a view that needed
// one method should not look like it administers all of access.
type store interface {
	access.Organizations
	access.Bindings
}

// Register wires the Organizations view over the access store.
func Register(store store) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Organizations",
		NavLabel: "ORGANIZATIONS",
		NavGroup: view.NavGroupAccess,
		// First within Access: a team belongs to an organization and a
		// grant is held by a team, so the group reads in the order the
		// objects have to be created in.
		NavOrder: 90,
		Summary:  "The tenancy boundary. Everything ownable belongs to exactly one.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   fields,
		Ops: view.Ops{
			List:   &apispec.ListOrganizations,
			Get:    &apispec.GetOrganization,
			Create: &apispec.CreateOrganization,
			Update: &apispec.UpdateOrganization,
			Delete: &apispec.DeleteOrganization,
		},
		Actions: []view.RecordAction{attestAction(store)},
		// The Access section, alongside the deployment-wide table rather
		// than instead of it. The table is the auditor's one page; this is
		// the answer to "who reaches this organization", asked while
		// looking at the organization.
		Sections: []view.Section{grants.SectionForScope(store, auth.ScopeOrganization, "organization")},
		Handlers: view.MustBind[access.Organization](reader{store}, writer{store}, view.Projector[access.Organization]{
			Row: func(org access.Organization) view.Row {
				return view.Row{
					// The numeric id, because that is what every URL and
					// every write path uses. IDField names the column a
					// reader recognises; this is the one a route resolves.
					ID: strconv.Itoa(org.ID),
					Cells: view.Cells{
						"name":           org.Name,
						"description":    org.Description,
						"classification": string(org.Classification),
						"change_window":  org.ChangeWindow,
						"frozen":         strconv.FormatBool(org.Frozen),
						"freeze_reason":  org.FreezeReason,
						"cost_centre":    org.CostCentre,
						"ticket_key":     org.TicketKey,
						"cmdb_id":        org.CMDBID,
						"attested":       org.Attested.Describe(),
						"teams":          "",
						"created":        org.CreatedAt.UTC().Format("2006-01-02 15:04"),
					},
				}
			},
			// Every writable field, so an edit form arrives populated. A
			// field present in the form but absent here renders empty and
			// then saves that emptiness over the stored value, which is the
			// quiet way an edit becomes a deletion.
			Form: func(org access.Organization) map[string]string {
				return map[string]string{
					"name":           org.Name,
					"description":    org.Description,
					"classification": string(org.Classification),
					"change_window":  org.ChangeWindow,
					"frozen":         strconv.FormatBool(org.Frozen),
					"freeze_reason":  org.FreezeReason,
					"cost_centre":    org.CostCentre,
					"ticket_key":     org.TicketKey,
					"cmdb_id":        org.CMDBID,
				}
			},
			Bind: func(v view.Values) (access.Organization, view.FieldErrors) {
				errs := view.FieldErrors{}
				name := strings.TrimSpace(v.Get("name"))
				if name == "" {
					errs.Add("name", "An organization needs a name.")
				}

				// The store validates this too. Checked here as well so the
				// message lands on the control that is wrong rather than as
				// a page-level refusal with every field left unmarked.
				marking := access.Classification(strings.TrimSpace(v.Get("classification")))
				if !access.ValidClassification(marking) {
					errs.Add("classification", "That is not a classification marking.")
				}

				frozen := v.Bool("frozen")
				reason := strings.TrimSpace(v.Get("freeze_reason"))
				if frozen && reason == "" {
					// A freeze with no reason is a stop nobody can lift,
					// because the next person has no way to know what it was
					// waiting for.
					errs.Add("freeze_reason", "Say why the freeze is in place, so somebody else can decide when it lifts.")
				}

				return access.Organization{
					Name:           name,
					Description:    strings.TrimSpace(v.Get("description")),
					Classification: marking,
					ChangeWindow:   strings.TrimSpace(v.Get("change_window")),
					Frozen:         frozen,
					FreezeReason:   reason,
					CostCentre:     strings.TrimSpace(v.Get("cost_centre")),
					TicketKey:      strings.TrimSpace(v.Get("ticket_key")),
					CMDBID:         strings.TrimSpace(v.Get("cmdb_id")),
				}, errs
			},
		}),
	})
}

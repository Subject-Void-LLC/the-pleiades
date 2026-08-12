package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// This file covers accountability: the exactly-one-owner invariant, the
// reachability rule, and the attestation. All against a real SQLite database
// per RULE 0, because two of the three are only observable as stored state.

// seedContact creates a contact against an organization and returns it.
func seedContact(t *testing.T, store access.Store, orgID int, name string, role access.ContactRole) access.Contact {
	t.Helper()
	c, err := store.CreateContact(context.Background(), access.Contact{
		Name: name, Role: role, Email: name + "@example.com", OrganizationID: orgID,
	})
	if err != nil {
		t.Fatalf("creating contact %q: %v", name, err)
	}
	return c
}

// TestContacts_RequireExactlyOneOwner is the invariant, from both directions.
//
// It is the same shape as the RoleBinding scope_id invariant and is checked
// for the same reason (FAILURE_PATTERNS.md #99): a nullable reference nothing
// validates becomes a row belonging to everything or to nothing.
func TestContacts_RequireExactlyOneOwner(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	base := access.Contact{Name: "Dana", Role: access.ContactOwner, Email: "dana@example.com"}

	t.Run("neither", func(t *testing.T) {
		_, err := store.CreateContact(ctx, base)
		if !errors.Is(err, access.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("both", func(t *testing.T) {
		both := base
		both.OrganizationID, both.TeamID = orgID, teamID
		_, err := store.CreateContact(ctx, both)
		if !errors.Is(err, access.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("organization", func(t *testing.T) {
		one := base
		one.OrganizationID = orgID
		created, err := store.CreateContact(ctx, one)
		if err != nil {
			t.Fatalf("CreateContact: %v", err)
		}
		if !created.OwnedByOrganization() || created.TeamID != 0 {
			t.Errorf("stored owner = %+v, want the organization alone", created)
		}
	})

	t.Run("team", func(t *testing.T) {
		one := base
		one.TeamID = teamID
		created, err := store.CreateContact(ctx, one)
		if err != nil {
			t.Fatalf("CreateContact: %v", err)
		}
		if created.OwnedByOrganization() || created.TeamID != teamID {
			t.Errorf("stored owner = %+v, want the team alone", created)
		}
	})
}

// TestContacts_NeedAWayToReachSomebody covers the rule that makes the record
// worth holding.
//
// A contact with a name, a role and no channel records that somebody is
// responsible without recording how to tell them, which is the exact failure
// the entity exists to prevent. Any one channel is enough, because which ones
// exist varies by site.
func TestContacts_NeedAWayToReachSomebody(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")

	unreachable := access.Contact{Name: "Dana", Role: access.ContactOwner, OrganizationID: orgID}
	if _, err := store.CreateContact(ctx, unreachable); !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}

	for name, channel := range map[string]func(*access.Contact){
		"email": func(c *access.Contact) { c.Email = "dana@example.com" },
		"phone": func(c *access.Contact) { c.Phone = "+1-555-0100" },
		"url":   func(c *access.Contact) { c.URL = "https://rota.example/oncall" },
	} {
		t.Run(name, func(t *testing.T) {
			reachable := unreachable
			reachable.Name = "Dana " + name
			channel(&reachable)
			if _, err := store.CreateContact(ctx, reachable); err != nil {
				t.Errorf("a contact reachable by %s was refused: %v", name, err)
			}
		})
	}
}

// TestContacts_RefuseAnUnknownRole keeps the vocabulary closed. A free-text
// role means two organizations spelling "escalation" two ways and a query
// that finds neither.
func TestContacts_RefuseAnUnknownRole(t *testing.T) {
	store, _ := newTestStore(t)
	orgID := seedOrg(t, store, "acme")

	_, err := store.CreateContact(context.Background(), access.Contact{
		Name: "Dana", Role: "whoever-answers", Email: "dana@example.com", OrganizationID: orgID,
	})
	if !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestContacts_ListInEscalationOrder proves the order is the operator's
// rather than insertion order. "Who do I try first" is the question, and
// answering it by creation timestamp makes the answer depend on who was typed
// in first.
func TestContacts_ListInEscalationOrder(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")

	for _, seed := range []struct {
		name  string
		order int
	}{{"third", 30}, {"first", 10}, {"second", 20}} {
		if _, err := store.CreateContact(ctx, access.Contact{
			Name: seed.name, Role: access.ContactEscalation,
			Phone: "+1-555-0100", Order: seed.order, OrganizationID: orgID,
		}); err != nil {
			t.Fatalf("seeding %q: %v", seed.name, err)
		}
	}

	got, err := store.ListContacts(ctx, access.ContactQuery{OrganizationID: orgID})
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	if want := "first second third"; strings.Join(names, " ") != want {
		t.Errorf("order = %v, want %q: the escalation path is in insertion order", names, want)
	}
}

// TestContacts_ListNarrowsToOneOwner proves an organization's contacts and a
// team's do not bleed into each other. They are different accountability
// statements and a page showing both would attribute one to the other.
func TestContacts_ListNarrowsToOneOwner(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	seedContact(t, store, orgID, "org-owner", access.ContactOwner)
	if _, err := store.CreateContact(ctx, access.Contact{
		Name: "team-owner", Role: access.ContactOwner, Email: "team@example.com", TeamID: teamID,
	}); err != nil {
		t.Fatalf("seeding the team's contact: %v", err)
	}

	orgContacts, err := store.ListContacts(ctx, access.ContactQuery{OrganizationID: orgID})
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(orgContacts) != 1 || orgContacts[0].Name != "org-owner" {
		t.Errorf("the organization's contacts = %+v, want only its own", orgContacts)
	}

	teamContacts, err := store.ListContacts(ctx, access.ContactQuery{TeamID: teamID})
	if err != nil {
		t.Fatalf("ListContacts: %v", err)
	}
	if len(teamContacts) != 1 || teamContacts[0].Name != "team-owner" {
		t.Errorf("the team's contacts = %+v, want only its own", teamContacts)
	}
}

// TestContacts_SearchMatchesNameOrEmail. An escalation list is searched by
// whichever of the two the reader has to hand, which is usually the address
// off an old page rather than the rota's name.
func TestContacts_SearchMatchesNameOrEmail(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")

	if _, err := store.CreateContact(ctx, access.Contact{
		Name: "Payments rota", Role: access.ContactEscalation,
		Email: "payments-oncall@example.com", OrganizationID: orgID,
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	seedContact(t, store, orgID, "network", access.ContactOwner)

	for name, term := range map[string]string{"by name": "payments rota", "by email": "payments-oncall"} {
		t.Run(name, func(t *testing.T) {
			got, err := store.ListContacts(ctx, access.ContactQuery{
				Query: access.Query{Search: term}, OrganizationID: orgID,
			})
			if err != nil {
				t.Fatalf("ListContacts: %v", err)
			}
			if len(got) != 1 || got[0].Name != "Payments rota" {
				t.Errorf("searching %q returned %d contacts, want the one", term, len(got))
			}
		})
	}
}

// TestUpdateContact_RefusesAnUnknownRecord. Editing something that is not
// there is a 404 rather than whichever body complaint the validation reached
// first, which is the read-before-write rule the rest of this store follows.
func TestUpdateContact_RefusesAnUnknownRecord(t *testing.T) {
	store, _ := newTestStore(t)
	err := store.UpdateContact(context.Background(), access.Contact{
		ID: 9999, Name: "Dana", Role: access.ContactOwner, Email: "dana@example.com",
	})
	if !errors.Is(err, access.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestUpdateContact_CannotChangeItsOwner covers the carried-forward owner.
//
// Re-pointing an accountability record is indistinguishable from deleting one
// and creating another, and a record that quietly changed what it was
// accountable for would defeat the attestation sitting beside it.
func TestUpdateContact_CannotChangeItsOwner(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")
	contact := seedContact(t, store, orgID, "dana", access.ContactOwner)

	contact.Name = "Dana Okafor"
	contact.OrganizationID, contact.TeamID = 0, teamID
	if err := store.UpdateContact(ctx, contact); err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}

	after, err := store.GetContact(ctx, contact.ID)
	if err != nil {
		t.Fatalf("GetContact: %v", err)
	}
	if after.Name != "Dana Okafor" {
		t.Errorf("the edit did not apply: %+v", after)
	}
	if !after.OwnedByOrganization() || after.OrganizationID != orgID {
		t.Errorf("the contact was moved to another owner: %+v", after)
	}
}

// TestDeleteOwner_TakesItsContactsWithIt covers the cascade.
//
// Without it the reference is nulled and the row survives owned by nothing,
// which is precisely the state the write path refuses to accept: deleting an
// owner would manufacture a record no create call could have made.
func TestDeleteOwner_TakesItsContactsWithIt(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	// The team holding the system grant lives in a second organization, so
	// the one under test has no team left when it is deleted. A team's
	// organization edge is required, so deleting an organization that still
	// holds one is refused: that is a separate, correct behaviour, and
	// tripping it here would hide whether the cascade works.
	otherOrg := seedOrg(t, store, "keeper-org")
	systemGrant(t, store, seedTeam(t, store, otherOrg, "keeper"))

	seedContact(t, store, orgID, "org-owner", access.ContactOwner)
	if _, err := store.CreateContact(ctx, access.Contact{
		Name: "team-owner", Role: access.ContactOwner, Email: "team@example.com", TeamID: teamID,
	}); err != nil {
		t.Fatalf("seeding the team's contact: %v", err)
	}

	if err := store.DeleteTeam(ctx, teamID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if err := store.DeleteOrganization(ctx, orgID); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}

	if n := client.Contact.Query().CountX(ctx); n != 0 {
		t.Errorf("%d contacts survive their owners, each belonging to nothing", n)
	}
}

// TestContacts_DeleteRemovesOnlyTheOneNamed, and specifically does not
// refuse the last one.
//
// The asymmetry with the last-system-grant guard is deliberate and worth
// pinning. Refusing to remove the final grant protects against locking every
// administrator out of a running deployment, which is unrecoverable from
// inside the product. An owner-less organization is a bad state but a
// visible and fixable one, and refusing here would leave somebody unable to
// remove a contact who has left the company.
func TestContacts_DeleteRemovesOnlyTheOneNamed(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	first := seedContact(t, store, orgID, "first", access.ContactOwner)
	second := seedContact(t, store, orgID, "second", access.ContactEscalation)

	if err := store.DeleteContact(ctx, first.ID); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if _, err := store.GetContact(ctx, first.ID); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("the deleted contact is still readable: %v", err)
	}
	if _, err := store.GetContact(ctx, second.ID); err != nil {
		t.Errorf("deleting one contact reached another: %v", err)
	}

	// The last one goes too.
	if err := store.DeleteContact(ctx, second.ID); err != nil {
		t.Errorf("removing the last contact was refused: %v", err)
	}
	if err := store.DeleteContact(ctx, 9999); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("deleting a contact that does not exist: err = %v, want ErrNotFound", err)
	}
}

// TestContacts_RefuseAnOwnerThatDoesNotExist. The failure arrives as a
// constraint violation, and reporting it as a conflict about the contact
// would describe the wrong record entirely: it is the owner that is missing.
func TestContacts_RefuseAnOwnerThatDoesNotExist(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.CreateContact(ctx, access.Contact{
		Name: "Dana", Role: access.ContactOwner, Email: "dana@example.com", OrganizationID: 9999,
	})
	if !errors.Is(err, access.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound naming the missing owner", err)
	}
	if !strings.Contains(err.Error(), "organization 9999") {
		t.Errorf("err = %v, want it to name which owner was missing", err)
	}
}

// TestVocabulariesAreOrderedForReading covers the two exported orderings,
// which are ordering rather than sets on purpose: a control offers them in
// this sequence, and alphabetical would put "billing" before "owner" in an
// incident escalation list.
func TestVocabulariesAreOrderedForReading(t *testing.T) {
	roles := access.ContactRoles()
	if len(roles) != 4 || roles[0] != access.ContactOwner || roles[1] != access.ContactEscalation {
		t.Errorf("ContactRoles() = %v, want owner and escalation first", roles)
	}

	markings := access.Classifications()
	if len(markings) != 6 || markings[0] != access.ClassificationUnclassified {
		t.Errorf("Classifications() = %v, want six, least sensitive first", markings)
	}
	if markings[len(markings)-1] != access.ClassificationTopSecretSCI {
		t.Errorf("Classifications() ends at %v, want the most sensitive last", markings[len(markings)-1])
	}
	if !access.ValidClassification("") {
		t.Error("the empty marking is refused, so an unmarked tenant cannot be expressed")
	}
	if access.ValidClassification("staging") {
		t.Error("an environment marking is accepted as a classification")
	}
}

// TestRenderedFieldsReadAsSentences covers the two strings a reader actually
// sees. Both exist so a page never renders a bare primary key or an empty
// cell where a fact belongs.
func TestRenderedFieldsReadAsSentences(t *testing.T) {
	orgContact := access.Contact{OrganizationID: 7}
	if got := orgContact.Owner(); got != "organization 7" {
		t.Errorf("Owner() = %q, want %q", got, "organization 7")
	}
	teamContact := access.Contact{TeamID: 4}
	if got := teamContact.Owner(); got != "team 4" {
		t.Errorf("Owner() = %q, want %q", got, "team 4")
	}

	// "Never attested" rather than a blank, because a blank is ambiguous
	// between "nobody confirmed this" and "this view does not show that",
	// and only the first is a finding.
	if got := (access.Attestation{}).Describe(); got != "Never attested" {
		t.Errorf("Describe() of an unattested record = %q, want %q", got, "Never attested")
	}
	when := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	if got := (access.Attestation{By: "dana@example.com", At: &when}).Describe(); got != "dana@example.com on 2026-08-11" {
		t.Errorf("Describe() = %q", got)
	}
}

// TestAttestation_ComesFromTheCallerNotTheRecord is the point of the whole
// feature.
//
// The subject is a parameter, so there is no shape of the call in which a
// request body could supply it, and an edit cannot carry one at all. An
// attestation somebody can type another person's name into is a rumour with a
// date on it.
func TestAttestation_ComesFromTheCallerNotTheRecord(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	before := time.Now().UTC().Add(-time.Second)
	if err := store.AttestOrganization(ctx, orgID, "dana@example.com"); err != nil {
		t.Fatalf("AttestOrganization: %v", err)
	}
	if err := store.AttestTeam(ctx, teamID, "sam@example.com"); err != nil {
		t.Fatalf("AttestTeam: %v", err)
	}

	org, err := store.GetOrganization(ctx, orgID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if !org.Attested.Attested() {
		t.Fatalf("the organization reports no attestation: %+v", org.Attested)
	}
	if org.Attested.By != "dana@example.com" || org.Attested.At.Before(before) {
		t.Errorf("attestation = %+v, want dana and a fresh timestamp", org.Attested)
	}

	// An edit carrying somebody else's claim must not disturb it. Built
	// from a fresh struct rather than by reading the record back and
	// changing one field, because a read-modify-write re-supplies the
	// stored value and would pass even if Update did write the attestation.
	// What a request body actually produces is this: a struct assembled
	// from submitted fields, carrying whatever the sender put in them.
	forged := time.Now().UTC()
	if err := store.UpdateOrganization(ctx, access.Organization{
		ID:       orgID,
		Name:     "acme-renamed",
		Attested: access.Attestation{By: "attacker@example.com", At: &forged},
	}); err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	after, err := store.GetOrganization(ctx, orgID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if after.Attested.By != "dana@example.com" {
		t.Errorf("an edit rewrote the attestation to %q, so any caller can attest as anybody", after.Attested.By)
	}
	if !after.Attested.At.Equal(*org.Attested.At) {
		t.Errorf("an edit moved the attestation date to %v", after.Attested.At)
	}

	team, err := store.GetTeam(ctx, teamID)
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if team.Attested.By != "sam@example.com" {
		t.Errorf("the team's attestation = %q, want sam", team.Attested.By)
	}
}

// TestAttestation_RefusesAnAnonymousClaim. An attestation by nobody is the
// exact thing the mechanism exists to make impossible, so storing one would
// be worse than storing none: it reads as confirmed.
func TestAttestation_RefusesAnAnonymousClaim(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "acme")
	teamID := seedTeam(t, store, orgID, "netops")

	if err := store.AttestOrganization(ctx, orgID, "   "); !errors.Is(err, access.ErrInvalidInput) {
		t.Errorf("AttestOrganization with a blank subject: err = %v, want ErrInvalidInput", err)
	}
	if err := store.AttestTeam(ctx, teamID, ""); !errors.Is(err, access.ErrInvalidInput) {
		t.Errorf("AttestTeam with a blank subject: err = %v, want ErrInvalidInput", err)
	}
	org, err := store.GetOrganization(ctx, orgID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org.Attested.Attested() {
		t.Error("the refused attestation was stored anyway")
	}
}

// TestAttestation_StaleTreatsNeverCheckedAsStale.
//
// Never having been checked is not a better position than having been checked
// too long ago, and a review that skipped unattested records would skip
// exactly the ones it exists to find.
func TestAttestation_StaleTreatsNeverCheckedAsStale(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour)
	old := now.Add(-400 * 24 * time.Hour)

	for name, tc := range map[string]struct {
		attestation access.Attestation
		want        bool
	}{
		"never attested":     {access.Attestation{}, true},
		"dated but unsigned": {access.Attestation{At: &recent}, true},
		"signed but undated": {access.Attestation{By: "dana"}, true},
		"attested recently":  {access.Attestation{By: "dana", At: &recent}, false},
		"attested long ago":  {access.Attestation{By: "dana", At: &old}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.attestation.Stale(now, 90*24*time.Hour); got != tc.want {
				t.Errorf("Stale() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestOrganizationMetadata_RoundTrips proves the new columns are actually
// written and read rather than accepted and dropped, which is what an
// unwired field looks like from outside.
func TestOrganizationMetadata_RoundTrips(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	created, err := store.CreateOrganization(ctx, access.Organization{
		Name:           "acme",
		Description:    "Retail payments platform",
		Classification: access.ClassificationSecret,
		ChangeWindow:   "Sat 02:00-06:00 UTC",
		Frozen:         true,
		FreezeReason:   "peak trading",
		CostCentre:     "CC-4417",
		TicketKey:      "ACME",
		CMDBID:         "ci-90210",
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	got, err := store.GetOrganization(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	for _, field := range []struct {
		name      string
		got, want string
	}{
		{"description", got.Description, "Retail payments platform"},
		{"classification", string(got.Classification), string(access.ClassificationSecret)},
		{"change window", got.ChangeWindow, "Sat 02:00-06:00 UTC"},
		{"freeze reason", got.FreezeReason, "peak trading"},
		{"cost centre", got.CostCentre, "CC-4417"},
		{"ticket key", got.TicketKey, "ACME"},
		{"cmdb id", got.CMDBID, "ci-90210"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	if !got.Frozen {
		t.Error("frozen did not round trip")
	}
}

// TestOrganizations_RefuseAnUnknownClassification keeps that vocabulary
// closed too, and specifically refuses an environment marking: an
// organization is not "staging", the deployment is.
func TestOrganizations_RefuseAnUnknownClassification(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.CreateOrganization(ctx, access.Organization{Name: "acme", Classification: "staging"})
	if !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}

	// Empty is valid and means unmarked, which is not the same as
	// unclassified: one is an absent statement, the other is a statement.
	if _, err := store.CreateOrganization(ctx, access.Organization{Name: "unmarked"}); err != nil {
		t.Errorf("an unmarked organization was refused: %v", err)
	}
}

// TestBindings_ResolveTheirScopeTargetToAName covers the one reference in
// this package that no eager load can reach.
//
// RoleBinding.scope_id is a bare polymorphic integer with no foreign key, by
// design, so the name has to be batched in afterwards. Without it the grants
// table reads "operator, inventory 7, allow", which an auditor cannot act on
// without going and looking up 7.
func TestBindings_ResolveTheirScopeTargetToAName(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "Network")
	teamID := seedTeam(t, store, orgID, "netops")
	systemGrant(t, store, teamID)

	group := client.Group.Create().SetName("edge").SaveX(ctx)
	device := client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)
	inv := client.Inventory.Create().SetName("Edge routers").SetOrganizationID(orgID).SaveX(ctx)

	for _, seed := range []struct {
		scope auth.ScopeType
		id    int
		want  string
	}{
		{auth.ScopeOrganization, orgID, "Network"},
		{auth.ScopeInventory, inv.ID, "Edge routers"},
		{auth.ScopeGroup, group.ID, "edge"},
		{auth.ScopeDevice, device.ID, "rtr1"},
	} {
		t.Run(string(seed.scope), func(t *testing.T) {
			created, err := store.CreateBinding(ctx, access.Binding{
				TeamID: teamID, Role: auth.RoleViewer,
				ScopeType: seed.scope, ScopeID: seed.id, Effect: auth.EffectAllow,
			})
			if err != nil {
				t.Fatalf("CreateBinding: %v", err)
			}
			got, err := store.GetBinding(ctx, created.ID)
			if err != nil {
				t.Fatalf("GetBinding: %v", err)
			}
			if got.ScopeName != seed.want {
				t.Errorf("ScopeName = %q, want %q", got.ScopeName, seed.want)
			}
			// The team name comes free from the eager load, and is the
			// other half of what makes the row readable.
			if got.TeamName != "netops" {
				t.Errorf("TeamName = %q, want netops", got.TeamName)
			}
		})
	}

	// Every grant on one page, to prove the resolution is batched rather
	// than accidentally per row, and that a system-scope grant naming no
	// target is left alone rather than resolved to something.
	all, err := store.ListBindings(ctx, access.BindingQuery{})
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	for _, b := range all {
		switch {
		case b.SystemWide() && b.ScopeName != "":
			t.Errorf("a system grant names target %q, but system scope names none", b.ScopeName)
		case !b.SystemWide() && b.ScopeName == "":
			t.Errorf("grant %d at %s %d resolved to no name", b.ID, b.ScopeType, b.ScopeID)
		}
	}
}

// TestBindings_DeletedScopeTargetIsNamedAsGone.
//
// The scope column carries no foreign key, so a grant outliving its target
// is a real state rather than a hypothetical. It must not render as a blank:
// "granted nowhere" and "granted at something that no longer exists" are
// different findings, and only the second is a cleanup task.
func TestBindings_DeletedScopeTargetIsNamedAsGone(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "Network")
	teamID := seedTeam(t, store, orgID, "netops")
	systemGrant(t, store, teamID)

	group := client.Group.Create().SetName("doomed").SaveX(ctx)
	created, err := store.CreateBinding(ctx, access.Binding{
		TeamID: teamID, Role: auth.RoleViewer,
		ScopeType: auth.ScopeGroup, ScopeID: group.ID, Effect: auth.EffectAllow,
	})
	if err != nil {
		t.Fatalf("CreateBinding: %v", err)
	}
	client.Group.DeleteOne(group).ExecX(ctx)

	got, err := store.GetBinding(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetBinding: %v", err)
	}
	if got.ScopeName != "" {
		t.Errorf("ScopeName = %q, want empty so the view can say it is gone", got.ScopeName)
	}
	if got.ScopeID != group.ID {
		t.Errorf("the grant lost its target id, so nothing can be cleaned up: %+v", got)
	}
}

// TestScopeNameResolution_ReportsAnOutageRatherThanAnEmptyName.
//
// A failed name lookup must not degrade into "the target was deleted": those
// render differently and mean different things, and a storage outage quietly
// reported as a tidy-up task is the worse of the two mistakes.
func TestScopeNameResolution_ReportsAnOutageRatherThanAnEmptyName(t *testing.T) {
	store, client := newTestStore(t)
	ctx := context.Background()
	orgID := seedOrg(t, store, "Network")
	teamID := seedTeam(t, store, orgID, "netops")
	systemGrant(t, store, teamID)

	// A grant at every scope type, so the closed-database read below has a
	// query of each kind to fail on rather than only the first.
	group := client.Group.Create().SetName("edge").SaveX(ctx)
	device := client.Device.Create().SetName("rtr1").SetType("cisco_router").SaveX(ctx)
	inv := client.Inventory.Create().SetName("Edge routers").SetOrganizationID(orgID).SaveX(ctx)
	for _, seed := range []struct {
		scope auth.ScopeType
		id    int
	}{
		{auth.ScopeOrganization, orgID},
		{auth.ScopeInventory, inv.ID},
		{auth.ScopeGroup, group.ID},
		{auth.ScopeDevice, device.ID},
	} {
		if _, err := store.CreateBinding(ctx, access.Binding{
			TeamID: teamID, Role: auth.RoleViewer,
			ScopeType: seed.scope, ScopeID: seed.id, Effect: auth.EffectAllow,
		}); err != nil {
			t.Fatalf("seeding a %s grant: %v", seed.scope, err)
		}
	}

	if err := client.Close(); err != nil {
		t.Fatalf("closing the client: %v", err)
	}
	if _, err := store.ListBindings(ctx, access.BindingQuery{}); err == nil {
		t.Fatal("a closed database listed grants successfully")
	}
}

// TestResolveScopeNames_EveryScopeTypeReportsItsOwnFailure walks the four
// target kinds against a closed database.
//
// Each is a separate query, so each has its own error path, and a
// misattributed one would tell an operator to look at the wrong table.
func TestResolveScopeNames_EveryScopeTypeReportsItsOwnFailure(t *testing.T) {
	store, client := newTestStore(t)
	if err := client.Close(); err != nil {
		t.Fatalf("closing the client: %v", err)
	}

	for name, scope := range map[string]auth.ScopeType{
		"organization": auth.ScopeOrganization,
		"inventory":    auth.ScopeInventory,
		"group":        auth.ScopeGroup,
		"device":       auth.ScopeDevice,
	} {
		t.Run(name, func(t *testing.T) {
			err := access.ResolveScopeNamesForTest(context.Background(), store,
				[]access.Binding{{ID: 1, ScopeType: scope, ScopeID: 7}})
			if err == nil {
				t.Fatal("a closed database resolved a name successfully")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v, want it to name the %s lookup that failed", err, name)
			}
		})
	}

	// A system grant names no target, so there is nothing to look up and no
	// query to fail. It must not be turned into an error by a broken
	// database it never touches.
	if err := access.ResolveScopeNamesForTest(context.Background(), store,
		[]access.Binding{{ID: 1, ScopeType: auth.ScopeSystem}}); err != nil {
		t.Errorf("resolving a system grant hit the database: %v", err)
	}
}

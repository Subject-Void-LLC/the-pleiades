package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// This file covers the two things about the Contacts view that the shared
// conformance assertions cannot reach: the owner is set once and never
// again, and it is named rather than numbered.
//
// The fixture seeds one contact against an organization and one against a
// team, per iteration, so both branches of the exactly-one-owner invariant
// are rendered by every assertion below.

func TestContactsView_TheCreateFormOffersBothKindsOfOwner(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/contacts/new")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/contacts/new = %d, want 200", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, `name="owner"`) {
		t.Fatal("the create form has no owner control, so a contact could not be attached to anything")
	}

	// One control listing both kinds, each labelled with which it is. Two
	// separate selects would make "both owners" and "neither owner"
	// submittable states, and the whole point of the single control is that
	// the invariant is enforced by its shape rather than by remembering to
	// check it.
	for _, kind := range []string{"(organization)", "(team)"} {
		if !strings.Contains(body, kind) {
			t.Errorf("the owner control offers no %s option", kind)
		}
	}
}

func TestContactsView_TheEditFormDoesNotOfferTheOwner(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/contacts/1/edit")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/contacts/1/edit = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// Absent rather than disabled. A contact's owner is carried forward
	// from storage on every update, so a control offering to change it is a
	// control whose value is then ignored: the operator picks a different
	// tenant, submits, sees a success, and nothing moved.
	if strings.Contains(body, `name="owner"`) {
		t.Error("the edit form renders an owner control, which the store then ignores")
	}

	// The rest of the form is still there, so this is a narrowed form
	// rather than a broken one.
	for _, offered := range []string{`name="name"`, `name="role"`, `name="email"`} {
		if !strings.Contains(body, offered) {
			t.Errorf("the edit form has no control for %s", offered)
		}
	}
}

func TestContactsView_RefusesAnEditThatSmugglesAnOwner(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// The edit form never offered the control, so a submission carrying it
	// did not come from the form. Refused rather than ignored: silently
	// dropping it is how somebody ends up certain they moved a contact
	// between tenants when they did not.
	w := h.post(t, "/ui/contacts/1", map[string]string{
		"name":  "conformance-owner",
		"role":  "owner",
		"email": "owner@example.com",
		"owner": "team:1",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST with an owner = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestContactsView_NamesTheOwnerRatherThanNumberingIt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/contacts")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/contacts = %d, want 200", w.Code)
	}
	body := w.Body.String()

	// Both branches, because the projection reads a different edge for
	// each and one of them being right proves nothing about the other.
	for _, owner := range []string{"organization conformance", "team conformance-team"} {
		if !strings.Contains(body, owner) {
			t.Errorf("the list does not name %q as an owner", owner)
		}
	}

	// The id form is what this replaced. A list that prints "organization
	// 1" has not saved the reader a join, it has moved the join into their
	// head.
	for _, key := range []string{"organization 1", "team 1"} {
		if strings.Contains(body, key) {
			t.Errorf("the list renders %q, which asks the reader to know which record that is", key)
		}
	}
}

func TestContactsView_ARequiredChannelIsAnyOfThree(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// No email, no phone, no URL. A contact nobody can reach records that
	// somebody is responsible without recording how to tell them, which is
	// the failure the entity exists to prevent, so it is refused at the
	// form rather than only in the store.
	w := h.post(t, "/ui/contacts", map[string]string{
		"name":  "unreachable",
		"role":  "owner",
		"owner": "organization:1",
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST with no channel = %d, want 422: %s", w.Code, w.Body.String())
	}

	// And any one of the three is enough, so the refusal above is about the
	// set rather than about a field somebody forgot to fill in.
	w = h.post(t, "/ui/contacts", map[string]string{
		"name":  "reachable-by-rota",
		"role":  "escalation",
		"owner": "organization:1",
		"url":   "https://example.invalid/rota",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST with only a URL = %d, want 303: %s", w.Code, w.Body.String())
	}
}

func TestAccessViews_CarryTheContactsOfTheRecordTheyAreOn(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// The organization's own contact, and not the team's. Both hang off the
	// same fixture iteration and a section that queried by id without the
	// owner column would show each on both pages.
	for _, tc := range []struct{ path, present, absent string }{
		{"/ui/organizations/1", "conformance-owner", "conformance-rota"},
		{"/ui/teams/1", "conformance-rota", "conformance-owner"},
	} {
		w := h.get(t, tc.path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", tc.path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "tab=contacts") {
			t.Errorf("%s offers no Contacts tab", tc.path)
			continue
		}
		body := h.section(t, tc.path, "Contacts")

		if !strings.Contains(body, "Contacts") {
			t.Errorf("%s has no Contacts section", tc.path)
		}
		if !strings.Contains(body, tc.present) {
			t.Errorf("%s does not list %q, which is accountable for it", tc.path, tc.present)
		}
		if strings.Contains(body, tc.absent) {
			t.Errorf("%s lists %q, which answers for a different record", tc.path, tc.absent)
		}
	}
}

func TestTeamsView_TheEditFormDoesNotOfferTheOrganization(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// The defect that produced view.Field.Immutable. UpdateTeam carries the
	// organization forward from storage, and the edit form rendered the
	// select anyway: somebody could re-tenant a team, submit, see a
	// success, and every grant the team holds would still be scoped where
	// it was.
	w := h.get(t, "/ui/teams/1/edit")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/teams/1/edit = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), `name="organization"`) {
		t.Error("the edit form offers an organization control, which the store then ignores")
	}

	// Still offered when the team is created, which is the one moment the
	// decision is actually taken.
	w = h.get(t, "/ui/teams/new")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/teams/new = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `name="organization"`) {
		t.Error("the create form has no organization control, so a team could not be tenanted")
	}
}

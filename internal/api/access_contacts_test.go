package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the accountability endpoints over the same real
// ent-backed store the rest of the access suite uses.
//
// The assertion that matters is the attestation's provenance. Everything
// else here is ordinary CRUD; that one is the reason the feature exists, and
// it can only be proven at this layer, because the question is what the HTTP
// boundary does with a subject a caller tried to supply.

// contactsRouter mounts the contact and attest endpoints alongside the rest
// of the access surface.
func contactsRouter(t *testing.T) http.Handler {
	t.Helper()
	router, _ := accessRouter(t)
	return router
}

// seedOwnedTeam creates an organization and a team inside it, returning both
// ids, since almost every contact assertion needs one owner of each kind.
func seedOwnedTeam(t *testing.T, router http.Handler) (orgID, teamID int) {
	t.Helper()
	orgID = createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID = createdID(t, router, "/api/v1/teams",
		fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	return orgID, teamID
}

func TestContacts_Lifecycle(t *testing.T) {
	router := contactsRouter(t)
	orgID, _ := seedOwnedTeam(t, router)

	id := createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Payments on-call","role":"escalation","phone":"+1-555-0100","order":10,"organization":%d}`, orgID))

	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/contacts/%d", id), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Name           string `json:"name"`
		Role           string `json:"role"`
		Phone          string `json:"phone"`
		Order          int    `json:"order"`
		Organization   int    `json:"organization"`
		AccountableFor string `json:"accountable_for"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Name != "Payments on-call" || got.Role != "escalation" || got.Phone != "+1-555-0100" {
		t.Errorf("round trip lost something: %+v", got)
	}
	if got.Order != 10 || got.Organization != orgID {
		t.Errorf("order or owner did not round trip: %+v", got)
	}
	// The owner named rather than numbered. This used to assert
	// "organization 1" and called that rendered, which was true only in the
	// sense that it was not a bare integer: a caller still has to fetch the
	// organization to learn which tenant it is, and the id is beside it in
	// the same document for anybody who wants to.
	if want := "organization acme"; got.AccountableFor != want {
		t.Errorf("accountable_for = %q, want %q", got.AccountableFor, want)
	}
	if got.Organization != orgID {
		t.Errorf("organization = %d, want %d: the id stays on the wire beside the name", got.Organization, orgID)
	}

	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/contacts/%d", id),
		`{"name":"Payments rota","role":"escalation","phone":"+1-555-0100"}`); rec.Code != http.StatusOK {
		t.Errorf("update: status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/contacts/%d", id), ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/contacts/%d", id), ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete: status = %d, want 404", rec.Code)
	}
}

// TestContacts_PatchWithoutOrderLeavesThePositionAlone is the same partial
// update defect this surface has already shipped twice (FAILURE_PATTERNS.md
// #103), in the one place it would be silent: zero is a valid position, so a
// plain int cannot tell "put this first" apart from "the caller said nothing
// about ordering", and a rename would promote a contact to the top of the
// escalation path.
func TestContacts_PatchWithoutOrderLeavesThePositionAlone(t *testing.T) {
	router := contactsRouter(t)
	orgID, _ := seedOwnedTeam(t, router)

	id := createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Third line","role":"escalation","phone":"+1-555-0300","order":30,"organization":%d}`, orgID))

	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/contacts/%d", id),
		`{"name":"Third line renamed","role":"escalation","phone":"+1-555-0300"}`); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Name  string `json:"name"`
		Order int    `json:"order"`
	}
	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/contacts/%d", id), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Name != "Third line renamed" {
		t.Errorf("the rename did not apply: %+v", got)
	}
	if got.Order != 30 {
		t.Errorf("order = %d, want 30: a rename moved this contact to position %d in the escalation path", got.Order, got.Order)
	}
}

// TestContacts_RefuseAnUnusableSubmission checks the three refusals at the
// boundary that serves them, because each maps to a status a caller acts on.
func TestContacts_RefuseAnUnusableSubmission(t *testing.T) {
	router := contactsRouter(t)
	orgID, teamID := seedOwnedTeam(t, router)

	for name, body := range map[string]string{
		"no owner":     `{"name":"Dana","role":"owner","email":"dana@example.com"}`,
		"two owners":   fmt.Sprintf(`{"name":"Dana","role":"owner","email":"d@e.com","organization":%d,"team":%d}`, orgID, teamID),
		"unreachable":  fmt.Sprintf(`{"name":"Dana","role":"owner","organization":%d}`, orgID),
		"unknown role": fmt.Sprintf(`{"name":"Dana","role":"whoever","email":"d@e.com","organization":%d}`, orgID),
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, router, http.MethodPost, "/api/v1/contacts", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestContacts_ListNarrowsToOneOwner covers the query parameters, and that a
// malformed one is refused rather than ignored.
//
// The refusal matters more than it looks: silently dropping an unparseable
// owner filter answers "every contact in the deployment" to a request that
// asked for one organization's, and that is an answer nobody checks because
// it looks like data.
func TestContacts_ListNarrowsToOneOwner(t *testing.T) {
	router := contactsRouter(t)
	orgID, teamID := seedOwnedTeam(t, router)

	createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Org owner","role":"owner","email":"org@example.com","organization":%d}`, orgID))
	createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Team owner","role":"owner","email":"team@example.com","team":%d}`, teamID))

	names := func(target string) []string {
		t.Helper()
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d: %s", target, rec.Code, rec.Body.String())
		}
		var list struct {
			Contacts []struct {
				Name string `json:"name"`
			} `json:"contacts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		out := make([]string, 0, len(list.Contacts))
		for _, c := range list.Contacts {
			out = append(out, c.Name)
		}
		return out
	}

	if got := names(fmt.Sprintf("/api/v1/contacts?organization=%d", orgID)); strings.Join(got, ",") != "Org owner" {
		t.Errorf("organization filter returned %v, want only the organization's own", got)
	}
	if got := names(fmt.Sprintf("/api/v1/contacts?team=%d", teamID)); strings.Join(got, ",") != "Team owner" {
		t.Errorf("team filter returned %v, want only the team's own", got)
	}
	if got := names("/api/v1/contacts"); len(got) != 2 {
		t.Errorf("unfiltered listing returned %v, want both", got)
	}

	for _, target := range []string{"/api/v1/contacts?organization=nonsense", "/api/v1/contacts?team=0"} {
		if rec := doJSON(t, router, http.MethodGet, target, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400: a dropped filter answers a different question than the one asked",
				target, rec.Code)
		}
	}
}

// TestAttestation_SignsWithTheCallerNotTheBody is the assertion this whole
// feature turns on.
//
// The request deliberately carries a body naming somebody else. It must have
// no effect whatsoever: the stored subject comes from the authenticated
// identity, because an attestation whose signer is chosen by whoever is
// writing records only that a request was made.
func TestAttestation_SignsWithTheCallerNotTheBody(t *testing.T) {
	router := contactsRouter(t)
	orgID, teamID := seedOwnedTeam(t, router)

	forged := `{"attested_by":"somebody-else@example.com","attested_at":"2020-01-01T00:00:00Z"}`

	for name, tc := range map[string]struct {
		target string
		read   string
	}{
		"organization": {
			target: fmt.Sprintf("/api/v1/organizations/%d/attest", orgID),
			read:   fmt.Sprintf("/api/v1/organizations/%d", orgID),
		},
		"team": {
			target: fmt.Sprintf("/api/v1/teams/%d/attest", teamID),
			read:   fmt.Sprintf("/api/v1/teams/%d", teamID),
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, router, http.MethodPost, tc.target, forged)
			if rec.Code != http.StatusOK {
				t.Fatalf("attest: status = %d: %s", rec.Code, rec.Body.String())
			}

			var got struct {
				AttestedBy string `json:"attested_by"`
				AttestedAt string `json:"attested_at"`
			}
			if err := json.Unmarshal(doJSON(t, router, http.MethodGet, tc.read, "").Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if got.AttestedBy != "test-user" {
				t.Errorf("attested_by = %q, want the caller's own subject: a request body chose the signer",
					got.AttestedBy)
			}
			if got.AttestedAt == "" {
				t.Error("attested_at is empty, so the attestation records no date")
			}
			if strings.HasPrefix(got.AttestedAt, "2020") {
				t.Errorf("attested_at = %q, which is the date the request asked for rather than now", got.AttestedAt)
			}
		})
	}
}

// TestAttestation_UnknownRecordIsNotFound. Attesting something that is not
// there must not create anything, and must say so.
func TestAttestation_UnknownRecordIsNotFound(t *testing.T) {
	router := contactsRouter(t)

	for _, target := range []string{"/api/v1/organizations/9999/attest", "/api/v1/teams/9999/attest"} {
		if rec := doJSON(t, router, http.MethodPost, target, ""); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: status = %d, want 404", target, rec.Code)
		}
	}
}

// TestContacts_RefusalsCarryTheStatusACallerActsOn walks the boundary
// failures, because each maps to a different decision: 400 means fix the
// request, 404 means the record is gone, and answering either with the other
// sends a caller looking in the wrong place.
func TestContacts_RefusalsCarryTheStatusACallerActsOn(t *testing.T) {
	router := contactsRouter(t)
	orgID, _ := seedOwnedTeam(t, router)

	for name, tc := range map[string]struct {
		method, target, body string
		want                 int
	}{
		"get a malformed id":         {http.MethodGet, "/api/v1/contacts/nonsense", "", http.StatusBadRequest},
		"update a malformed id":      {http.MethodPatch, "/api/v1/contacts/0", `{"name":"x","role":"owner","email":"x@y.z"}`, http.StatusBadRequest},
		"delete a malformed id":      {http.MethodDelete, "/api/v1/contacts/-1", "", http.StatusBadRequest},
		"attest a malformed org id":  {http.MethodPost, "/api/v1/organizations/nonsense/attest", "", http.StatusBadRequest},
		"attest a malformed team id": {http.MethodPost, "/api/v1/teams/0/attest", "", http.StatusBadRequest},
		"create with a broken body":  {http.MethodPost, "/api/v1/contacts", `{"name":`, http.StatusBadRequest},
		"update with a broken body":  {http.MethodPatch, "/api/v1/contacts/1", `{"name":`, http.StatusBadRequest},
		"get a missing record":       {http.MethodGet, "/api/v1/contacts/9999", "", http.StatusNotFound},
		"update a missing record":    {http.MethodPatch, "/api/v1/contacts/9999", `{"name":"x","role":"owner","email":"x@y.z"}`, http.StatusNotFound},
		"delete a missing record":    {http.MethodDelete, "/api/v1/contacts/9999", "", http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := doJSON(t, router, tc.method, tc.target, tc.body); rec.Code != tc.want {
				t.Errorf("%s %s: status = %d, want %d: %s", tc.method, tc.target, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// An update that would make an existing contact unreachable is refused
	// on the same terms a create is: the rule belongs to the record, not to
	// the verb that reached it.
	id := createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Dana","role":"owner","email":"dana@example.com","organization":%d}`, orgID))
	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/contacts/%d", id),
		`{"name":"Dana","role":"owner"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an edit that removed every channel: status = %d, want 400", rec.Code)
	}
}

// TestAttestation_RefusesAnUnauthenticatedRequest.
//
// The real router runs authentication before any handler, so this cannot
// happen through it today. It is asserted anyway because the failure if it
// ever did would be the worst one this feature has available: an attestation
// stored with no signer, which reads as confirmed.
func TestAttestation_RefusesAnUnauthenticatedRequest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:apiattest%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	store := access.NewEntStore(client)

	org, err := store.CreateOrganization(context.Background(), access.Organization{Name: "acme"})
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}

	h := api.NewAccessHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		// Passes every request through carrying no identity at all, which
		// is the one condition the handler's own check exists for.
		Auth:      func(next http.Handler) http.Handler { return next },
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.AttestOrganization.Route(h.AttestOrganization),
			apispec.AttestTeam.Route(h.AttestTeam),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	team, err := store.CreateTeam(context.Background(), access.Team{Name: "netops", OrganizationID: org.ID})
	if err != nil {
		t.Fatalf("seeding a team: %v", err)
	}

	// Both routes, because the check is written twice and one of the two
	// copies could lose it.
	for name, target := range map[string]string{
		"organization": fmt.Sprintf("/api/v1/organizations/%d/attest", org.ID),
		"team":         fmt.Sprintf("/api/v1/teams/%d/attest", team.ID),
	} {
		if rec := doJSON(t, router, http.MethodPost, target, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401: %s", name, rec.Code, rec.Body.String())
		}
	}

	stored, err := store.GetOrganization(context.Background(), org.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if stored.Attested.Attested() {
		t.Errorf("the refused request stored an attestation anyway: %+v", stored.Attested)
	}
}

// failingReads is a real store whose read-back fails.
//
// Every write handler here writes and then reads the record back to render
// the response, and that second read is the one branch a working database
// never exercises. It is not a stub: the store underneath is the real ent
// one, and only the named read is broken, which is what an outage arriving
// between two statements actually looks like.
type failingReads struct {
	access.Store
	err error
}

func (f failingReads) GetOrganization(ctx context.Context, id int) (access.Organization, error) {
	return access.Organization{}, f.err
}

func (f failingReads) GetTeam(ctx context.Context, id int) (access.Team, error) {
	return access.Team{}, f.err
}

func (f failingReads) GetContact(ctx context.Context, id int) (access.Contact, error) {
	return access.Contact{}, f.err
}

// TestWriteHandlers_ReportAnOutageRatherThanAStaleRecord.
//
// The distinction being asserted is the one an operator acts on. If the
// read-back after a write fails, the honest answer is 500: the write may
// well have landed. Answering 404 would say the record does not exist, and
// answering 200 with a zero-valued body would say it exists and is empty,
// which is the worst of the three because it looks like data.
func TestWriteHandlers_ReportAnOutageRatherThanAStaleRecord(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:apifail%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	real := access.NewEntStore(client)
	org, err := real.CreateOrganization(ctx, access.Organization{Name: "acme"})
	if err != nil {
		t.Fatalf("seeding an organization: %v", err)
	}
	team, err := real.CreateTeam(ctx, access.Team{Name: "netops", OrganizationID: org.ID})
	if err != nil {
		t.Fatalf("seeding a team: %v", err)
	}
	contact, err := real.CreateContact(ctx, access.Contact{
		Name: "Dana", Role: access.ContactOwner, Email: "dana@example.com", OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("seeding a contact: %v", err)
	}

	h := api.NewAccessHandler(
		failingReads{Store: real, err: errors.New("the database went away")},
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.AttestOrganization.Route(h.AttestOrganization),
			apispec.AttestTeam.Route(h.AttestTeam),
			apispec.UpdateContact.Route(h.UpdateContact),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	for name, tc := range map[string]struct{ method, target, body string }{
		"attest an organization": {http.MethodPost, fmt.Sprintf("/api/v1/organizations/%d/attest", org.ID), ""},
		"attest a team":          {http.MethodPost, fmt.Sprintf("/api/v1/teams/%d/attest", team.ID), ""},
		"update a contact": {http.MethodPatch, fmt.Sprintf("/api/v1/contacts/%d", contact.ID),
			`{"name":"Dana Okafor","role":"owner","email":"dana@example.com"}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, router, tc.method, tc.target, tc.body)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestAccess_StillReferencedIsAConflictNotAConfusion.
//
// A team's organization edge is required, so an organization holding one
// cannot be deleted. Reporting that as "a record with that name already
// exists" would describe the opposite operation, which is the defect
// recorded as FAILURE_PATTERNS.md #106; this pins the mapping at the
// boundary that serves the status.
func TestAccess_StillReferencedIsAConflictNotAConfusion(t *testing.T) {
	router := contactsRouter(t)
	orgID, _ := seedOwnedTeam(t, router)

	rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/organizations/%d", orgID), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "references") {
		t.Errorf("body = %s, want it to say something still references the record", body)
	}
}

// TestOrganizationMetadata_RoundTripsOverTheWire proves the new columns are
// carried in both directions rather than accepted and dropped, which is what
// a field wired into only some of the DTO, the schema and the store looks
// like from outside.
func TestOrganizationMetadata_RoundTripsOverTheWire(t *testing.T) {
	router := contactsRouter(t)

	id := createdID(t, router, "/api/v1/organizations", `{
		"name":"acme",
		"description":"Retail payments platform",
		"classification":"secret",
		"change_window":"Sat 02:00-06:00 UTC",
		"frozen":true,
		"freeze_reason":"peak trading",
		"cost_centre":"CC-4417",
		"ticket_key":"ACME",
		"cmdb_id":"ci-90210"
	}`)

	var got struct {
		Description    string `json:"description"`
		Classification string `json:"classification"`
		ChangeWindow   string `json:"change_window"`
		Frozen         bool   `json:"frozen"`
		FreezeReason   string `json:"freeze_reason"`
		CostCentre     string `json:"cost_centre"`
		TicketKey      string `json:"ticket_key"`
		CMDBID         string `json:"cmdb_id"`
	}
	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%d", id), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, field := range []struct{ name, got, want string }{
		{"description", got.Description, "Retail payments platform"},
		{"classification", got.Classification, "secret"},
		{"change_window", got.ChangeWindow, "Sat 02:00-06:00 UTC"},
		{"freeze_reason", got.FreezeReason, "peak trading"},
		{"cost_centre", got.CostCentre, "CC-4417"},
		{"ticket_key", got.TicketKey, "ACME"},
		{"cmdb_id", got.CMDBID, "ci-90210"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	if !got.Frozen {
		t.Error("frozen did not round trip")
	}

	// An environment marking is not a tenant marking: an organization is
	// not "staging", the deployment is.
	if rec := doJSON(t, router, http.MethodPost, "/api/v1/organizations",
		`{"name":"other","classification":"staging"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an environment marking was accepted as a classification: status = %d", rec.Code)
	}
}

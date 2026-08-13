// This file covers the access administration handlers over a real
// ent-backed store rather than a stub.
//
// The store is real deliberately. What these handlers mostly do is translate
// between the wire and internal/access, and the parts worth proving are the
// refusals: an unresolvable grant, a rename that would collide, a delete that
// would leave nobody able to administer the deployment. Every one of those
// decisions belongs to the store or to the schema, so a stub would prove the
// stub.
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

// accessRouter mounts every access endpoint over a real store.
func accessRouter(t *testing.T) (http.Handler, access.Store) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:apiaccess%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })

	store := access.NewEntStore(client)
	h := api.NewAccessHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListOrganizations.Route(h.ListOrganizations),
			apispec.GetOrganization.Route(h.GetOrganization),
			apispec.CreateOrganization.Route(h.CreateOrganization),
			apispec.UpdateOrganization.Route(h.UpdateOrganization),
			apispec.DeleteOrganization.Route(h.DeleteOrganization),
			apispec.AttestOrganization.Route(h.AttestOrganization),
			apispec.ListTeams.Route(h.ListTeams),
			apispec.GetTeam.Route(h.GetTeam),
			apispec.CreateTeam.Route(h.CreateTeam),
			apispec.UpdateTeam.Route(h.UpdateTeam),
			apispec.DeleteTeam.Route(h.DeleteTeam),
			apispec.AttestTeam.Route(h.AttestTeam),
			apispec.ListUsers.Route(h.ListUsers),
			apispec.GetUser.Route(h.GetUser),
			apispec.CreateUser.Route(h.CreateUser),
			apispec.UpdateUser.Route(h.UpdateUser),
			apispec.DeleteUser.Route(h.DeleteUser),
			apispec.ListBindings.Route(h.ListBindings),
			apispec.GetBinding.Route(h.GetBinding),
			apispec.CreateBinding.Route(h.CreateBinding),
			apispec.UpdateBinding.Route(h.UpdateBinding),
			apispec.DeleteBinding.Route(h.DeleteBinding),
			apispec.ListContacts.Route(h.ListContacts),
			apispec.GetContact.Route(h.GetContact),
			apispec.CreateContact.Route(h.CreateContact),
			apispec.UpdateContact.Route(h.UpdateContact),
			apispec.DeleteContact.Route(h.DeleteContact),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router, store
}

// createdID posts a body and returns the new record's id.
func createdID(t *testing.T, router http.Handler, target, body string) int {
	t.Helper()
	rec := doJSON(t, router, http.MethodPost, target, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s: status = %d, want 201: %s", target, rec.Code, rec.Body.String())
	}
	var got struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the created record: %v", err)
	}
	if loc := rec.Header().Get("Location"); loc == "" {
		t.Error("a created record carried no Location header")
	}
	return got.ID
}

func TestAccess_OrganizationLifecycle(t *testing.T) {
	router, _ := accessRouter(t)

	id := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/organizations", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", rec.Code)
	}
	var list struct {
		Organizations []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(list.Organizations) != 1 || list.Organizations[0].Name != "acme" {
		t.Fatalf("listing returned %+v", list.Organizations)
	}

	if rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%d", id), ""); rec.Code != http.StatusOK {
		t.Errorf("get: status = %d", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/organizations/%d", id), `{"name":"acme-renamed"}`); rec.Code != http.StatusOK {
		t.Errorf("update: status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/organizations/%d", id), ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%d", id), ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete: status = %d, want 404", rec.Code)
	}
}

// TestAccess_DuplicateNameIsAConflict proves a caller's mistake is reported
// as one rather than as a platform failure.
func TestAccess_DuplicateNameIsAConflict(t *testing.T) {
	router, _ := accessRouter(t)
	createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/organizations", `{"name":"acme"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestAccess_MalformedRequestsAreRejected(t *testing.T) {
	router, _ := accessRouter(t)

	for _, tc := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"bad json", http.MethodPost, "/api/v1/organizations", `{"name":`, http.StatusBadRequest},
		{"undeclared field", http.MethodPost, "/api/v1/organizations", `{"name":"x","secret":"y"}`, http.StatusBadRequest},
		{"blank name", http.MethodPost, "/api/v1/organizations", `{"name":"   "}`, http.StatusBadRequest},
		{"zero id", http.MethodGet, "/api/v1/organizations/0", "", http.StatusBadRequest},
		{"negative id", http.MethodGet, "/api/v1/organizations/-1", "", http.StatusBadRequest},
		{"non numeric id", http.MethodGet, "/api/v1/organizations/abc", "", http.StatusBadRequest},
		{"bad after", http.MethodGet, "/api/v1/organizations?after=-1", "", http.StatusBadRequest},
		{"bad limit", http.MethodGet, "/api/v1/organizations?limit=0", "", http.StatusBadRequest},
		{"missing record", http.MethodGet, "/api/v1/organizations/4242", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, router, tc.method, tc.target, tc.body)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestAccess_TeamCarriesMembershipAndKeepsItsOrganization(t *testing.T) {
	router, _ := accessRouter(t)

	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	otherOrg := createdID(t, router, "/api/v1/organizations", `{"name":"globex"}`)
	userID := createdID(t, router, "/api/v1/users", `{"email":"operator@example.com"}`)

	teamID := createdID(t, router, "/api/v1/teams",
		fmt.Sprintf(`{"name":"netops","organization":%d,"users":[%d]}`, orgID, userID))

	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/teams/%d", teamID), "")
	var team struct {
		Organization int    `json:"organization"`
		Users        *[]int `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &team); err != nil {
		t.Fatalf("decoding team: %v", err)
	}
	if team.Users == nil || len(*team.Users) != 1 {
		t.Fatalf("membership = %v, want one member", team.Users)
	}

	// The submitted organization is ignored: moving a team between tenants
	// would silently re-scope every grant it holds.
	body := fmt.Sprintf(`{"name":"netops-renamed","organization":%d,"users":[]}`, otherOrg)
	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/teams/%d", teamID), body); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/teams/%d", teamID), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &team); err != nil {
		t.Fatalf("decoding team: %v", err)
	}
	if team.Organization != orgID {
		t.Errorf("organization = %d, want the stored %d rather than the submitted %d",
			team.Organization, orgID, otherOrg)
	}
	// An emptied membership must render as [] rather than vanishing, which
	// is the omitempty trap LESSONS_LEARNED.md #98 records.
	if team.Users == nil {
		t.Error("an emptied membership rendered as an absent key")
	}
}

// TestAccess_ListingsOmitMembership keeps a page of teams from costing one
// membership read per row for data the page does not render.
func TestAccess_ListingsOmitMembership(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))

	rec := doJSON(t, router, http.MethodGet, "/api/v1/teams", "")
	var list struct {
		Teams []struct {
			Users *[]int `json:"users"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(list.Teams) != 1 || list.Teams[0].Users != nil {
		t.Errorf("a listing carried membership: %+v", list.Teams)
	}
}

func TestAccess_UserLifecycleAndNormalisation(t *testing.T) {
	router, _ := accessRouter(t)

	id := createdID(t, router, "/api/v1/users", `{"email":"  Operator@Example.COM  "}`)

	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/users/%d", id), "")
	var user struct {
		Email string `json:"email"`
		Teams *[]int `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &user); err != nil {
		t.Fatalf("decoding user: %v", err)
	}
	if user.Email != "operator@example.com" {
		t.Errorf("email = %q, want it normalised", user.Email)
	}
	if user.Teams == nil {
		t.Error("a user with no teams rendered an absent key rather than []")
	}

	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/users/%d", id), `{"email":"renamed@example.com"}`); rec.Code != http.StatusOK {
		t.Errorf("update: status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", id), ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodGet, "/api/v1/users?q=nobody", ""); rec.Code != http.StatusOK {
		t.Errorf("search: status = %d", rec.Code)
	}
}

// TestAccess_UnresolvableBindingIsRefused is the security assertion of this
// surface. Each of these would otherwise store successfully and then
// evaluate to something other than what the operator saw in the table.
func TestAccess_UnresolvableBindingIsRefused(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))

	for _, tc := range []struct{ name, body string }{
		{"unknown role", fmt.Sprintf(`{"team":%d,"role":"superuser","scope_type":"organization","scope_id":%d,"effect":"allow"}`, teamID, orgID)},
		{"unknown scope", fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"galaxy","scope_id":1,"effect":"allow"}`, teamID)},
		{"unknown effect", fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"maybe"}`, teamID, orgID)},
		{"no team", `{"role":"viewer","scope_type":"organization","scope_id":1,"effect":"allow"}`},
		// The FAILURE_PATTERNS #99 row: a zero at a real scope matched every
		// request naming nothing at that level.
		{"zero id at a real scope", fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"device","scope_id":0,"effect":"allow"}`, teamID)},
		{"system scope carrying an id", fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","scope_id":7,"effect":"allow"}`, teamID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, router, http.MethodPost, "/api/v1/bindings", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestAccess_BindingCarriesItsProvenance proves the response says where a
// grant sits in words, which is the difference between a permissions table
// somebody can audit and one they can only read.
func TestAccess_BindingCarriesItsProvenance(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))

	systemID := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))
	scopedID := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"deny"}`, teamID, orgID))

	var b struct {
		ScopeID   *int   `json:"scope_id"`
		GrantedAt string `json:"granted_at"`
	}

	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/bindings/%d", systemID), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	// Omitted rather than zero: a system grant names no target, and a 0
	// would suggest a record with that id.
	if b.ScopeID != nil {
		t.Errorf("a system grant carried scope_id %d", *b.ScopeID)
	}
	if !strings.Contains(b.GrantedAt, "system") {
		t.Errorf("granted_at = %q, want it to say the grant is everywhere", b.GrantedAt)
	}

	rec = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/bindings/%d", scopedID), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if b.ScopeID == nil || *b.ScopeID != orgID {
		t.Errorf("scope_id = %v, want %d", b.ScopeID, orgID)
	}
	if !strings.Contains(b.GrantedAt, "organization") {
		t.Errorf("granted_at = %q, want it to name the scope type", b.GrantedAt)
	}
}

// TestAccess_LastSystemGrantCannotBeRemoved is the lockout guard, reported
// as a 409 with a message that explains itself. An operator who just tried
// to delete their own last key needs to know that is what they did.
func TestAccess_LastSystemGrantCannotBeRemoved(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	bindingID := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))

	rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/bindings/%d", bindingID), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "administer") {
		t.Errorf("the refusal does not explain itself: %s", rec.Body.String())
	}

	// Editing it into a deny is the same outcome by another route.
	rec = doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/bindings/%d", bindingID),
		`{"role":"admin","effect":"deny"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("editing the last grant into a deny: status = %d, want 409", rec.Code)
	}
}

// TestAccess_BindingUpdateCannotRepointAGrant pins the deliberate narrowing:
// re-pointing is revoke-plus-issue, and an audit trail showing a grant
// quietly changing what it covers is one nobody can reconstruct.
func TestAccess_BindingUpdateCannotRepointAGrant(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))
	scopedID := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"allow"}`, teamID, orgID))

	body := fmt.Sprintf(`{"team":9999,"role":"admin","scope_type":"device","scope_id":4242,"effect":"allow"}`)
	if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/bindings/%d", scopedID), body); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/bindings/%d", scopedID), "")
	var got struct {
		Team      int    `json:"team"`
		Role      string `json:"role"`
		ScopeType string `json:"scope_type"`
		ScopeID   *int   `json:"scope_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Role != "admin" {
		t.Errorf("role = %q, want the update to have landed", got.Role)
	}
	if got.Team != teamID || got.ScopeType != "organization" || got.ScopeID == nil || *got.ScopeID != orgID {
		t.Errorf("the grant was re-pointed to team %d, %s %v", got.Team, got.ScopeType, got.ScopeID)
	}
}

// TestAccess_BindingsFilterByTeamAndScope covers the two questions an
// auditor actually asks first.
func TestAccess_BindingsFilterByTeamAndScope(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	netops := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	secops := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"secops","organization":%d}`, orgID))

	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, netops))
	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"allow"}`, secops, orgID))

	count := func(target string) int {
		t.Helper()
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d", target, rec.Code)
		}
		var list struct {
			Bindings []json.RawMessage `json:"bindings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		return len(list.Bindings)
	}

	if n := count("/api/v1/bindings"); n != 2 {
		t.Errorf("unfiltered listing returned %d, want 2", n)
	}
	if n := count(fmt.Sprintf("/api/v1/bindings?team=%d", secops)); n != 1 {
		t.Errorf("team filter returned %d, want 1", n)
	}
	if n := count("/api/v1/bindings?scope_type=system"); n != 1 {
		t.Errorf("scope filter returned %d, want 1", n)
	}
}

// TestNewAccessHandler_NilLoggerFallsBack proves the constructor's guard, so
// a caller that omits a logger gets the process default rather than a nil
// dereference on the first store failure.
func TestNewAccessHandler_NilLoggerFallsBack(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:apiaccessnil?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	h := api.NewAccessHandler(access.NewEntStore(client), nil)

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.ListOrganizations.Route(h.ListOrganizations)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	_ = client.Close()

	if rec := doJSON(t, router, http.MethodGet, "/api/v1/organizations", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestAccess_BoundaryChecksAreUniformAcrossCollections walks the same four
// refusals on every collection.
//
// Uniformity is the property worth asserting. Four collections that each
// validated their own path parameter would eventually disagree about what a
// bad id is, and the one that drifted would be the one nobody tested.
func TestAccess_BoundaryChecksAreUniformAcrossCollections(t *testing.T) {
	router, _ := accessRouter(t)

	for _, collection := range []string{"organizations", "teams", "users", "bindings"} {
		base := "/api/v1/" + collection
		t.Run(collection, func(t *testing.T) {
			for _, tc := range []struct {
				name, method, target, body string
				want                       int
			}{
				{"get with a non numeric id", http.MethodGet, base + "/abc", "", http.StatusBadRequest},
				{"get with a zero id", http.MethodGet, base + "/0", "", http.StatusBadRequest},
				{"patch with a non numeric id", http.MethodPatch, base + "/abc", `{}`, http.StatusBadRequest},
				{"delete with a non numeric id", http.MethodDelete, base + "/abc", "", http.StatusBadRequest},
				{"get a missing record", http.MethodGet, base + "/4242", "", http.StatusNotFound},
				{"patch a missing record", http.MethodPatch, base + "/4242", `{}`, http.StatusNotFound},
				{"delete a missing record", http.MethodDelete, base + "/4242", "", http.StatusNotFound},
				{"post malformed json", http.MethodPost, base, `{"name":`, http.StatusBadRequest},
				{"patch malformed json", http.MethodPatch, base + "/1", `{"name":`, http.StatusBadRequest},
				{"bad paging cursor", http.MethodGet, base + "?after=nope", "", http.StatusBadRequest},
				{"bad page size", http.MethodGet, base + "?limit=nope", "", http.StatusBadRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					rec := doJSON(t, router, tc.method, tc.target, tc.body)
					if rec.Code != tc.want {
						t.Errorf("%s %s: status = %d, want %d: %s",
							tc.method, tc.target, rec.Code, tc.want, rec.Body.String())
					}
				})
			}
		})
	}
}

// TestAccess_DeleteTeamRevokesItsGrants proves the API path does what the
// store promises: a deleted team leaves no rows granting access to a
// principal that no longer exists.
func TestAccess_DeleteTeamRevokesItsGrants(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	keeper := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"keeper","organization":%d}`, orgID))
	doomed := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"doomed","organization":%d}`, orgID))

	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, keeper))
	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"allow"}`, doomed, orgID))

	if rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/teams/%d", doomed), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete team: status = %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, router, http.MethodGet, "/api/v1/bindings", "")
	var list struct {
		Bindings []struct {
			Team int `json:"team"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, b := range list.Bindings {
		if b.Team == doomed {
			t.Error("a deleted team still holds a grant, which is an orphaned permission")
		}
	}
	if len(list.Bindings) != 1 {
		t.Errorf("%d bindings survive, want only the keeper's", len(list.Bindings))
	}
}

// TestAccess_TeamRequiresAnOrganization refuses the row whose grants would
// resolve against no organization scope at all.
func TestAccess_TeamRequiresAnOrganization(t *testing.T) {
	router, _ := accessRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/teams", `{"name":"orphan","organization":0}`)
	if rec.Code == http.StatusCreated {
		t.Fatal("a team with no organization was accepted")
	}
}

// TestAccess_InvalidUserEmailIsRefused proves the join key against a token's
// subject cannot be set to something no subject could ever match.
func TestAccess_InvalidUserEmailIsRefused(t *testing.T) {
	router, _ := accessRouter(t)

	for _, body := range []string{`{"email":""}`, `{"email":"   "}`, `{"email":"not-an-address"}`} {
		if rec := doJSON(t, router, http.MethodPost, "/api/v1/users", body); rec.Code == http.StatusCreated {
			t.Errorf("POST %s was accepted", body)
		}
	}
}

// TestAccess_ValidPagingParametersReachTheStore proves the parsed values are
// used rather than validated and dropped, which is a real failure shape: the
// handler would still answer 200 with a full, unpaged result set.
func TestAccess_ValidPagingParametersReachTheStore(t *testing.T) {
	router, _ := accessRouter(t)
	for i := range 4 {
		createdID(t, router, "/api/v1/organizations", fmt.Sprintf(`{"name":"org-%d"}`, i))
	}

	count := func(target string) int {
		t.Helper()
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d", target, rec.Code)
		}
		var list struct {
			Organizations []json.RawMessage `json:"organizations"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		return len(list.Organizations)
	}

	if n := count("/api/v1/organizations?limit=2"); n != 2 {
		t.Errorf("limit=2 returned %d records", n)
	}
	if n := count("/api/v1/organizations?after=2"); n != 2 {
		t.Errorf("after=2 returned %d records, want the 2 beyond the cursor", n)
	}
	if n := count("/api/v1/users?limit=1&after=0"); n != 0 {
		t.Errorf("paged users returned %d, want 0", n)
	}
}

// TestAccess_BindingDeleteAndBadFilter covers the revoke path and the
// refusal of a filter that is not a team id.
func TestAccess_BindingDeleteAndBadFilter(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))
	scoped := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"device","scope_id":9,"effect":"deny"}`, teamID))

	// A scoped grant is not load-bearing for administration, so revoking it
	// is allowed where revoking the last system grant is not.
	if rec := doJSON(t, router, http.MethodDelete, fmt.Sprintf("/api/v1/bindings/%d", scoped), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d: %s", rec.Code, rec.Body.String())
	}

	for _, bad := range []string{"?team=abc", "?team=0", "?team=-1"} {
		rec := doJSON(t, router, http.MethodGet, "/api/v1/bindings"+bad, "")
		if rec.Code == http.StatusOK {
			t.Errorf("GET /bindings%s was accepted", bad)
		}
	}
}

// readFailingStore wraps a real store and fails reads once a threshold is
// passed, so the branch where a write lands and the read back to render it
// does not can be reached at all.
//
// That branch matters: answering 200 with the pre-edit record would tell a
// caller their change did not apply when it did.
type readFailingStore struct {
	access.Store
	reads    int
	failFrom int
}

func (s *readFailingStore) fail() bool {
	s.reads++
	return s.reads > s.failFrom
}

func (s *readFailingStore) GetOrganization(ctx context.Context, id int) (access.Organization, error) {
	if s.fail() {
		return access.Organization{}, errors.New("deliberate re-read failure")
	}
	return s.Store.GetOrganization(ctx, id)
}

func (s *readFailingStore) GetTeam(ctx context.Context, id int) (access.Team, error) {
	if s.fail() {
		return access.Team{}, errors.New("deliberate re-read failure")
	}
	return s.Store.GetTeam(ctx, id)
}

func (s *readFailingStore) GetUser(ctx context.Context, id int) (access.User, error) {
	if s.fail() {
		return access.User{}, errors.New("deliberate re-read failure")
	}
	return s.Store.GetUser(ctx, id)
}

func (s *readFailingStore) GetBinding(ctx context.Context, id int) (access.Binding, error) {
	if s.fail() {
		return access.Binding{}, errors.New("deliberate re-read failure")
	}
	return s.Store.GetBinding(ctx, id)
}

// TestAccess_ReReadFailureAfterAWriteIsAServerError walks the four update
// handlers with a store that fails the read back.
func TestAccess_ReReadFailureAfterAWriteIsAServerError(t *testing.T) {
	seed, store := accessRouter(t)
	orgID := createdID(t, seed, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, seed, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	userID := createdID(t, seed, "/api/v1/users", `{"email":"operator@example.com"}`)
	bindingID := createdID(t, seed, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))

	for _, tc := range []struct {
		name, target, body string
		// failFrom is how many reads succeed before the failure. It counts
		// only the handler's own reads: the store's internal ones go
		// straight to the wrapped value, not back through this wrapper.
		// Team is the one handler that reads twice, once to carry the
		// organization forward and once to render.
		failFrom int
	}{
		{"organization", fmt.Sprintf("/api/v1/organizations/%d", orgID), `{"name":"renamed"}`, 0},
		{"team", fmt.Sprintf("/api/v1/teams/%d", teamID), `{"name":"renamed","organization":1}`, 1},
		{"user", fmt.Sprintf("/api/v1/users/%d", userID), `{"email":"renamed@example.com"}`, 0},
		{"binding", fmt.Sprintf("/api/v1/bindings/%d", bindingID), `{"role":"operator","effect":"allow"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failing := &readFailingStore{Store: store, failFrom: tc.failFrom}
			h := api.NewAccessHandler(failing, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			router, err := api.NewRouter(api.RouterConfig{
				Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
				Auth:      alwaysAuthenticated,
				Admission: &fakeAdmitter{},
				HATEOAS:   allowAllGenerator(t),
				Routes: []api.Route{
					apispec.UpdateOrganization.Route(h.UpdateOrganization),
					apispec.UpdateTeam.Route(h.UpdateTeam),
					apispec.UpdateUser.Route(h.UpdateUser),
					apispec.UpdateBinding.Route(h.UpdateBinding),
				},
			})
			if err != nil {
				t.Fatalf("NewRouter: %v", err)
			}

			rec := doJSON(t, router, http.MethodPatch, tc.target, tc.body)
			if rec.Code != http.StatusInternalServerError && rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want a failure rather than a stale 200: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "deliberate") {
				t.Error("the store's error text leaked into the response")
			}
		})
	}
}

// TestAccess_PatchOmittingMembershipLeavesItAlone is the regression test for
// a bug an adversarial reviewer found in this code.
//
// PATCH means partial. A plain []int decodes an absent key and an explicit
// [] identically, so renaming a team through the API silently removed every
// member from it. The store's replace-wholesale semantics are right at that
// layer, because an explicit empty list has to be able to clear a
// membership; what was missing was the HTTP layer distinguishing "the caller
// said nothing" from "the caller said none".
func TestAccess_PatchOmittingMembershipLeavesItAlone(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	first := createdID(t, router, "/api/v1/users", `{"email":"a@example.com"}`)
	second := createdID(t, router, "/api/v1/users", `{"email":"b@example.com"}`)
	teamID := createdID(t, router, "/api/v1/teams",
		fmt.Sprintf(`{"name":"netops","organization":%d,"users":[%d,%d]}`, orgID, first, second))

	members := func() []int {
		t.Helper()
		rec := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/teams/%d", teamID), "")
		var got struct {
			Users *[]int `json:"users"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if got.Users == nil {
			t.Fatal("membership rendered as an absent key")
		}
		return *got.Users
	}

	t.Run("omitting the key preserves membership", func(t *testing.T) {
		if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/teams/%d", teamID), `{"name":"renamed"}`); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if got := members(); len(got) != 2 {
			t.Errorf("membership = %v after a PATCH that never mentioned it, want both members", got)
		}
	})

	t.Run("an explicit empty list still clears it", func(t *testing.T) {
		body := `{"name":"renamed","users":[]}`
		if rec := doJSON(t, router, http.MethodPatch, fmt.Sprintf("/api/v1/teams/%d", teamID), body); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if got := members(); len(got) != 0 {
			t.Errorf("membership = %v after an explicit empty list, want it cleared", got)
		}
	})
}

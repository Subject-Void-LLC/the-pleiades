// This file holds the partial-update rules of the four access PATCH routes
// (organizations, teams, contacts, role bindings) through the real router
// and the real ent-backed store, as the rest of the access suite does.
//
// Each route used to decode its create body, so a field the body left out
// was written back empty: a PATCH naming only an organization's name
// unfroze it and erased its change-management fields (FAILURE_PATTERNS
// 1075). These tests hold the four to the template PATCH's rules: a field
// left out keeps its stored value, a null is refused by name, an unknown key
// or a body that is not an object is malformed, a fixed field is refused
// when it differs and accepted as an echo, and the merged record is still
// validated whole.
package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// readRecord GETs target and returns the decoded body, failing on a non-200.
func readRecord(t *testing.T, router http.Handler, target string) map[string]any {
	t.Helper()
	rec := doJSON(t, router, http.MethodGet, target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d: %s", target, rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", target, err)
	}
	return got
}

// patchOK sends a PATCH that must succeed.
func patchOK(t *testing.T, router http.Handler, target, body string) {
	t.Helper()
	if rec := doJSON(t, router, http.MethodPatch, target, body); rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s %s: status = %d, want 200: %s", target, body, rec.Code, rec.Body.String())
	}
}

// assertRefusedUnchanged sends a PATCH that must be refused with want, with a
// response naming each of mentions, and leave the record exactly as it was.
func assertRefusedUnchanged(t *testing.T, router http.Handler, target, body string, want int, mentions ...string) {
	t.Helper()
	before := readRecord(t, router, target)
	rec := doJSON(t, router, http.MethodPatch, target, body)
	if rec.Code != want {
		t.Fatalf("PATCH %s %s: status = %d, want %d: %s", target, body, rec.Code, want, rec.Body.String())
	}
	for _, mention := range mentions {
		if !strings.Contains(rec.Body.String(), mention) {
			t.Errorf("PATCH %s %s: the refusal %s does not say %q", target, body, rec.Body.String(), mention)
		}
	}
	if after := readRecord(t, router, target); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused PATCH changed the record:\n before %v\n after  %v", before, after)
	}
}

// assertFields fails for each key of want whose value in got differs.
func assertFields(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
}

// fullOrganization is a create body setting every organization field.
const fullOrganization = `{
	"name":"acme",
	"description":"Retail payments platform",
	"classification":"secret",
	"change_window":"Sat 02:00-06:00 UTC",
	"frozen":true,
	"freeze_reason":"peak trading",
	"cost_centre":"CC-4417",
	"ticket_key":"ACME",
	"cmdb_id":"ci-90210"
}`

// TestAccessPatch_OrganizationKeepsWhatItDoesNotName is FAILURE_PATTERNS
// 1075: a PATCH naming only the name used to unfreeze the organization and
// erase its six change-management fields.
func TestAccessPatch_OrganizationKeepsWhatItDoesNotName(t *testing.T) {
	router, _ := accessRouter(t)
	target := fmt.Sprintf("/api/v1/organizations/%d", createdID(t, router, "/api/v1/organizations", fullOrganization))

	patchOK(t, router, target, `{"name":"acme-renamed"}`)
	assertFields(t, readRecord(t, router, target), map[string]any{
		"name":           "acme-renamed",
		"description":    "Retail payments platform",
		"classification": "secret",
		"change_window":  "Sat 02:00-06:00 UTC",
		"frozen":         true,
		"freeze_reason":  "peak trading",
		"cost_centre":    "CC-4417",
		"ticket_key":     "ACME",
		"cmdb_id":        "ci-90210",
	})

	// false is the explicit unfreeze, and it touches nothing else.
	patchOK(t, router, target, `{"frozen":false}`)
	assertFields(t, readRecord(t, router, target), map[string]any{
		"name": "acme-renamed", "frozen": false, "freeze_reason": "peak trading", "cmdb_id": "ci-90210",
	})

	// An empty value clears a field that can be empty. The read projection
	// omits an empty string, so a cleared field is an absent key.
	patchOK(t, router, target, `{"description":"","classification":""}`)
	got := readRecord(t, router, target)
	for _, key := range []string{"description", "classification"} {
		if value, present := got[key]; present {
			t.Errorf("%s = %#v after an empty value, want it cleared", key, value)
		}
	}
	assertFields(t, got, map[string]any{"change_window": "Sat 02:00-06:00 UTC", "ticket_key": "ACME"})
}

// TestAccessPatch_OrganizationIsValidatedWhole holds the merged record to
// the store's checks: a blank name, a bad marking and a taken name.
func TestAccessPatch_OrganizationIsValidatedWhole(t *testing.T) {
	router, _ := accessRouter(t)
	createdID(t, router, "/api/v1/organizations", `{"name":"globex"}`)
	target := fmt.Sprintf("/api/v1/organizations/%d", createdID(t, router, "/api/v1/organizations", fullOrganization))

	assertRefusedUnchanged(t, router, target, `{"name":"  "}`, http.StatusBadRequest)
	assertRefusedUnchanged(t, router, target, `{"classification":"staging"}`, http.StatusBadRequest)
	assertRefusedUnchanged(t, router, target, `{"name":"globex"}`, http.StatusConflict)
}

// TestAccessPatch_TeamKeepsWhatItDoesNotName covers the team's name and
// description, which a PATCH naming the other used to erase.
func TestAccessPatch_TeamKeepsWhatItDoesNotName(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	userID := createdID(t, router, "/api/v1/users", `{"email":"a@example.com"}`)
	target := fmt.Sprintf("/api/v1/teams/%d", createdID(t, router, "/api/v1/teams",
		fmt.Sprintf(`{"name":"netops","organization":%d,"description":"Owns the WAN","users":[%d]}`, orgID, userID)))

	patchOK(t, router, target, `{"name":"netops-renamed"}`)
	assertFields(t, readRecord(t, router, target), map[string]any{
		"name": "netops-renamed", "description": "Owns the WAN", "users": []any{float64(userID)},
	})

	patchOK(t, router, target, `{"description":"Owns the WAN and the LAN"}`)
	assertFields(t, readRecord(t, router, target), map[string]any{
		"name": "netops-renamed", "description": "Owns the WAN and the LAN",
	})

	patchOK(t, router, target, `{"description":""}`)
	if value, present := readRecord(t, router, target)["description"]; present {
		t.Errorf("description = %#v after an empty value, want it cleared", value)
	}

	assertRefusedUnchanged(t, router, target, `{"name":""}`, http.StatusBadRequest)
}

// TestAccessPatch_ContactKeepsWhatItDoesNotName covers a contact's six
// detail fields, of which a PATCH naming one used to erase the rest.
func TestAccessPatch_ContactKeepsWhatItDoesNotName(t *testing.T) {
	router, _ := accessRouter(t)
	orgID, _ := seedOwnedTeam(t, router)
	target := fmt.Sprintf("/api/v1/contacts/%d", createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Payments rota","role":"escalation","email":"pay@example.com","phone":"+1-555-0100",`+
			`"url":"https://example.com/rota","notes":"weekends","order":20,"organization":%d}`, orgID)))

	patchOK(t, router, target, `{"notes":"weekdays after six"}`)
	assertFields(t, readRecord(t, router, target), map[string]any{
		"name": "Payments rota", "role": "escalation", "email": "pay@example.com", "phone": "+1-555-0100",
		"url": "https://example.com/rota", "notes": "weekdays after six", "order": float64(20),
		"organization": float64(orgID),
	})

	// Clearing one channel of three is a valid contact.
	patchOK(t, router, target, `{"email":""}`)
	got := readRecord(t, router, target)
	if value, present := got["email"]; present {
		t.Errorf("email = %#v after an empty value, want it cleared", value)
	}
	assertFields(t, got, map[string]any{"phone": "+1-555-0100", "url": "https://example.com/rota"})
}

// TestAccessPatch_ContactIsValidatedWhole holds the merged contact to the
// one-channel rule: blanking the only way to reach somebody is refused, as a
// full update doing it would be.
func TestAccessPatch_ContactIsValidatedWhole(t *testing.T) {
	router, _ := accessRouter(t)
	orgID, _ := seedOwnedTeam(t, router)
	target := fmt.Sprintf("/api/v1/contacts/%d", createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Dana","role":"owner","email":"dana@example.com","organization":%d}`, orgID)))

	assertRefusedUnchanged(t, router, target, `{"email":""}`, http.StatusBadRequest)
	assertRefusedUnchanged(t, router, target, `{"role":"whoever"}`, http.StatusBadRequest)
}

// TestAccessPatch_BindingKeepsWhatItDoesNotName covers role and effect, of
// which a PATCH naming one used to send the other empty, refused as an
// unresolvable grant.
func TestAccessPatch_BindingKeepsWhatItDoesNotName(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	systemTarget := fmt.Sprintf("/api/v1/bindings/%d", createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID)))
	target := fmt.Sprintf("/api/v1/bindings/%d", createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"deny"}`, teamID, orgID)))

	patchOK(t, router, target, `{"role":"operator"}`)
	assertFields(t, readRecord(t, router, target), map[string]any{
		"role": "operator", "effect": "deny", "team": float64(teamID),
		"scope_type": "organization", "scope_id": float64(orgID),
	})

	patchOK(t, router, target, `{"effect":"allow"}`)
	assertFields(t, readRecord(t, router, target), map[string]any{"role": "operator", "effect": "allow"})

	// The merged grant is still held to the store's rules: an unknown role,
	// and the last system-scope allow edited into a deny.
	assertRefusedUnchanged(t, router, target, `{"role":"superuser"}`, http.StatusBadRequest)
	assertRefusedUnchanged(t, router, systemTarget, `{"effect":"deny"}`, http.StatusConflict, "administer")
}

// accessPatchCase is one access PATCH route with a record already created.
type accessPatchCase struct {
	name   string
	target string
	// fields is every field of the resource's PATCH body.
	fields []string
	// change is a valid body naming one field, and spare another, for the
	// one case where change names the field under test.
	change, spare string
}

// accessPatchCases creates one record of each access resource and returns
// how to PATCH it.
func accessPatchCases(t *testing.T, router http.Handler) []accessPatchCase {
	t.Helper()
	orgID := createdID(t, router, "/api/v1/organizations", fullOrganization)
	teamID := createdID(t, router, "/api/v1/teams",
		fmt.Sprintf(`{"name":"netops","organization":%d,"description":"Owns the WAN"}`, orgID))
	contactID := createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Dana","role":"owner","email":"dana@example.com","phone":"+1-555-0100","organization":%d}`, orgID))
	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))
	bindingID := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"allow"}`, teamID, orgID))

	return []accessPatchCase{
		{
			name:   "organization",
			target: fmt.Sprintf("/api/v1/organizations/%d", orgID),
			fields: []string{"name", "description", "classification", "change_window", "frozen",
				"freeze_reason", "cost_centre", "ticket_key", "cmdb_id"},
			change: `"description":"changed"`, spare: `"cmdb_id":"changed"`,
		},
		{
			name:   "team",
			target: fmt.Sprintf("/api/v1/teams/%d", teamID),
			fields: []string{"name", "description", "organization", "users"},
			change: `"description":"changed"`, spare: `"name":"changed"`,
		},
		{
			name:   "contact",
			target: fmt.Sprintf("/api/v1/contacts/%d", contactID),
			fields: []string{"name", "role", "email", "phone", "url", "notes", "order", "organization", "team"},
			change: `"notes":"changed"`, spare: `"name":"changed"`,
		},
		{
			name:   "binding",
			target: fmt.Sprintf("/api/v1/bindings/%d", bindingID),
			fields: []string{"team", "role", "scope_type", "scope_id", "effect"},
			change: `"role":"operator"`, spare: `"effect":"deny"`,
		},
	}
}

// TestAccessPatch_RefusesNull holds the null rule on every field of all four
// resources. Read as "clear" a null deletes what a client serializing an
// unset value as null never meant to touch; read as "keep" it silently drops
// a removal a JSON Merge Patch client asked for. Refused, it says which
// field, and the valid change beside it is not applied either.
func TestAccessPatch_RefusesNull(t *testing.T) {
	router, _ := accessRouter(t)
	for _, tc := range accessPatchCases(t, router) {
		for _, field := range tc.fields {
			t.Run(tc.name+"."+field, func(t *testing.T) {
				change := tc.change
				if strings.HasPrefix(change, `"`+field+`"`) {
					change = tc.spare
				}
				body := `{` + change + `,"` + field + `":null}`
				assertRefusedUnchanged(t, router, tc.target, body, http.StatusBadRequest,
					`\"`+field+`\"`, "may not be null")
			})
		}
	}
}

// TestAccessPatch_RefusesABodyThatIsNotAnObjectOfKnownFields holds the strict
// decode the custom unmarshalers repeat: decodeJSON's own
// DisallowUnknownFields does not reach inside one, and a bare null would
// otherwise read as an empty patch.
func TestAccessPatch_RefusesABodyThatIsNotAnObjectOfKnownFields(t *testing.T) {
	router, _ := accessRouter(t)
	for _, tc := range accessPatchCases(t, router) {
		for name, body := range map[string]string{
			"a bare null":               `null`,
			"an array":                  `[]`,
			"an unknown field":          `{` + tc.change + `,"owner":"ada"}`,
			"an attestation":            `{` + tc.change + `,"attested_by":"somebody-else@example.com"}`,
			"a field of the wrong type": `{` + tc.change + `,"` + tc.fields[0] + `":{"nested":true}}`,
		} {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				assertRefusedUnchanged(t, router, tc.target, body, http.StatusBadRequest, "malformed JSON body")
			})
		}
	}
}

// TestAccessPatch_FixedFieldsAreRefusedUnlessEchoed holds the fields no
// update can change: a team's organization, a contact's owner, and a grant's
// team and scope. A differing value used to be ignored with a 200, which told
// the caller the record was re-pointed when it was not; it is now refused,
// naming the field. The stored value is still accepted, so a client that
// sends back the create fields with one edited keeps working.
func TestAccessPatch_FixedFieldsAreRefusedUnlessEchoed(t *testing.T) {
	router, _ := accessRouter(t)
	orgID := createdID(t, router, "/api/v1/organizations", `{"name":"acme"}`)
	otherOrg := createdID(t, router, "/api/v1/organizations", `{"name":"globex"}`)
	teamID := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"netops","organization":%d}`, orgID))
	otherTeam := createdID(t, router, "/api/v1/teams", fmt.Sprintf(`{"name":"secops","organization":%d}`, orgID))
	contactID := createdID(t, router, "/api/v1/contacts", fmt.Sprintf(
		`{"name":"Dana","role":"owner","email":"dana@example.com","organization":%d}`, orgID))
	createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"admin","scope_type":"system","effect":"allow"}`, teamID))
	bindingID := createdID(t, router, "/api/v1/bindings",
		fmt.Sprintf(`{"team":%d,"role":"viewer","scope_type":"organization","scope_id":%d,"effect":"allow"}`, teamID, orgID))

	teamTarget := fmt.Sprintf("/api/v1/teams/%d", teamID)
	contactTarget := fmt.Sprintf("/api/v1/contacts/%d", contactID)
	bindingTarget := fmt.Sprintf("/api/v1/bindings/%d", bindingID)

	for _, tc := range []struct {
		name, target, body, field string
	}{
		{"team organization", teamTarget, fmt.Sprintf(`{"name":"renamed","organization":%d}`, otherOrg), "organization"},
		{"contact organization", contactTarget, fmt.Sprintf(`{"name":"renamed","organization":%d}`, otherOrg), "organization"},
		{"contact team", contactTarget, fmt.Sprintf(`{"name":"renamed","team":%d}`, teamID), "team"},
		{"binding team", bindingTarget, fmt.Sprintf(`{"role":"admin","team":%d}`, otherTeam), "team"},
		{"binding scope type", bindingTarget, `{"role":"admin","scope_type":"device"}`, "scope_type"},
		{"binding scope id", bindingTarget, fmt.Sprintf(`{"role":"admin","scope_id":%d}`, otherOrg), "scope_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRefusedUnchanged(t, router, tc.target, tc.body, http.StatusBadRequest,
				tc.field+" cannot be changed", "fixed")
		})
	}

	// An echo of every fixed field is accepted and the edit beside it lands.
	patchOK(t, router, teamTarget, fmt.Sprintf(`{"name":"renamed","organization":%d}`, orgID))
	assertFields(t, readRecord(t, router, teamTarget), map[string]any{"name": "renamed", "organization": float64(orgID)})

	patchOK(t, router, contactTarget, fmt.Sprintf(`{"name":"Dana Okafor","organization":%d,"team":0}`, orgID))
	assertFields(t, readRecord(t, router, contactTarget), map[string]any{"name": "Dana Okafor", "organization": float64(orgID)})

	patchOK(t, router, bindingTarget, fmt.Sprintf(
		`{"team":%d,"role":"operator","scope_type":"organization","scope_id":%d,"effect":"allow"}`, teamID, orgID))
	assertFields(t, readRecord(t, router, bindingTarget), map[string]any{
		"role": "operator", "team": float64(teamID), "scope_type": "organization", "scope_id": float64(orgID),
	})
}

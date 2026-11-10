// This file holds the credential type writes to the rule that a value
// written as secret is never shown as plain, through the real router and the
// real ent-backed store with the envelope hooks installed.
//
// The read path redacts from the type's CURRENT schema, so before this rule
// a type edit could expose every stored value of an input: clearing its
// secret flag did it in one request, and removing it and adding it back as
// non-secret did it in two, since a removal left the values behind in every
// credential. Both answered 200 and GET /credentials/{id} then showed the
// plaintext.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// patchTypeInputs is the patch type's input schema as a JSON document, with
// the backup input as given (its whole field object, or "" to leave it out).
func patchTypeInputs(backup string) string {
	fields := `{"id":"token","label":"Token","secret":true},`
	if backup != "" {
		fields += backup + `,`
	}
	fields += `{"id":"url","label":"URL"},{"id":"region","label":"Region","choices":["east","west"]}`
	return `{"fields":[` + fields + `],"required":["token"]}`
}

// The backup input as each test writes it back.
const (
	secretBackup = `{"id":"backup","label":"Backup token","secret":true}`
	plainBackup  = `{"id":"backup","label":"Backup token"}`
)

// assertBackupGone fails unless GET shows the credential with no backup
// value at all and no secret anywhere in the body, and the database holds no
// backup value either.
func (f *credentialFixture) assertBackupGone(t *testing.T, id int) {
	t.Helper()
	status, body := f.do(t, http.MethodGet, credentialPath(id), nil)
	assertNoSecret(t, "GET", body)
	if status != http.StatusOK || bytes.Contains(body, []byte(`"backup"`)) {
		t.Errorf("GET credential %d: %d %s, want no backup value shown", id, status, body)
	}
	if value, held := f.stored(t, id).Inputs["backup"]; held {
		t.Errorf("credential %d still stores a backup value (%d bytes)", id, len(value))
	}
}

// TestCredentialTypePatch_RefusesMakingASecretInputPlain is the one-request
// exposure: clearing an input's secret flag is refused, through PATCH and
// through PUT .../inputs, naming the input, and the stored value still reads
// as the marker.
func TestCredentialTypePatch_RefusesMakingASecretInputPlain(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	id := f.edgeCredential(t, typeID)

	f.credentialTypeRefusedUnchanged(t, typeID, `{"inputs":`+patchTypeInputs(plainBackup)+`}`,
		http.StatusBadRequest, `\"backup\"`, "remove it and add it again")

	status, resp := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credential-types/%d/inputs", typeID),
		json.RawMessage(`{"inputs":`+patchTypeInputs(plainBackup)+`}`))
	if status != http.StatusBadRequest || !strings.Contains(string(resp), `\"backup\"`) {
		t.Errorf("PUT .../inputs clearing secret: %d %s, want 400 naming backup", status, resp)
	}

	got := f.readCredential(t, id)
	if inputs := got["inputs"].(map[string]any); inputs["backup"] != redact.Marker {
		t.Errorf("GET shows backup as %v, want the marker", inputs["backup"])
	}
	if stored := f.stored(t, id).Inputs["backup"]; stored != patchBackupSecret {
		t.Error("a refused flip changed the stored backup")
	}
}

// TestCredentialTypePatch_RemovingAnInputDeletesWhatCredentialsHoldForIt is
// the two-request exposure: removing an input deletes the value, the
// external reference and the source binding every credential of the type
// holds for it, so adding it back as non-secret shows nothing. It also ends
// the state where a credential could no longer be edited once its type
// dropped an input it held a value for.
func TestCredentialTypePatch_RemovingAnInputDeletesWhatCredentialsHoldForIt(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	id := f.edgeCredential(t, typeID)

	// The backup input also reads through a source credential.
	source := f.createCredential(t, "vault login")
	if status, resp := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credentials/%d/input-sources", id), map[string]any{
		"input_sources": []map[string]any{{
			"input_id": "backup", "source_credential": source, "metadata": map[string]string{"source_field": "api_token"},
		}},
	}); status != http.StatusOK {
		t.Fatalf("binding backup to a source: %d %s", status, resp)
	}

	// Remove backup and url; the injector reading url goes with it.
	f.patchCredentialTypeOK(t, typeID, `{"inputs":{"fields":[{"id":"token","label":"Token","secret":true},
		{"id":"region","label":"Region","choices":["east","west"]}],"required":["token"]},
		"injectors":{"env":{"API_TOKEN":"{{ token }}"}}}`)
	stored := f.stored(t, id)
	if _, held := stored.Inputs["backup"]; held || stored.Inputs["token"] != patchTokenSecret {
		t.Errorf("after removing backup the credential stores %d inputs, want token kept and backup gone", len(stored.Inputs))
	}
	if len(stored.External) != 0 {
		t.Errorf("after removing backup the credential keeps external %v", stored.External)
	}
	if status, body := f.do(t, http.MethodGet, fmt.Sprintf("/api/v1/credentials/%d/input-sources", id), nil); status != http.StatusOK ||
		bytes.Contains(body, []byte(`"backup"`)) {
		t.Errorf("after removing backup its source binding remains: %d %s", status, body)
	}
	f.patchCredentialOK(t, id, `{"name":"edge api renamed"}`)

	// Adding it back, non-secret, shows nothing: there is nothing left.
	f.patchCredentialTypeOK(t, typeID, `{"inputs":`+patchTypeInputs(plainBackup)+`,
		"injectors":{"env":{"API_TOKEN":"{{ token }}","API_URL":"{{ url }}"}}}`)
	f.assertBackupGone(t, id)

	// The same through PUT .../inputs, twice.
	other := f.createPatchType(t, f.orgA, "patch_api_put")
	otherID := f.createPatchCredential(t, map[string]any{
		"name": "put api", "organization": f.orgA, "credential_type": other,
		"inputs": map[string]string{"token": patchRotatedSecret, "backup": patchBackupSecret},
	})
	for _, inputs := range []string{patchTypeInputs(""), patchTypeInputs(plainBackup)} {
		if status, resp := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credential-types/%d/inputs", other),
			json.RawMessage(`{"inputs":`+inputs+`}`)); status != http.StatusOK {
			t.Fatalf("PUT .../inputs: %d %s", status, resp)
		}
	}
	f.assertBackupGone(t, otherID)
}

// TestCredentialTypePatch_PurgesValuesAnEarlierRemovalLeftBehind covers a
// credential whose type dropped an input before this rule existed, seeded by
// editing the type row through the ent client as the old store did: the
// value stays behind, and a PATCH of the credential carries it, shown
// nowhere. The next write to the type, of any field, deletes it, unless that
// write adds the input back as secret, which keeps it and shows only the
// marker.
func TestCredentialTypePatch_PurgesValuesAnEarlierRemovalLeftBehind(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	ctx := context.Background()
	withoutBackup := credtype.InputSchema{
		Fields: []credtype.InputField{
			{ID: "token", Label: "Token", Secret: true},
			{ID: "url", Label: "URL"},
			{ID: "region", Label: "Region", Choices: []string{"east", "west"}},
		},
		Required: []string{"token"},
	}
	legacy := func(t *testing.T, namespace string) (int, int) {
		t.Helper()
		typeID := f.createPatchType(t, f.orgA, namespace)
		id := f.createPatchCredential(t, map[string]any{
			"name": "legacy " + namespace, "organization": f.orgA, "credential_type": typeID,
			"inputs": map[string]string{"token": patchTokenSecret, "backup": patchBackupSecret},
		})
		f.client.CredentialType.UpdateOneID(typeID).SetInputs(withoutBackup).ExecX(ctx)
		if f.stored(t, id).Inputs["backup"] != patchBackupSecret {
			t.Fatal("the seeded removal did not leave the value behind")
		}
		return typeID, id
	}

	t.Run("any type write deletes it", func(t *testing.T) {
		typeID, id := legacy(t, "legacy_patch")
		f.patchCredentialOK(t, id, `{"name":"renamed"}`)
		if f.stored(t, id).Inputs["backup"] != patchBackupSecret {
			t.Fatal("the PATCH did not carry the value the type no longer declares")
		}
		_, body := f.do(t, http.MethodGet, credentialPath(id), nil)
		assertNoSecret(t, "GET", body)
		f.patchCredentialTypeOK(t, typeID, `{"description":"cleaned"}`)
		if _, held := f.stored(t, id).Inputs["backup"]; held {
			t.Error("a type PATCH left the value an earlier removal left behind")
		}
		f.patchCredentialOK(t, id, `{"name":"renamed"}`)
	})
	t.Run("PUT .../injectors, which changes no secret flag, deletes it too", func(t *testing.T) {
		typeID, id := legacy(t, "legacy_injectors")
		if status, resp := f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credential-types/%d/injectors", typeID),
			json.RawMessage(`{"injectors":{"env":{"API_TOKEN":"{{ token }}"}}}`)); status != http.StatusOK {
			t.Fatalf("PUT .../injectors: %d %s", status, resp)
		}
		if _, held := f.stored(t, id).Inputs["backup"]; held {
			t.Error("PUT .../injectors left the value an earlier removal left behind")
		}
	})
	t.Run("adding the input back shows nothing", func(t *testing.T) {
		typeID, id := legacy(t, "legacy_readd")
		f.patchCredentialTypeOK(t, typeID, `{"inputs":`+patchTypeInputs(plainBackup)+`}`)
		f.assertBackupGone(t, id)
	})
	t.Run("adding it back as secret keeps it, shown only as the marker", func(t *testing.T) {
		// Safe to keep: it reads back only as the marker while backup is
		// secret, and backup can become plain only by a removal, which
		// deletes it, or a plain add, which deletes it too.
		typeID, id := legacy(t, "legacy_readd_secret")
		f.patchCredentialTypeOK(t, typeID, `{"inputs":`+patchTypeInputs(secretBackup)+`}`)
		status, body := f.do(t, http.MethodGet, credentialPath(id), nil)
		assertNoSecret(t, "GET", body)
		if inputs, _ := f.readCredential(t, id)["inputs"].(map[string]any); status != http.StatusOK || inputs["backup"] != redact.Marker {
			t.Errorf("GET shows backup as %v, want the marker", inputs["backup"])
		}
		if f.stored(t, id).Inputs["backup"] != patchBackupSecret {
			t.Error("adding backup back as secret deleted the value an earlier removal left")
		}
		f.patchCredentialOK(t, id, `{"name":"edited again"}`)
	})
}

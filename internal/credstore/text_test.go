// This file holds the credential writes to the text rules every writer
// meets, the web UI's forms among them: a blank name, which ent's NotEmpty
// validator answered with an unclassified error, and a NUL, which SQLite
// stores and PostgreSQL refuses.
package credstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// TestCredentialWritesRefuseABlankNameAndANUL drives CreateCredential and
// UpdateCredential with each bad value and expects a TextError naming the
// field, with nothing written.
func TestCredentialWritesRefuseABlankNameAndANUL(t *testing.T) {
	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)
	existing, err := store.CreateCredential(ctx, orgID, typeID, "kept", "as written", map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nul := string(rune(0))

	cases := []struct {
		name, field           string
		credName, description string
		external              map[string]string
	}{
		{"a blank name", "name", "", "", nil},
		{"a NUL in the name", "name", "new" + nul, "", nil},
		{"a NUL in the description", "description", "new", "x" + nul, nil},
		{"a NUL in a reference", "external", "new", "", map[string]string{"api_url": "file:x" + nul}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := func(op string, err error) {
				t.Helper()
				var text *credtype.TextError
				if !errors.As(err, &text) || text.Field != tc.field || !errors.Is(err, credtype.ErrInvalidCredential) {
					t.Errorf("%s = %v, want a TextError on %s that is ErrInvalidCredential", op, err, tc.field)
				}
			}
			_, err := store.CreateCredential(ctx, orgID, typeID, tc.credName, tc.description,
				map[string]string{"api_token": theSecret}, tc.external)
			check("CreateCredential()", err)
			_, err = store.UpdateCredential(ctx, existing.ID, tc.credName, tc.description, nil, tc.external)
			check("UpdateCredential()", err)
			if row := client.Credential.GetX(ctx, existing.ID); row.Name != "kept" || row.Description != "as written" {
				t.Errorf("a refused update changed the credential to %q, %q", row.Name, row.Description)
			}
		})
	}
}

// TestInputSourceMetadataRefusesANUL holds a binding's metadata, kept as
// jsonb, to the same rule: a NUL in a key or a value is refused, through
// SetCredentialInputSources and through WithInputSources on a create, with
// nothing written.
func TestInputSourceMetadataRefusesANUL(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, typeID, sourceID := sourceFixture(t)
	target, err := store.CreateCredential(ctx, orgID, typeID, "api", "", map[string]string{"api_token": "stored"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nul := string(rune(0))

	for name, metadata := range map[string]map[string]string{
		"a value": {"path": "secret/data/p" + nul},
		"a key":   {"path" + nul: "secret/data/p"},
	} {
		t.Run(name, func(t *testing.T) {
			binding := []credstore.InputSourceBinding{{InputID: "api_token", SourceCredentialID: sourceID, Metadata: metadata}}
			check := func(op string, err error) {
				t.Helper()
				var text *credtype.TextError
				if !errors.As(err, &text) || text.Field != "metadata" || !errors.Is(err, credtype.ErrInvalidCredential) {
					t.Errorf("%s = %v, want a TextError on metadata that is ErrInvalidCredential", op, err)
				}
			}
			_, err := store.SetCredentialInputSources(ctx, target.ID, binding)
			check("SetCredentialInputSources()", err)
			_, err = store.CreateCredential(ctx, orgID, typeID, "bound "+name, "", nil, nil, credstore.WithInputSources(binding))
			check("CreateCredential(WithInputSources)", err)
			if sources, _ := store.ListCredentialInputSources(ctx, target.ID); len(sources) != 0 {
				t.Errorf("a refused binding was stored: %v", sources)
			}
		})
	}
}

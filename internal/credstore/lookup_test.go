package credstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The lookup and not-found paths. Split from ent_store_test.go, which
// covers the security properties, because these are ordinary CRUD
// behaviors and mixing the two makes it harder to see which assertions are
// load bearing.

// TestGetType covers the id and namespace lookups.
//
// The namespace one is the interesting half: it is what an AWX import keys
// on to decide whether a type already exists, so a broken lookup there
// means every import creates duplicates rather than updating.
func TestGetType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, _, typeID := fixture(t)

	byID, err := store.GetType(ctx, typeID)
	if err != nil {
		t.Fatalf("GetType() error = %v", err)
	}
	if byID.Name != "Custom API" || byID.Namespace != "custom_api" {
		t.Errorf("GetType() = %+v", byID)
	}
	if byID.ID != typeID {
		t.Errorf("GetType().ID = %d, want %d", byID.ID, typeID)
	}
	if byID.OrganizationID == 0 {
		t.Error("GetType() lost the owning organization, which a custom type always has")
	}

	byNamespace, err := store.GetTypeByNamespace(ctx, "custom_api")
	if err != nil {
		t.Fatalf("GetTypeByNamespace() error = %v", err)
	}
	if byNamespace.ID != typeID {
		t.Errorf("GetTypeByNamespace() returned a different row: %d, want %d", byNamespace.ID, typeID)
	}
}

// TestNotFoundIsASentinel covers every lookup's missing-record path, so a
// caller can distinguish "no such thing" from "the database is broken" and
// answer 404 rather than 500.
func TestNotFoundIsASentinel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, orgID, _ := fixture(t)

	const missing = 999999

	tests := map[string]func() error{
		"GetType": func() error {
			_, err := store.GetType(ctx, missing)
			return err
		},
		"GetTypeByNamespace": func() error {
			_, err := store.GetTypeByNamespace(ctx, "no_such_namespace")
			return err
		},
		"GetCredential": func() error {
			_, err := store.GetCredential(ctx, missing)
			return err
		},
		"UpdateType": func() error {
			_, err := store.UpdateType(ctx, missing, credtype.CredentialType{})
			return err
		},
		"DeleteType": func() error { return store.DeleteType(ctx, missing) },
		"DeleteCredential": func() error {
			return store.DeleteCredential(ctx, missing)
		},
		"UpdateCredential": func() error {
			_, err := store.UpdateCredential(ctx, missing, "x", "", nil, nil)
			return err
		},
		"SetTemplateCredentials": func() error {
			return store.SetTemplateCredentials(ctx, missing, nil)
		},
		"CreateCredential": func() error {
			_, err := store.CreateCredential(ctx, orgID, missing, "x", "", nil, nil)
			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := call()
			if err == nil {
				t.Fatalf("%s on a missing record succeeded", name)
			}
			if !errors.Is(err, credstore.ErrNotFound) {
				t.Errorf("%s gave %v, want one matching ErrNotFound so a caller can answer 404", name, err)
			}
		})
	}
}

// TestDuplicateNamesAndNamespacesAreRefused covers the uniqueness
// constraints, translated into this package's own sentinel so a handler can
// answer 409 rather than 500.
func TestDuplicateNamesAndNamespacesAreRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, orgID, typeID := fixture(t)

	// A second type with the same namespace.
	_, err := store.CreateType(ctx, orgID, credtype.CredentialType{
		Name:      "Another Name",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "token", Label: "Token"}}},
	})
	if !errors.Is(err, credstore.ErrExists) {
		t.Errorf("CreateType() with a duplicate namespace gave %v, want ErrExists", err)
	}

	// A second credential with the same name in the same organization.
	if _, err := store.CreateCredential(ctx, orgID, typeID, "same name", "",
		map[string]string{"api_token": "a-token-value"}, nil); err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	_, err = store.CreateCredential(ctx, orgID, typeID, "same name", "",
		map[string]string{"api_token": "b-token-value"}, nil)
	if !errors.Is(err, credstore.ErrExists) {
		t.Errorf("CreateCredential() with a duplicate name gave %v, want ErrExists", err)
	}
}

// TestUpdateTypeKeepsTheImmutableNamespace covers the deliberate choice to
// ignore a caller's namespace on update rather than refuse the request.
//
// The column is immutable in the schema. Refusing an update that merely
// carried the field would fail on something the caller was never allowed to
// change, which is a confusing error for a form that round-trips every
// field it rendered.
func TestUpdateTypeKeepsTheImmutableNamespace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, _, typeID := fixture(t)

	updated, err := store.UpdateType(ctx, typeID, credtype.CredentialType{
		Name:      "Renamed",
		Kind:      credtype.KindCloud,
		Namespace: "a_completely_different_namespace",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "api_token", Label: "Token", Secret: true}}},
		Injectors: credtype.Injectors{Env: map[string]string{"T": "{{ api_token }}"}},
	})
	if err != nil {
		t.Fatalf("UpdateType() error = %v", err)
	}
	if updated.Namespace != "custom_api" {
		t.Errorf("Namespace = %q, want the immutable stored one", updated.Namespace)
	}
	if updated.Name != "Renamed" {
		t.Errorf("Name = %q, want the update applied", updated.Name)
	}
}

// TestEnsureManagedTypeRefusesANamespaceHeldByACustomType covers the
// collision that would otherwise silently replace something an operator
// wrote with something this platform ships.
func TestEnsureManagedTypeRefusesANamespaceHeldByACustomType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, _, _ := fixture(t)

	_, err := store.EnsureManagedType(ctx, credtype.CredentialType{
		Name:      "Custom API",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "token", Label: "Token"}}},
	})
	if !errors.Is(err, credstore.ErrExists) {
		t.Errorf("EnsureManagedType() over a custom namespace gave %v, want ErrExists", err)
	}
}

// TestNewEntStoreRefusesIncompleteWiring pins the two panics.
//
// A store built without a render engine would silently skip injector
// validation, accepting credential types that cannot render, and the
// failure would surface at somebody's launch rather than at startup.
func TestNewEntStoreRefusesIncompleteWiring(t *testing.T) {
	t.Parallel()

	assertPanics(t, "a nil client", func() { credstore.NewEntStore(nil, render.New()) })

	_, client, _, _ := fixture(t)
	assertPanics(t, "a nil render engine", func() { credstore.NewEntStore(client, nil) })
}

// assertPanics fails unless fn panics.
func assertPanics(t *testing.T, what string, fn func()) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Errorf("NewEntStore accepted %s, which should fail at process start rather than at the first request", what)
		}
	}()
	fn()
}

// TestListTypesIsScopedToTheTenant covers the filter, which a broken
// version would turn into a cross-tenant disclosure of every custom type's
// name and injector document.
func TestListTypesIsScopedToTheTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, _ := fixture(t)

	other := client.Organization.Create().SetName("other").SaveX(ctx)
	if _, err := store.CreateType(ctx, other.ID, credtype.CredentialType{
		Name:      "Their Type",
		Kind:      credtype.KindCloud,
		Namespace: "their_type",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "token", Label: "Token"}}},
	}); err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	mine, err := store.ListTypes(ctx, orgID)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	for _, ct := range mine {
		if ct.Namespace == "their_type" {
			t.Error("ListTypes() returned another tenant's custom credential type")
		}
	}
}

// TestListCredentialsIsScopedToTheTenant is the same check for credentials,
// where the disclosure would be larger: names, types and external secret
// references.
func TestListCredentialsIsScopedToTheTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	if _, err := store.CreateCredential(ctx, orgID, typeID, "mine", "",
		map[string]string{"api_token": "a-token-value"}, nil); err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	other := client.Organization.Create().SetName("other").SaveX(ctx)
	listed, err := store.ListCredentials(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListCredentials() error = %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("ListCredentials() returned %d of another tenant's credentials", len(listed))
	}
}

// TestUnbindingRemovesEveryBinding covers replacing a template's bindings
// with an empty set, which is how a template stops running as anything.
func TestUnbindingRemovesEveryBinding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	cred, err := store.CreateCredential(ctx, orgID, typeID, "bound", "",
		map[string]string{"api_token": "a-token-value"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)

	if err := store.SetTemplateCredentials(ctx, tmpl.ID, []int{cred.ID}); err != nil {
		t.Fatalf("SetTemplateCredentials() error = %v", err)
	}
	if err := store.SetTemplateCredentials(ctx, tmpl.ID, nil); err != nil {
		t.Fatalf("SetTemplateCredentials() with an empty set error = %v", err)
	}

	bound, err := store.TemplateCredentials(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("TemplateCredentials() error = %v", err)
	}
	if len(bound) != 0 {
		t.Errorf("the template still binds %d credential(s)", len(bound))
	}

	// The credential itself survives: unbinding is not deleting.
	if _, err := store.GetCredential(ctx, cred.ID); err != nil {
		t.Errorf("unbinding deleted the credential: %v", err)
	}
}

// TestDeletingACredentialRemovesItsBindings covers the cascade on the join
// rows, which is what lets a compromised credential be removed during an
// incident without editing every template that used it first.
func TestDeletingACredentialRemovesItsBindings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	cred, err := store.CreateCredential(ctx, orgID, typeID, "compromised", "",
		map[string]string{"api_token": "a-token-value"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)
	if err := store.SetTemplateCredentials(ctx, tmpl.ID, []int{cred.ID}); err != nil {
		t.Fatalf("SetTemplateCredentials() error = %v", err)
	}

	if err := store.DeleteCredential(ctx, cred.ID); err != nil {
		t.Fatalf("DeleteCredential() error = %v", err)
	}

	// The template survives, holding nothing.
	if _, err := client.Template.Get(ctx, tmpl.ID); err != nil {
		t.Fatalf("deleting a credential took its template with it: %v", err)
	}
	bound, err := store.TemplateCredentials(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("TemplateCredentials() error = %v", err)
	}
	if len(bound) != 0 {
		t.Errorf("the template still binds a deleted credential")
	}
}

// TestCredentialsWithExternalReferences covers the external half of the
// store, which is the pointer-rather-than-secret path.
//
// The headline case is the one Phase 22b found broken: a credential whose
// REQUIRED input lives in an external source has no stored value for it,
// and refusing that would make an externally-sourced credential impossible
// to create at all.
func TestCredentialsWithExternalReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, orgID, typeID := fixture(t)

	t.Run("a required input satisfied by an external reference is accepted", func(t *testing.T) {
		created, err := store.CreateCredential(ctx, orgID, typeID, "external api", "",
			nil, map[string]string{"api_token": "file:prod_api_token"})
		if err != nil {
			t.Fatalf("CreateCredential() refused an externally-sourced credential: %v", err)
		}
		// The reference reads back UNREDACTED, deliberately: a Vault path
		// is a pointer to a secret rather than a secret, and hiding it
		// would make "which credentials point at this mount" unanswerable
		// during a migration.
		if created.External["api_token"] != "file:prod_api_token" {
			t.Errorf("External = %v, want the reference readable", created.External)
		}
		// And nothing invented a stored value for it.
		if _, stored := created.Inputs["api_token"]; stored {
			t.Errorf("Inputs = %v, want no stored value for an externally-sourced input", created.Inputs)
		}
	})

	t.Run("an external reference to an undeclared input is refused", func(t *testing.T) {
		_, err := store.CreateCredential(ctx, orgID, typeID, "typo api", "",
			map[string]string{"api_token": "a-real-secret"},
			map[string]string{"nonexistent": "file:something"})
		if err == nil {
			t.Fatal("CreateCredential() accepted an external reference to an undeclared input")
		}
		if !strings.Contains(err.Error(), "nonexistent") {
			t.Errorf("the error does not name the undeclared input: %v", err)
		}
	})

	t.Run("an update may move an input from stored to external", func(t *testing.T) {
		created, err := store.CreateCredential(ctx, orgID, typeID, "moving api", "",
			map[string]string{"api_token": "a-real-secret"}, nil)
		if err != nil {
			t.Fatalf("CreateCredential() error = %v", err)
		}

		updated, err := store.UpdateCredential(ctx, created.ID, "moving api", "",
			nil, map[string]string{"api_token": "file:prod_api_token"})
		if err != nil {
			t.Fatalf("UpdateCredential() error = %v", err)
		}
		if updated.External["api_token"] != "file:prod_api_token" {
			t.Errorf("External = %v, want the reference recorded", updated.External)
		}
	})
}

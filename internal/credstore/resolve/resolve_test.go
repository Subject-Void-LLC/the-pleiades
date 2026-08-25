package resolve_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	_ "github.com/mattn/go-sqlite3"
)

// theSecret is the value these tests follow. Unlike the store's tests,
// which assert it is ABSENT, these assert it is present: this package is
// the one place it is supposed to be.
const theSecret = "sk-live-CANARY-resolver-9f8e7d6c"

// fixture opens a real database with the encryption hooks registered and
// seeds a credential.
func fixture(t testing.TB) (resolve.Resolver, credstore.Store, *ent.Client, int, int) {
	t.Helper()

	ctx := context.Background()
	client, err := ent.OpenEmbedded(ctx, filepath.Join(t.TempDir(), "resolve_test.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	store := credstore.NewEntStore(client, render.New())

	ct, err := store.CreateType(ctx, org.ID, credtype.CredentialType{
		Name:      "Custom API",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "region", Label: "Region", Default: "us-east-1"},
			},
			Required: []string{"api_token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	return resolve.NewEntResolver(client), store, client, org.ID, ct.ID
}

// TestResolveReturnsRealValues is the whole point of this package. The
// store cannot do this, deliberately; if this stopped working, no
// credential could be injected into anything.
func TestResolveReturnsRealValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, store, _, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := resolver.Resolve(ctx, []int{created.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve() returned %d credentials, want 1", len(got))
	}
	if got[0].Inputs["api_token"] != theSecret {
		t.Errorf("Resolve() gave %q for the secret, want the real value", got[0].Inputs["api_token"])
	}

	// The type travels with it, because injection needs both halves: the
	// schema to know what is secret, the injectors to know where it goes.
	if got[0].Type.Namespace != "custom_api" {
		t.Errorf("Resolve() lost the credential's type: %+v", got[0].Type)
	}
	if len(got[0].Type.Injectors.Env) == 0 {
		t.Error("Resolve() lost the injector document, so nothing could be injected")
	}
}

// TestResolveFillsTypeDefaults covers the work done here rather than at the
// injector, so the injector receives a complete value set and never has to
// reach back to the type to find out what a missing input should have been.
func TestResolveFillsTypeDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, store, _, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := resolver.Resolve(ctx, []int{created.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got[0].Inputs["region"] != "us-east-1" {
		t.Errorf("Resolve() did not fill the type's declared default: %v", got[0].Inputs)
	}
}

// TestResolvePreservesTheGivenOrder pins the ordering contract.
//
// The caller binds these to a template, and binding order is meaningful for
// vault credentials: re-sorting here would silently reorder the --vault-id
// arguments a playbook receives, which changes which vault password decrypts
// which file.
func TestResolvePreservesTheGivenOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, store, _, orgID, typeID := fixture(t)

	var ids []int
	for _, name := range []string{"zeta", "alpha", "mu"} {
		c, err := store.CreateCredential(ctx, orgID, typeID, name, "",
			map[string]string{"api_token": "token-for-" + name}, nil)
		if err != nil {
			t.Fatalf("CreateCredential() error = %v", err)
		}
		ids = append(ids, c.ID)
	}

	// Deliberately not ascending, so a resolver that sorted by id or by
	// name would produce a different result.
	requested := []int{ids[2], ids[0], ids[1]}

	got, err := resolver.Resolve(ctx, requested)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Resolve() returned %d credentials, want 3", len(got))
	}
	for i, want := range requested {
		if got[i].ID != want {
			t.Fatalf("Resolve() returned ids %v, want %v in the order given",
				[]int{got[0].ID, got[1].ID, got[2].ID}, requested)
		}
	}
}

// TestResolveRefusesAMissingCredential covers the deliberate hard failure.
//
// Silently omitting one of four bound credentials produces a run that
// authenticates partially, which fails somewhere unrelated and much later,
// and looks like a device problem rather than a credential problem.
func TestResolveRefusesAMissingCredential(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, store, _, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := resolver.Resolve(ctx, []int{created.ID, 999999})
	if err == nil {
		t.Fatalf("Resolve() silently omitted a missing credential, returning %d", len(got))
	}
	if strings.Contains(err.Error(), theSecret) {
		t.Errorf("the error quotes a secret value: %v", err)
	}
}

// TestResolveOnAnEmptySetIsNotAnError covers the ordinary case of a
// template that binds nothing, which must not be a dispatch failure.
func TestResolveOnAnEmptySetIsNotAnError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, _, _, _, _ := fixture(t)

	got, err := resolver.Resolve(ctx, nil)
	if err != nil {
		t.Fatalf("Resolve(nil) error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Resolve(nil) returned %d credentials", len(got))
	}
}

// TestResolveReturnsACopyOfTheInputs guards a real hazard.
//
// The injector fills prompted values and resolved external lookups into
// this map. Doing that to ent's own cached entity would leave a plaintext
// secret attached to a live object for as long as that entity is
// referenced, which is exactly the lifetime this package tries to keep
// short.
func TestResolveReturnsACopyOfTheInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, store, client, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := resolver.Resolve(ctx, []int{created.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	got[0].Inputs["api_token"] = "mutated-by-the-caller"
	got[0].Inputs["injected"] = "a-prompted-value"

	fresh := client.Credential.GetX(ctx, created.ID)
	if fresh.Inputs["api_token"] != theSecret {
		t.Error("mutating a resolved credential changed what the store holds")
	}
	if _, leaked := fresh.Inputs["injected"]; leaked {
		t.Error("a value added to a resolved credential appeared in the stored entity")
	}
}

// TestResolvedCredentialProjectsIntoTheBindingRule covers the seam between
// this package and the binding check, where the vault identifier has to
// come from the REAL values because vault_id may itself be a secret field.
func TestResolvedCredentialProjectsIntoTheBindingRule(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver, store, _, orgID, _ := fixture(t)

	vaultType, err := store.CreateType(ctx, orgID, credtype.CredentialType{
		Name:      "Vault",
		Kind:      credtype.KindVault,
		Namespace: "vault",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "vault_password", Label: "Password", Secret: true},
			{ID: "vault_id", Label: "Identifier"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	created, err := store.CreateCredential(ctx, orgID, vaultType.ID, "prod vault", "",
		map[string]string{"vault_password": "a-vault-password", "vault_id": "prod"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := resolver.Resolve(ctx, []int{created.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	bound := got[0].Bound()
	if bound.Kind != credtype.KindVault {
		t.Errorf("Bound().Kind = %q, want vault", bound.Kind)
	}
	if bound.VaultIdentifier != "prod" {
		t.Errorf("Bound().VaultIdentifier = %q, want prod", bound.VaultIdentifier)
	}
	if bound.CredentialName != "prod vault" {
		t.Errorf("Bound().CredentialName = %q", bound.CredentialName)
	}
}

// TestNewEntResolverRefusesANilClient pins the panic, which turns a wiring
// error into a process-start failure rather than a first-dispatch one.
func TestNewEntResolverRefusesANilClient(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("NewEntResolver accepted a nil client")
		}
	}()
	resolve.NewEntResolver(nil)
}

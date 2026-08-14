package credstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	_ "github.com/mattn/go-sqlite3"
)

// theSecret is the value these tests follow through the store. It is
// distinctive enough that finding it in a projection is a real result
// rather than a coincidence.
const theSecret = "sk-live-CANARY-9f8e7d6c5b4a3210"

// fixture opens a real database with the encryption hooks registered, the
// way the composition root does, and seeds an organization and a type.
func fixture(t *testing.T) (credstore.Store, *ent.Client, int, int) {
	t.Helper()

	ctx := context.Background()
	client, err := ent.OpenEmbedded(ctx, filepath.Join(t.TempDir(), "credstore_test.db"))
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
				{ID: "api_url", Label: "URL"},
			},
			Required: []string{"api_token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	return store, client, org.ID, ct.ID
}

// TestNoReadPathEverReturnsASecret is the assertion this whole package
// exists to make.
//
// It goes through EVERY method that returns a credential rather than one of
// them, because the failure this guards against is a single read path
// somebody added later without the projection. One untested method is
// exactly where that lands.
func TestNoReadPathEverReturnsASecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret, "api_url": "https://api.example.com"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// Bind it to a template so the template-scoped read path is covered too.
	inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)
	if err := store.SetTemplateCredentials(ctx, tmpl.ID, []int{created.ID}); err != nil {
		t.Fatalf("SetTemplateCredentials() error = %v", err)
	}

	fetched, err := store.GetCredential(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetCredential() error = %v", err)
	}
	listed, err := store.ListCredentials(ctx, orgID)
	if err != nil {
		t.Fatalf("ListCredentials() error = %v", err)
	}
	bound, err := store.TemplateCredentials(ctx, tmpl.ID)
	if err != nil {
		t.Fatalf("TemplateCredentials() error = %v", err)
	}
	updated, err := store.UpdateCredential(ctx, created.ID, "prod api", "renamed",
		map[string]string{"api_url": "https://api2.example.com"}, nil)
	if err != nil {
		t.Fatalf("UpdateCredential() error = %v", err)
	}

	paths := map[string][]credstore.Credential{
		"CreateCredential":    {created},
		"GetCredential":       {fetched},
		"ListCredentials":     listed,
		"TemplateCredentials": bound,
		"UpdateCredential":    {updated},
	}

	for name, creds := range paths {
		if len(creds) == 0 {
			t.Errorf("%s returned nothing, so this read path was not actually exercised", name)
			continue
		}
		for _, c := range creds {
			if c.Inputs["api_token"] == theSecret {
				t.Errorf("%s returned the secret in plaintext", name)
			}
			if c.Inputs["api_token"] != redact.Marker {
				t.Errorf("%s returned %q for a secret input, want the redaction marker", name, c.Inputs["api_token"])
			}
			// A non-secret input must survive, or the credential becomes
			// unmanageable: nobody can see which URL it points at.
			if c.Inputs["api_url"] == "" {
				t.Errorf("%s redacted a non-secret input", name)
			}
		}
	}
}

// TestUpdateTreatsTheMarkerAsLeaveAlone guards a data-loss bug that would
// look like a successful save.
//
// A form renders the marker for a secret. An operator edits an unrelated
// field and submits. If the store took the marker literally, every
// credential's secret would be destroyed on its first edit, replaced by the
// text "$encrypted$", and the failure would surface later as an
// authentication error against the device.
func TestUpdateTreatsTheMarkerAsLeaveAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret, "api_url": "https://one.example.com"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// Exactly what a form submits back: the marker for the secret, a real
	// new value for the field that changed.
	if _, err := store.UpdateCredential(ctx, created.ID, "prod api", "",
		map[string]string{"api_token": redact.Marker, "api_url": "https://two.example.com"}, nil); err != nil {
		t.Fatalf("UpdateCredential() error = %v", err)
	}

	stored := client.Credential.GetX(ctx, created.ID)
	if stored.Inputs["api_token"] != theSecret {
		t.Fatalf("the stored secret is now %q, so an ordinary edit destroyed it", stored.Inputs["api_token"])
	}
	if stored.Inputs["api_url"] != "https://two.example.com" {
		t.Errorf("the edited field was not saved: %v", stored.Inputs)
	}
}

// TestUpdateCanStillReplaceASecret is the other half. Treating the marker
// as leave-alone must not make a secret unchangeable, or a rotation becomes
// impossible.
func TestUpdateCanStillReplaceASecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	created, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	const rotated = "sk-live-ROTATED-0123456789abcdef"
	if _, err := store.UpdateCredential(ctx, created.ID, "prod api", "",
		map[string]string{"api_token": rotated}, nil); err != nil {
		t.Fatalf("UpdateCredential() error = %v", err)
	}

	if got := client.Credential.GetX(ctx, created.ID).Inputs["api_token"]; got != rotated {
		t.Errorf("the secret is %q, want the rotated value", got)
	}
}

// TestBindingRuleIsEnforcedAtTheStore proves the store checks too, not only
// the handler. Two callers, one implementation, and this is the one that
// stops a second writer skipping it.
func TestBindingRuleIsEnforcedAtTheStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	first, err := store.CreateCredential(ctx, orgID, typeID, "prod aws", "",
		map[string]string{"api_token": "one-token-value"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	second, err := store.CreateCredential(ctx, orgID, typeID, "dev aws", "",
		map[string]string{"api_token": "two-token-value"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
	tmpl := client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)

	err = store.SetTemplateCredentials(ctx, tmpl.ID, []int{first.ID, second.ID})
	if err == nil {
		t.Fatal("SetTemplateCredentials() bound two credentials of the same kind")
	}
	if !errors.Is(err, credtype.ErrBindingConflict) {
		t.Errorf("error = %v, want one matching ErrBindingConflict", err)
	}
}

// TestManagedTypesCannotBeEditedOrDeleted covers AWX's own rule, which is
// load bearing for imports: an import must recognize and reuse the
// built-ins rather than recreating them as custom types, and it can only do
// that if editing one is refused.
func TestManagedTypesCannotBeEditedOrDeleted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, _, _ := fixture(t)

	managed, err := store.EnsureManagedType(ctx, credtype.CredentialType{
		Name:      "Amazon Web Services",
		Kind:      credtype.KindCloud,
		Namespace: "aws",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "access_key", Label: "Access Key"},
		}},
	})
	if err != nil {
		t.Fatalf("EnsureManagedType() error = %v", err)
	}
	if !managed.Managed {
		t.Fatal("EnsureManagedType() did not mark the type managed")
	}

	if _, err := store.UpdateType(ctx, managed.ID, managed.CredentialType); !errors.Is(err, credstore.ErrManaged) {
		t.Errorf("UpdateType() on a managed type gave %v, want ErrManaged", err)
	}
	if err := store.DeleteType(ctx, managed.ID); !errors.Is(err, credstore.ErrManaged) {
		t.Errorf("DeleteType() on a managed type gave %v, want ErrManaged", err)
	}
}

// TestEnsureManagedTypeIsIdempotent covers the reconcile shape. Managed
// types are reconciled at every startup rather than installed by a
// migration, because a migration cannot be re-run when a later release adds
// a type.
func TestEnsureManagedTypeIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, _, _ := fixture(t)

	def := credtype.CredentialType{
		Name:      "Amazon Web Services",
		Kind:      credtype.KindCloud,
		Namespace: "aws",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "access_key", Label: "Access Key"}}},
	}

	for i := 0; i < 3; i++ {
		if _, err := store.EnsureManagedType(ctx, def); err != nil {
			t.Fatalf("EnsureManagedType() call %d error = %v", i+1, err)
		}
	}

	count := client.CredentialType.Query().CountX(ctx)
	// One from the fixture, one managed.
	if count != 2 {
		t.Errorf("reconciling three times produced %d types, want 2", count)
	}
}

// TestDeletingATypeInUseIsRefused covers the refusal that stops a delete
// from producing credentials that cannot be injected. Finding that out at
// launch is worse than being refused here with a count.
func TestDeletingATypeInUseIsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	if _, err := store.CreateCredential(ctx, orgID, typeID, "prod api", "",
		map[string]string{"api_token": "a-token-value"}, nil); err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	err := store.DeleteType(ctx, typeID)
	if !errors.Is(err, credstore.ErrInUse) {
		t.Errorf("DeleteType() on a type in use gave %v, want ErrInUse", err)
	}
	if !strings.Contains(err.Error(), "1 credential") {
		t.Errorf("the refusal does not say how many credentials block it: %v", err)
	}

	// Removing the credential releases the type.
	creds := client.Credential.Query().AllX(ctx)
	if err := store.DeleteCredential(ctx, creds[0].ID); err != nil {
		t.Fatalf("DeleteCredential() error = %v", err)
	}
	if err := store.DeleteType(ctx, typeID); err != nil {
		t.Errorf("DeleteType() after removing the credential error = %v", err)
	}
}

// TestCrossOrganizationRefusals covers the tenancy boundary ent cannot
// express, the same class as Template's own inventory check.
func TestCrossOrganizationRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, orgID, typeID := fixture(t)

	other := client.Organization.Create().SetName("other").SaveX(ctx)

	// A credential built on another tenant's custom type.
	_, err := store.CreateCredential(ctx, other.ID, typeID, "borrowed", "",
		map[string]string{"api_token": "a-token-value"}, nil)
	if !errors.Is(err, credstore.ErrCrossOrganization) {
		t.Errorf("CreateCredential() across tenants gave %v, want ErrCrossOrganization", err)
	}

	// A template binding another tenant's credential.
	mine, err := store.CreateCredential(ctx, orgID, typeID, "mine", "",
		map[string]string{"api_token": "a-token-value"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	otherInv := client.Inventory.Create().SetName("theirs").SetOrganizationID(other.ID).SaveX(ctx)
	otherTmpl := client.Template.Create().
		SetName("theirs").SetKind("runbook").SetDefinition("x.yml").
		SetOrganizationID(other.ID).SetInventoryID(otherInv.ID).SaveX(ctx)

	err = store.SetTemplateCredentials(ctx, otherTmpl.ID, []int{mine.ID})
	if !errors.Is(err, credstore.ErrCrossOrganization) {
		t.Errorf("SetTemplateCredentials() across tenants gave %v, want ErrCrossOrganization", err)
	}
}

// TestManagedTypesAreUsableByEveryTenant is the counterpart: the tenancy
// check must not lock every organization out of the built-ins, which belong
// to nobody.
func TestManagedTypesAreUsableByEveryTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, client, _, _ := fixture(t)

	machine, err := store.EnsureManagedType(ctx, credtype.CredentialType{
		Name:      "Machine",
		Kind:      credtype.KindSSH,
		Namespace: "ssh",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "username", Label: "Username"}}},
	})
	if err != nil {
		t.Fatalf("EnsureManagedType() error = %v", err)
	}

	other := client.Organization.Create().SetName("other").SaveX(ctx)
	if _, err := store.CreateCredential(ctx, other.ID, machine.ID, "their machine", "",
		map[string]string{"username": "root"}, nil); err != nil {
		t.Fatalf("a managed type was not usable by a second tenant: %v", err)
	}

	// And it appears in that tenant's list even though it belongs to nobody.
	types, err := store.ListTypes(ctx, other.ID)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	var found bool
	for _, ct := range types {
		if ct.Namespace == "ssh" {
			found = true
		}
	}
	if !found {
		t.Error("a managed type is missing from a tenant's type list, which would hide the whole built-in catalog")
	}
}

// TestCreateTypeValidatesInjectorsAtTheWrite proves the store compiles
// injector templates rather than storing whatever it is given. Architecture
// Principle 5: an author learns about a broken template when they save it,
// not when an operator launches a job.
func TestCreateTypeValidatesInjectorsAtTheWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, orgID, _ := fixture(t)

	_, err := store.CreateType(ctx, orgID, credtype.CredentialType{
		Name:      "Broken",
		Kind:      credtype.KindCloud,
		Namespace: "broken",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "token", Label: "Token"}}},
		// References an input the type does not declare.
		Injectors: credtype.Injectors{Env: map[string]string{"TOKEN": "{{ tokn }}"}},
	})
	if !errors.Is(err, credtype.ErrInvalidType) {
		t.Errorf("CreateType() stored a type whose injector names an undeclared input: %v", err)
	}
}

// TestExternalReferenceMustNameADeclaredInput covers the check that stops a
// typo producing a credential that looks configured, resolves nothing, and
// injects an empty value.
func TestExternalReferenceMustNameADeclaredInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _, orgID, typeID := fixture(t)

	_, err := store.CreateCredential(ctx, orgID, typeID, "external", "",
		map[string]string{"api_token": "a-token-value"},
		map[string]string{"api_tokn": "vault:secret/data/api#token"})
	if err == nil {
		t.Fatal("CreateCredential() accepted an external lookup naming an input the type does not declare")
	}
}

package credstore_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
)

// quietLogger returns a logger writing into a buffer, so a test can assert
// what an operator would have been told without printing it.
func quietLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// TestReconcileManagedInstallsTheShippedCatalog runs the real reconcile
// against a real database with the real catalog, which is the only version
// of this test worth having: a reconcile driven by two invented types would
// prove the loop works and not that the data this build ships can be
// installed.
func TestReconcileManagedInstallsTheShippedCatalog(t *testing.T) {
	t.Parallel()

	store, _, org, _ := fixture(t)
	ctx := context.Background()
	logger, _ := quietLogger()

	if err := credstore.ReconcileManaged(ctx, store, managed.Types(), logger); err != nil {
		t.Fatalf("ReconcileManaged() error = %v", err)
	}

	installed, err := store.ListTypes(ctx, org)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	byNamespace := make(map[string]credstore.CredentialType, len(installed))
	for _, ct := range installed {
		byNamespace[ct.Namespace] = ct
	}

	for _, want := range managed.Types() {
		got, ok := byNamespace[want.Namespace]
		if !ok {
			t.Errorf("the catalog ships %q and the reconcile did not install it", want.Namespace)
			continue
		}
		if !got.Managed {
			t.Errorf("%q was installed unmanaged, so nothing stops an operator editing it", want.Namespace)
		}
		if got.OrganizationID != 0 {
			t.Errorf("%q was installed into organization %d, want global", want.Namespace, got.OrganizationID)
		}
		if got.Name != want.Name {
			t.Errorf("%q installed as %q, want %q", want.Namespace, got.Name, want.Name)
		}
	}
}

// TestReconcileManagedIsIdempotent is the property that makes running this
// at every startup safe, and it is asserted by running it twice rather than
// by reasoning about EnsureManagedType.
func TestReconcileManagedIsIdempotent(t *testing.T) {
	t.Parallel()

	store, _, org, _ := fixture(t)
	ctx := context.Background()
	logger, _ := quietLogger()

	if err := credstore.ReconcileManaged(ctx, store, managed.Types(), logger); err != nil {
		t.Fatalf("first ReconcileManaged() error = %v", err)
	}
	first, err := store.ListTypes(ctx, org)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}

	if err := credstore.ReconcileManaged(ctx, store, managed.Types(), logger); err != nil {
		t.Fatalf("second ReconcileManaged() error = %v", err)
	}
	second, err := store.ListTypes(ctx, org)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("a second reconcile changed the type count from %d to %d", len(first), len(second))
	}
	// Row identity, not just the count: a reconcile that deleted and
	// recreated each type would keep the count and break every credential
	// pointing at one.
	ids := make(map[int]bool, len(first))
	for _, ct := range first {
		ids[ct.ID] = true
	}
	for _, ct := range second {
		if !ids[ct.ID] {
			t.Errorf("type %q has a new row id after a second reconcile, so its credentials would be orphaned", ct.Namespace)
		}
	}
}

// TestReconcileManagedCorrectsAnOutdatedManagedType covers the reason this
// is a reconcile at all: a later release changing an injector template has
// to reach a deployment that already installed the older one.
func TestReconcileManagedCorrectsAnOutdatedManagedType(t *testing.T) {
	t.Parallel()

	store, _, org, _ := fixture(t)
	ctx := context.Background()
	logger, _ := quietLogger()

	// A previous release's version of a shipped type, with a template this
	// release corrected.
	stale := credtype.CredentialType{
		Name:      "Amazon Web Services",
		Kind:      credtype.KindCloud,
		Namespace: "aws",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "username", Label: "Access Key"}},
			Required: []string{"username"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"AWS_ACCESS_KEY_ID": "{{ username }}"}},
	}
	if _, err := store.EnsureManagedType(ctx, stale); err != nil {
		t.Fatalf("seeding the stale type: %v", err)
	}

	if err := credstore.ReconcileManaged(ctx, store, managed.Types(), logger); err != nil {
		t.Fatalf("ReconcileManaged() error = %v", err)
	}

	types, err := store.ListTypes(ctx, org)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	for _, ct := range types {
		if ct.Namespace != "aws" {
			continue
		}
		if _, corrected := ct.Injectors.Env["AWS_SECRET_ACCESS_KEY"]; !corrected {
			t.Error("the stale aws type was left in place, so a corrected injector would never reach an upgraded deployment")
		}
		return
	}
	t.Fatal("aws is missing after the reconcile")
}

// TestReconcileManagedLeavesACustomTypeAloneAndSaysSo is the failure an
// operator has to act on, so it is asserted in three parts: the custom type
// survives untouched, the reconcile reports the failure, and the log names
// the namespace and what to do about it.
func TestReconcileManagedLeavesACustomTypeAloneAndSaysSo(t *testing.T) {
	t.Parallel()

	store, _, org, _ := fixture(t)
	ctx := context.Background()
	logger, buf := quietLogger()

	mine, err := store.CreateType(ctx, org, credtype.CredentialType{
		Name:      "Our AWS",
		Kind:      credtype.KindCloud,
		Namespace: "aws",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "our_key", Label: "Key", Secret: true}},
			Required: []string{"our_key"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"OUR_AWS_KEY": "{{ our_key }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	err = credstore.ReconcileManaged(ctx, store, managed.Types(), logger)
	if err == nil {
		t.Fatal("ReconcileManaged() reported success while a managed type could not be installed")
	}
	if !strings.Contains(err.Error(), "aws") {
		t.Errorf("error = %v, want it to name the namespace that failed", err)
	}

	// Untouched, which is the part that matters: overwriting an operator's
	// own type with one this platform ships would silently replace a
	// working credential's schema.
	after, err := store.GetType(ctx, mine.ID)
	if err != nil {
		t.Fatalf("GetType() error = %v", err)
	}
	if after.Name != "Our AWS" || after.Managed {
		t.Errorf("the custom type became %+v, want it unchanged and still custom", after)
	}
	if _, ours := after.Injectors.Env["OUR_AWS_KEY"]; !ours {
		t.Error("the custom type's injectors were replaced")
	}

	logged := buf.String()
	for _, want := range []string{"aws", "custom type holds its namespace", "rename"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log does not mention %q, so an operator would not know what to do:\n%s", want, logged)
		}
	}

	// Every other shipped type still installed. One namespace colliding is
	// not a reason to leave the deployment with no managed types at all.
	types, err := store.ListTypes(ctx, org)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	if len(types) < len(managed.Types()) {
		t.Errorf("installed %d types, want the %d shipped ones minus the collision, plus the custom one",
			len(types), len(managed.Types()))
	}
}

// TestListAllTypesCrossesTenantsWhereListTypesDoesNot is the pair, because
// the whole reason ListAllTypes is a separate method is that the two answer
// different questions and a caller must be able to pick.
func TestListAllTypesCrossesTenantsWhereListTypesDoesNot(t *testing.T) {
	t.Parallel()

	store, client, orgA, _ := fixture(t)
	ctx := context.Background()

	orgB := client.Organization.Create().SetName("other").SaveX(ctx)
	if _, err := store.CreateType(ctx, orgB.ID, credtype.CredentialType{
		Name:      "Other API",
		Kind:      credtype.KindCloud,
		Namespace: "other_api",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "token", Label: "Token", Secret: true}},
			Required: []string{"token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"OTHER_TOKEN": "{{ token }}"}},
	}); err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	scoped, err := store.ListTypes(ctx, orgA)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	for _, ct := range scoped {
		if ct.Namespace == "other_api" {
			t.Error("ListTypes returned another tenant's type, which is the boundary it exists to draw")
		}
	}

	all, err := store.ListAllTypes(ctx)
	if err != nil {
		t.Fatalf("ListAllTypes() error = %v", err)
	}
	var found bool
	for _, ct := range all {
		if ct.Namespace == "other_api" {
			found = true
			// The tenant's NAME, so a cross-tenant list renders something
			// readable rather than a primary key.
			if ct.OrganizationName != "other" {
				t.Errorf("OrganizationName = %q, want %q", ct.OrganizationName, "other")
			}
		}
	}
	if !found {
		t.Error("ListAllTypes did not return the other tenant's type")
	}
}

// TestListAllCredentialsCrossesTenantsAndStaysRedacted is the same pair on
// the credential side, plus the property that matters more there: the
// projection cannot carry a plaintext value however it was queried.
func TestListAllCredentialsCrossesTenantsAndStaysRedacted(t *testing.T) {
	t.Parallel()

	store, client, orgA, typeID := fixture(t)
	ctx := context.Background()

	if _, err := store.CreateCredential(ctx, orgA, typeID, "mine", "",
		map[string]string{"api_token": theSecret}, nil); err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	orgB := client.Organization.Create().SetName("other").SaveX(ctx)
	ctB, err := store.CreateType(ctx, orgB.ID, credtype.CredentialType{
		Name:      "Other API",
		Kind:      credtype.KindCloud,
		Namespace: "other_api",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "token", Label: "Token", Secret: true}},
			Required: []string{"token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"OTHER_TOKEN": "{{ token }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}
	if _, err := store.CreateCredential(ctx, orgB.ID, ctB.ID, "theirs", "",
		map[string]string{"token": theSecret}, nil); err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	scoped, err := store.ListCredentials(ctx, orgA)
	if err != nil {
		t.Fatalf("ListCredentials() error = %v", err)
	}
	if len(scoped) != 1 || scoped[0].Name != "mine" {
		t.Errorf("ListCredentials returned %d credential(s), want only this tenant's", len(scoped))
	}

	all, err := store.ListAllCredentials(ctx)
	if err != nil {
		t.Fatalf("ListAllCredentials() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAllCredentials returned %d, want both tenants'", len(all))
	}
	for _, c := range all {
		for id, v := range c.Inputs {
			if v == theSecret {
				t.Errorf("credential %q returned input %q in plaintext", c.Name, id)
			}
		}
		if c.OrganizationName == "" {
			t.Errorf("credential %q carries no organization name", c.Name)
		}
	}
}

// TestReconcileManagedReportsAnUninstallableTypeWithoutStopping covers the
// generic failure branch and the nil-logger default together.
//
// A malformed type is not a reason to refuse to start: the deployment
// still runs every template that does not use it, and a controller that
// will not boot because one credential type is unhappy converts a small
// problem into a total outage.
func TestReconcileManagedReportsAnUninstallableTypeWithoutStopping(t *testing.T) {
	t.Parallel()

	store, _, org, _ := fixture(t)
	ctx := context.Background()

	broken := credtype.CredentialType{
		Name:      "Broken",
		Kind:      credtype.KindCloud,
		Namespace: "broken",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "token", Label: "Token", Secret: true}}},
		// Names an input the type does not declare, which Validate refuses.
		Injectors: credtype.Injectors{Env: map[string]string{"BROKEN": "{{ not_declared }}"}},
	}

	// A nil logger, which is the other branch: the reconcile falls back to
	// the process default rather than panicking on a caller that did not
	// wire one.
	err := credstore.ReconcileManaged(ctx, store, append(managed.Types(), broken), nil)
	if err == nil {
		t.Fatal("ReconcileManaged() reported success while a type could not be installed")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error = %v, want it to name the type that failed", err)
	}

	// Everything else still installed, which is the point of continuing.
	types, err := store.ListTypes(ctx, org)
	if err != nil {
		t.Fatalf("ListTypes() error = %v", err)
	}
	installed := 0
	for _, ct := range types {
		if ct.Managed {
			installed++
		}
	}
	if installed != len(managed.Types()) {
		t.Errorf("installed %d managed types, want all %d despite the one failure", installed, len(managed.Types()))
	}
}

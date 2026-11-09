// Tests of the bridge that resolves a project's credential for a sync, over
// the real credential store and resolver.
package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	_ "github.com/mattn/go-sqlite3"
)

// TestProjectAuth_RefusesACredentialOfAnotherOrganization resolves a
// project's credential through the real store and resolver. A credential its
// own organization owns resolves to its values. Another organization's is
// refused before its secret is read, as a binding stored before the write
// paths checked the owner would be, and the refusal names neither the
// credential, its owner nor its secret.
func TestProjectAuth_RefusesACredentialOfAnotherOrganization(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })

	// Encrypted at rest and decrypted on read, as the composition root
	// installs it, so the resolver reads what it reads in a deployment.
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService: %v", err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	store := credstore.NewEntStore(client, render.New())
	if err := credstore.ReconcileManaged(ctx, store, managed.Types(), nil); err != nil {
		t.Fatalf("ReconcileManaged: %v", err)
	}
	scm, err := store.GetTypeByNamespace(ctx, "scm")
	if err != nil {
		t.Fatalf("reading the Source Control type: %v", err)
	}

	acme := client.Organization.Create().SetName("acme").SaveX(ctx)
	globex := client.Organization.Create().SetName("globex").SaveX(ctx)
	ownCred, err := store.CreateCredential(ctx, acme.ID, scm.ID, "acme-forge", "",
		map[string]string{"username": "git", "password": "ghp-acme-secret"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential(acme): %v", err)
	}
	foreignCred, err := store.CreateCredential(ctx, globex.ID, scm.ID, "globex-forge", "",
		map[string]string{"username": "git", "password": "ghp-globex-secret"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential(globex): %v", err)
	}

	auth := projectAuth{resolver: resolve.NewEntResolver(client), owners: store}

	got, err := auth.ResolveAuth(ctx, project.Project{ID: 1, OrganizationID: acme.ID, CredentialID: ownCred.ID})
	if err != nil || got.Password != "ghp-acme-secret" {
		t.Fatalf("ResolveAuth of the project's own organization's credential = %+v, %v; want its values", got, err)
	}

	got, err = auth.ResolveAuth(ctx, project.Project{ID: 2, OrganizationID: acme.ID, CredentialID: foreignCred.ID})
	if !errors.Is(err, errProjectCredentialOfAnotherOrganization) {
		t.Fatalf("ResolveAuth of another organization's credential = %+v, %v; want it refused", got, err)
	}
	if !reflect.DeepEqual(got, project.Auth{}) {
		t.Errorf("a refused ResolveAuth returned values: %+v", got)
	}
	for _, leak := range []string{"ghp-globex-secret", "globex", "forge"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal %q names %q", err, leak)
		}
	}

	// No credential is the public repository case, and nothing to check.
	if got, err := auth.ResolveAuth(ctx, project.Project{OrganizationID: acme.ID}); err != nil || !reflect.DeepEqual(got, project.Auth{}) {
		t.Errorf("ResolveAuth with no credential = %+v, %v; want an empty Auth", got, err)
	}

	// Without the owner lookup nothing can be checked, so nothing resolves.
	if _, err := (projectAuth{resolver: resolve.NewEntResolver(client)}).ResolveAuth(ctx,
		project.Project{OrganizationID: acme.ID, CredentialID: ownCred.ID}); err == nil {
		t.Error("ResolveAuth with no owner lookup resolved a credential")
	}
}

package resources_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// credentialCanary is the value the conformance suite follows through the
// credential views. It is distinctive enough that finding it in a rendered
// page is a real result rather than a coincidence.
const credentialCanary = "sk-live-UI-CANARY-4c3b2a1908f7e6d5"

// newTestCredentialStore builds the credential store the two credential
// views are registered over.
//
// A real ent-backed store rather than a fake, for the reason the access
// fixture gives about its own: the redaction these views depend on happens
// inside the store's projection, so a fake returning hand-built rows would
// assert that this suite can construct a redacted value rather than that
// the shipping code produces one. The encryption hook is registered the way
// the composition root registers it, so the canary below really is
// encrypted at rest and really is redacted on the way out.
func newTestCredentialStore(t *testing.T) credstore.Store {
	t.Helper()

	// No t.Cleanup, for the reason the other fixtures state: the view
	// registry is process-wide and the ports it captures outlive whichever
	// test triggered the registration.
	client := enttest.Open(t, "sqlite3", "file:uiaccessfixture?mode=memory&cache=shared&_fk=1")
	ctx := context.Background()

	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	org, err := client.Organization.Query().First(ctx)
	if err != nil {
		t.Fatalf("reading the seeded organization: %v", err)
	}

	store := credstore.NewEntStore(client, render.New())

	// The types this build ships, installed the way the controller
	// installs them, so the view renders the real catalog rather than an
	// invented one and a managed type's uneditability is a real property
	// of the rows on the page.
	if err := credstore.ReconcileManaged(ctx, store, managed.Types(), nil); err != nil {
		t.Fatalf("ReconcileManaged() error = %v", err)
	}

	// One custom type as well, so the MANAGED column has both values and
	// an assertion that platform types are marked cannot pass by there
	// being nothing else to compare against.
	ct, err := store.CreateType(ctx, org.ID, credtype.CredentialType{
		Name:      "Conformance API",
		Kind:      credtype.KindCloud,
		Namespace: "conformance_api",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "api_url", Label: "URL"},
				// One prompted input, so the launch form has a control to
				// render and the never-persist path has something to carry.
				// A suite whose only credential stored everything would
				// never exercise it.
				{ID: "one_time_code", Label: "One Time Code", Secret: true, AskAtRuntime: true},
			},
			Required: []string{"api_token"},
		},
		Injectors: credtype.Injectors{Env: map[string]string{"CONFORMANCE_TOKEN": "{{ api_token }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	cred, err := store.CreateCredential(ctx, org.ID, ct.ID,
		"conformance credential", "carries the canary",
		map[string]string{"api_token": credentialCanary, "api_url": "https://api.example.test"},
		nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// A Source Control credential, so the Projects view's chooser has
	// something it can legitimately offer. The conformance credential above
	// is a cloud type carrying an api_token, which is exactly the kind of
	// credential that chooser must REFUSE, so a suite holding only that one
	// could not tell a working filter from a broken one.
	scmType, err := store.GetTypeByNamespace(ctx, "scm")
	if err != nil {
		t.Fatalf("reading the shipped Source Control type: %v", err)
	}
	if _, err := store.CreateCredential(ctx, org.ID, scmType.ID,
		"conformance scm credential", "clones a private repository",
		map[string]string{"username": "git", "password": "ghp-conformance-token"},
		nil); err != nil {
		t.Fatalf("CreateCredential(scm) error = %v", err)
	}

	// A Machine credential carrying an SSH key, which is the sharpest test
	// of the Projects chooser's rule. It holds exactly the material a git
	// clone needs and must still be refused, because it was issued to open
	// shells on managed devices rather than to read a repository. A suite
	// whose only negative case was a cloud credential could not tell a
	// filter keyed on KIND from one keyed on what a credential carries.
	machineType, err := store.GetTypeByNamespace(ctx, "ssh")
	if err != nil {
		t.Fatalf("reading the shipped Machine type: %v", err)
	}
	if _, err := store.CreateCredential(ctx, org.ID, machineType.ID,
		"conformance machine credential", "reaches managed devices",
		map[string]string{
			"username":     "ops",
			"ssh_key_data": "-----BEGIN OPENSSH PRIVATE KEY-----\nconformance\n-----END OPENSSH PRIVATE KEY-----\n",
		},
		nil); err != nil {
		t.Fatalf("CreateCredential(machine) error = %v", err)
	}

	// Bound to the first template the template fixture seeded, which is
	// what makes the launch form's credential prompt reachable at all. The
	// template fixture runs first because the Deps literal names Templates
	// before Credentials and Go evaluates a struct literal in source order.
	tmpl, err := client.Template.Query().Order(ent.Asc("id")).First(ctx)
	if err != nil {
		t.Fatalf("reading a seeded template to bind against: %v", err)
	}
	if err := store.SetTemplateCredentials(ctx, tmpl.ID, []int{cred.ID}); err != nil {
		t.Fatalf("SetTemplateCredentials() error = %v", err)
	}

	return store
}

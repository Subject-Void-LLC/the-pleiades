package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	credfile "github.com/Subject-Void-LLC/the-pleiades/internal/credtype/lookup/file"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// External secret resolution, which happens HERE rather than at the
// injector, and at DISPATCH rather than at launch.
//
// That is PLAN.md Section 17.4's just-in-time requirement made real: a job
// queued behind a capacity limit holds a pointer to a secret rather than
// the secret, and a relaunch a week later reads whatever the source holds
// now rather than what it held then.

// secretsDir writes the named secrets to a fresh directory and returns a
// lookup set over it, through the real file source rather than a fake: it
// needs no network and no dependency, so RULE 0 is cheap to satisfy here.
func secretsDir(t *testing.T, secrets map[string]string) *credtype.Lookups {
	t.Helper()

	dir := t.TempDir()
	for name, body := range secrets {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("failed to write fixture secret %q: %v", name, err)
		}
	}
	lookups, err := credtype.NewLookups(credfile.New(dir))
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}
	return lookups
}

// externalFixture seeds a credential holding the given stored and external
// values, and returns the client alongside its id.
//
// It reuses the same real-database, real-hooks fixture the rest of this
// package's tests use rather than a second one, so the encryption and
// redaction underneath these assertions is the production path.
func externalFixture(t *testing.T, inputs, external map[string]string) (*ent.Client, int) {
	t.Helper()

	_, store, client, orgID, typeID := fixture(t)
	created, err := store.CreateCredential(context.Background(), orgID, typeID, "prod api", "", inputs, external)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	return client, created.ID
}

// TestExternalReferenceResolvesAtDispatch is the base case, driven through
// the real ent-backed resolver against a real database.
func TestExternalReferenceResolvesAtDispatch(t *testing.T) {
	lookups := secretsDir(t, map[string]string{"prod_api_token": "a-real-external-secret\n"})
	client, id := externalFixture(t, nil, map[string]string{"api_token": "file:prod_api_token"})

	resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))
	got, err := resolver.Resolve(context.Background(), []int{id})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve() returned %d credentials, want one", len(got))
	}
	if got[0].Inputs["api_token"] != "a-real-external-secret" {
		t.Errorf("the external input = %q, want the value the source holds", got[0].Inputs["api_token"])
	}
	// The reference is kept beside the resolved value, so an audit record
	// can say where a value came from without saying what it was.
	if got[0].External["api_token"] != "file:prod_api_token" {
		t.Errorf("External = %v, want the reference preserved", got[0].External)
	}
}

// TestAnUnresolvableReferenceFailsRatherThanInjectingThePointer is the case
// that matters most, and the failure mode it prevents is specific: a
// reference string reaching a remote API as a bearer token is an
// authentication failure attributed to the wrong subsystem, and the
// reference is now in that API's access log.
func TestAnUnresolvableReferenceFailsRatherThanInjectingThePointer(t *testing.T) {
	lookups := secretsDir(t, nil)
	client, id := externalFixture(t, nil, map[string]string{"api_token": "file:prod_api_token"})

	resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))
	got, err := resolver.Resolve(context.Background(), []int{id})
	if err == nil {
		t.Fatalf("Resolve() succeeded with an unresolvable reference and returned %+v", got)
	}
	// Named by id rather than by name: this reaches a job record, and a
	// credential's name is chosen by an operator and can carry anything.
	if !strings.Contains(err.Error(), "prod_api_token") {
		t.Errorf("the error does not name the reference: %v", err)
	}
}

// TestAControllerWithNoSourceConfiguredFailsOnlyTheCredentialThatNeedsOne
// covers the degradation, which has to be narrow.
//
// A deployment with no external secret source is an ordinary deployment,
// not a broken one, so it must resolve every credential whose values this
// platform stores and fail only the specific credential that names an
// external reference.
func TestAControllerWithNoSourceConfiguredFailsOnlyTheCredentialThatNeedsOne(t *testing.T) {
	_, store, client, orgID, typeID := fixture(t)
	ctx := context.Background()

	stored, err := store.CreateCredential(ctx, orgID, typeID, "stored api", "",
		map[string]string{"api_token": "a-stored-secret"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	external, err := store.CreateCredential(ctx, orgID, typeID, "external api", "",
		nil, map[string]string{"api_token": "file:prod_api_token"})
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// No WithLookups at all, which is exactly how a controller that has
	// configured nothing is built.
	resolver := resolve.NewEntResolver(client)

	got, err := resolver.Resolve(ctx, []int{stored.ID})
	if err != nil {
		t.Fatalf("an ordinary credential failed on a controller with no external source: %v", err)
	}
	if got[0].Inputs["api_token"] != "a-stored-secret" {
		t.Errorf("the stored input = %q, want the stored value", got[0].Inputs["api_token"])
	}

	if _, err := resolver.Resolve(ctx, []int{external.ID}); err == nil {
		t.Fatal("a credential naming an external source resolved on a controller with none configured")
	}
}

// TestAResolvedExternalValueIsNotWrittenBack covers the boundary between
// what this resolver returns and what the database holds.
//
// The resolved value lives for the length of one dispatch. Writing it back
// would turn an external secret into a stored one, silently, and the next
// rotation at the source would stop taking effect.
func TestAResolvedExternalValueIsNotWrittenBack(t *testing.T) {
	lookups := secretsDir(t, map[string]string{"prod_api_token": "a-real-external-secret"})
	client, id := externalFixture(t, nil, map[string]string{"api_token": "file:prod_api_token"})

	resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))
	if _, err := resolver.Resolve(context.Background(), []int{id}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	row := client.Credential.GetX(context.Background(), id)
	if _, written := row.Inputs["api_token"]; written {
		t.Errorf("the resolved external value was written back onto the row: %v", row.Inputs)
	}
}

// TestResolvingTwiceReadsTheSourceAgain is what makes "a relaunch picks up
// a rotated secret" true rather than intended.
func TestResolvingTwiceReadsTheSourceAgain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prod_api_token")
	if err := os.WriteFile(path, []byte("the-old-value"), 0o600); err != nil {
		t.Fatalf("failed to write the fixture secret: %v", err)
	}
	lookups, err := credtype.NewLookups(credfile.New(dir))
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}

	client, id := externalFixture(t, nil, map[string]string{"api_token": "file:prod_api_token"})

	resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))
	first, err := resolver.Resolve(context.Background(), []int{id})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if first[0].Inputs["api_token"] != "the-old-value" {
		t.Fatalf("the first resolve = %q, want the old value", first[0].Inputs["api_token"])
	}

	// The operator rotates the secret at the source.
	if err := os.WriteFile(path, []byte("the-new-value"), 0o600); err != nil {
		t.Fatalf("failed to rotate the fixture secret: %v", err)
	}

	second, err := resolver.Resolve(context.Background(), []int{id})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if second[0].Inputs["api_token"] != "the-new-value" {
		t.Errorf("the second resolve = %q, want the rotated value: nothing here may cache",
			second[0].Inputs["api_token"])
	}
}

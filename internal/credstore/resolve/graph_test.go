package resolve_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// The row form of external secret resolution: an input filled from another
// CREDENTIAL rather than from a string naming a process-wide source.
//
// What these tests are really asserting is that the recursion the row form
// introduces is bounded in both directions, because every link in a chain
// is an ordinary row an operator can write through an ordinary API.

// vaultNamespace is the credential type namespace the stub factory below
// builds a source for.
//
// Deliberately not one of AWX's own namespaces. managed.Types() is checked
// against AWX's registry in both directions by
// TestTheCatalogCoversEveryAWXManagedType, and a name invented here that
// later collided with a real one would be found by that test rather than by
// this one, which is the wrong place to learn it.
const vaultNamespace = "test_vault"

// stubLookup resolves a reference by concatenating the token its source
// credential holds with the reference itself.
//
// The concatenation is the whole point rather than a convenience. Asserting
// on the result proves BOTH halves reached the source: the source
// credential's own resolved input value, and the binding's metadata. In the
// chained case the token was itself resolved from a further source, so one
// string assertion covers the entire walk.
type stubLookup struct{ token string }

// Name returns the source name.
func (s stubLookup) Name() string { return vaultNamespace }

// Resolve returns the token and reference joined, or an error for the one
// reference reserved to exercise the failure path.
func (s stubLookup) Resolve(_ context.Context, reference string) (string, error) {
	if reference == "missing" {
		return "", fmt.Errorf("%w: no secret named %q", credtype.ErrLookupReference, reference)
	}
	return s.token + "/" + reference, nil
}

// stubFactory builds a stubLookup from a source credential's inputs.
type stubFactory struct{}

// Namespace returns the credential type namespace this factory serves.
func (stubFactory) Namespace() string { return vaultNamespace }

// New builds a source from the resolved inputs of one source credential.
//
// The reserved token exercises the path where a source credential exists,
// is bound, and still cannot produce a usable client: a Vault address that
// does not parse, a token the client refuses. That failure has to name the
// SOURCE rather than the target, because the source is the row somebody has
// to go and fix.
func (stubFactory) New(inputs map[string]string) (credtype.Lookup, error) {
	token := inputs["token"]
	if token == "" || token == "unusable" {
		return nil, errors.New("this source is not configured")
	}
	return stubLookup{token: token}, nil
}

// Reference flattens a binding's metadata into the reference Resolve reads.
func (stubFactory) Reference(metadata map[string]string) (string, error) {
	path := metadata["path"]
	if path == "" {
		return "", fmt.Errorf("%w: this binding has no path", credtype.ErrLookupReference)
	}
	return path, nil
}

// graphFixture returns everything a row-form test needs: a store and client
// over a real database with the real hooks, the organization, the target
// credential type, and an external-kind source type.
func graphFixture(t testing.TB) (credstore.Store, *ent.Client, *credtype.Lookups, int, int, int) {
	t.Helper()

	_, store, client, orgID, targetTypeID := fixture(t)

	sourceType, err := store.CreateType(context.Background(), orgID, credtype.CredentialType{
		Name:      "Test Vault",
		Kind:      credtype.KindExternal,
		Namespace: vaultNamespace,
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "token", Label: "Token", Secret: true},
			},
			Required: []string{"token"},
		},
	})
	if err != nil {
		t.Fatalf("CreateType() for the source type error = %v", err)
	}

	lookups, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{stubFactory{}})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}
	return store, client, lookups, orgID, targetTypeID, sourceType.ID
}

// TestAnInputResolvesThroughItsSourceCredential is the base case for the
// row form, driven through the real store and a real database.
func TestAnInputResolvesThroughItsSourceCredential(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "prod vault", "",
		map[string]string{"token": "s.roottoken"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}
	// The binding travels with the create: api_token is required and this
	// credential stores no value for it, so the two cannot be separate
	// writes.
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{"path": "secret/data/prod"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// Both halves: the source credential's own stored token, and the
	// binding's metadata. Either one missing would change this string.
	if want := "s.roottoken/secret/data/prod"; got[0].Inputs["api_token"] != want {
		t.Errorf("the bound input = %q, want %q", got[0].Inputs["api_token"], want)
	}
}

// TestASourceWhoseOwnTokenIsExternalResolvesThroughBoth is the recursion
// this phase's bounds exist for: a vault whose own token comes from another
// vault.
func TestASourceWhoseOwnTokenIsExternalResolvesThroughBoth(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	root, err := store.CreateCredential(ctx, orgID, sourceTypeID, "root vault", "",
		map[string]string{"token": "s.root"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the root vault error = %v", err)
	}
	// The second vault stores no token of its own; it reads one from the
	// first.
	inner, err := store.CreateCredential(ctx, orgID, sourceTypeID, "inner vault", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "token",
			SourceCredentialID: root.ID,
			Metadata:           map[string]string{"path": "issued"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the inner vault error = %v", err)
	}

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: inner.ID,
			Metadata:           map[string]string{"path": "app"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// The inner vault's token is itself "s.root/issued", so the final
	// value carries the whole chain. One assertion, two hops.
	if want := "s.root/issued/app"; got[0].Inputs["api_token"] != want {
		t.Errorf("the bound input = %q, want the whole chain %q", got[0].Inputs["api_token"], want)
	}
}

// TestTheStoreRefusesABindingThatWouldCloseACycle is the write-time half of
// the cycle rule: Architecture Principle 5's "catch it at write time."
func TestTheStoreRefusesABindingThatWouldCloseACycle(t *testing.T) {
	ctx := context.Background()
	store, _, _, orgID, _, sourceTypeID := graphFixture(t)

	second, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault b", "",
		map[string]string{"token": "s.b"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	// a reads its token from b, which is legal.
	first, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault a", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "token",
			SourceCredentialID: second.ID,
			Metadata:           map[string]string{"path": "x"},
		}}))
	if err != nil {
		t.Fatalf("the first binding should be legal, got error = %v", err)
	}

	// b reading its token from a closes the loop, and must be refused
	// before it is written rather than at the dispatch that trips over it.
	_, err = store.SetCredentialInputSources(ctx, second.ID, []credstore.InputSourceBinding{{
		InputID:            "token",
		SourceCredentialID: first.ID,
		Metadata:           map[string]string{"path": "y"},
	}})
	if !errors.Is(err, credtype.ErrLookupCycle) {
		t.Fatalf("SetCredentialInputSources() error = %v, want a cycle refusal", err)
	}
}

// TestACredentialCannotBeItsOwnSource is the degenerate cycle, refused by
// its own check so the error names the mistake rather than describing a
// walk of length one.
func TestACredentialCannotBeItsOwnSource(t *testing.T) {
	ctx := context.Background()
	store, _, _, orgID, _, sourceTypeID := graphFixture(t)

	cred, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	_, err = store.SetCredentialInputSources(ctx, cred.ID, []credstore.InputSourceBinding{{
		InputID:            "token",
		SourceCredentialID: cred.ID,
		Metadata:           map[string]string{"path": "x"},
	}})
	if !errors.Is(err, credtype.ErrLookupCycle) {
		t.Fatalf("SetCredentialInputSources() error = %v, want a cycle refusal", err)
	}
}

// TestTheResolverRefusesACycleWrittenBehindTheStore is the resolve-time
// half, and it is not redundant with the write-time half above.
//
// The store's check sees one writer's proposed graph at one instant. Two
// concurrent writers can each write a change that is acyclic on its own and
// cyclic together, and a direct SQL writer bypasses the store entirely. So
// the rows here are written through the ent client rather than the store,
// which is exactly the situation the resolve-time bound exists for.
func TestTheResolverRefusesACycleWrittenBehindTheStore(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, _, sourceTypeID := graphFixture(t)

	// Both store a token, so creating them is legal on its own. The cycle
	// is added afterwards, behind the store, which is the situation this
	// test exists for.
	first, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault a", "",
		map[string]string{"token": "s.a"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	second, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault b", "",
		map[string]string{"token": "s.b"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	bindBehindTheStore(t, client, first.ID, second.ID, "token", "x")
	bindBehindTheStore(t, client, second.ID, first.ID, "token", "y")

	_, err = resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{first.ID})
	if !errors.Is(err, credtype.ErrLookupCycle) {
		t.Fatalf("Resolve() error = %v, want a cycle refusal", err)
	}
	// It must refuse AS a cycle rather than as depth. An undetected cycle
	// would eventually trip the depth bound too, and reporting it as depth
	// sends somebody looking for a chain that does not exist.
	if errors.Is(err, credtype.ErrLookupDepth) {
		t.Errorf("Resolve() reported depth for a cycle: %v", err)
	}
	// The chain is named, because "a cycle exists" is not something an
	// operator can act on.
	if !strings.Contains(err.Error(), "->") {
		t.Errorf("Resolve() error = %v, want it to name the chain", err)
	}
}

// TestALegalChainAtTheLimitResolvesAndOneHopFurtherRefuses is the
// falsifiable-in-both-directions test the Release Gate asks for. A depth
// bound that refused everything would pass a one-sided test.
func TestALegalChainAtTheLimitResolvesAndOneHopFurtherRefuses(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, _, sourceTypeID := graphFixture(t)
	resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))

	// A chain of vaults, each reading its token from the next, with the
	// last one holding a real stored token.
	// Built back to front: a binding names a source that must already
	// exist, so the last vault in the chain (the one holding a real stored
	// token) is created first.
	build := func(t *testing.T, hops int) int {
		t.Helper()
		last, err := store.CreateCredential(ctx, orgID, sourceTypeID,
			fmt.Sprintf("vault end hops %d", hops), "", map[string]string{"token": "s.end"}, nil)
		if err != nil {
			t.Fatalf("CreateCredential() error = %v", err)
		}
		next := last.ID
		for i := hops - 1; i >= 0; i-- {
			cred, err := store.CreateCredential(ctx, orgID, sourceTypeID,
				fmt.Sprintf("vault %d hops %d", i, hops), "", nil, nil,
				credstore.WithInputSources([]credstore.InputSourceBinding{{
					InputID:            "token",
					SourceCredentialID: next,
					Metadata:           map[string]string{"path": "p"},
				}}))
			if err != nil {
				t.Fatalf("CreateCredential() error = %v", err)
			}
			next = cred.ID
		}
		return next
	}

	// Four hops is the documented limit and must succeed.
	atLimit := build(t, 4)
	if _, err := resolver.Resolve(ctx, []int{atLimit}); err != nil {
		t.Fatalf("a chain at the limit must resolve, got error = %v", err)
	}

	// Five must refuse, as depth rather than as a cycle.
	pastLimit := build(t, 5)
	_, err := resolver.Resolve(ctx, []int{pastLimit})
	if !errors.Is(err, credtype.ErrLookupDepth) {
		t.Fatalf("Resolve() error = %v, want a depth refusal", err)
	}
	if errors.Is(err, credtype.ErrLookupCycle) {
		t.Errorf("Resolve() reported a cycle for a chain that has none: %v", err)
	}
}

// TestTwoInputsSharingOneSourceIsNotACycle is the negative control for the
// cycle check: a diamond is not a loop, and a check keyed on "have I seen
// this credential" rather than on the chain would refuse it.
func TestTwoInputsSharingOneSourceIsNotACycle(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	bind := credstore.WithInputSources([]credstore.InputSourceBinding{{
		InputID:            "api_token",
		SourceCredentialID: source.ID,
		Metadata:           map[string]string{"path": "p"},
	}})
	first, err := store.CreateCredential(ctx, orgID, targetTypeID, "api one", "", nil, nil, bind)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	second, err := store.CreateCredential(ctx, orgID, targetTypeID, "api two", "", nil, nil, bind)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// Both in one call, which is what a template binding two credentials
	// does: a chain carried across the loop would report the second as a
	// cycle.
	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).
		Resolve(ctx, []int{first.ID, second.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Resolve() returned %d credentials, want two", len(got))
	}
	for i, cred := range got {
		if cred.Inputs["api_token"] != "s.t/p" {
			t.Errorf("credential %d resolved to %q, want the shared source's value", i, cred.Inputs["api_token"])
		}
	}
}

// TestAControllerWithNoFactoryNamesTheUnbuiltSource covers the deployment
// that has a binding and no source wired for it: the set of built sources
// is named, so the operator learns which one is missing rather than that
// something is.
func TestAControllerWithNoFactoryNamesTheUnbuiltSource(t *testing.T) {
	ctx := context.Background()
	store, client, _, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// A lookup set with the declared sources and no factories at all.
	empty, err := credtype.NewLookups()
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}
	_, err = resolve.NewEntResolver(client, resolve.WithLookups(empty)).Resolve(ctx, []int{target.ID})
	if !errors.Is(err, credtype.ErrLookupUnknown) {
		t.Fatalf("Resolve() error = %v, want an unknown-source refusal", err)
	}
	if !strings.Contains(err.Error(), vaultNamespace) {
		t.Errorf("Resolve() error = %v, want it to name the source it could not build", err)
	}
}

// bindBehindTheStore writes a binding row directly, bypassing the store's
// own refusals.
//
// It exists for exactly one test, and the reason is written there: the
// resolve-time bound must hold for rows the store never saw.
func bindBehindTheStore(t testing.TB, client *ent.Client, targetID, sourceID int, inputID, path string) {
	t.Helper()
	if err := client.CredentialInputSource.Create().
		SetInputID(inputID).
		SetMetadata(map[string]string{"path": path}).
		SetTargetCredentialID(targetID).
		SetSourceCredentialID(sourceID).
		Exec(context.Background()); err != nil {
		t.Fatalf("writing a binding directly: %v", err)
	}
}

// TestTwoBindingsOnOneCredentialBothResolve covers the ordinary case of a
// credential reading more than one input from a source, which is also what
// makes the deterministic ordering observable.
func TestTwoBindingsOnOneCredentialBothResolve(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{
			// Deliberately out of sorted order, so the store and the
			// resolver are both seen to order them rather than to inherit
			// whatever the caller sent.
			{InputID: "region", SourceCredentialID: source.ID, Metadata: map[string]string{"path": "r"}},
			{InputID: "api_token", SourceCredentialID: source.ID, Metadata: map[string]string{"path": "t"}},
		}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got[0].Inputs["api_token"] != "s.t/t" {
		t.Errorf("api_token = %q, want its own binding's value", got[0].Inputs["api_token"])
	}
	// region declares a default, so this also proves a binding wins over a
	// default rather than the other way round.
	if got[0].Inputs["region"] != "s.t/r" {
		t.Errorf("region = %q, want its binding's value rather than the type default", got[0].Inputs["region"])
	}
}

// TestAnIndirectCycleIsRefusedAtTheWrite covers the walk itself rather than
// the one-hop case: a to b to c and back to a is only found by following
// edges the proposed binding does not name.
func TestAnIndirectCycleIsRefusedAtTheWrite(t *testing.T) {
	ctx := context.Background()
	store, _, _, orgID, _, sourceTypeID := graphFixture(t)

	// c holds a real token; b reads from c; a reads from b.
	third, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault c", "",
		map[string]string{"token": "s.c"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	second, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault b", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: third.ID, Metadata: map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	first, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault a", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: second.ID, Metadata: map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// Pointing c at a closes a three-link loop. The proposed binding names
	// only a, so finding the cycle requires walking a to b to c.
	_, err = store.SetCredentialInputSources(ctx, third.ID, []credstore.InputSourceBinding{{
		InputID: "token", SourceCredentialID: first.ID, Metadata: map[string]string{"path": "p"},
	}})
	if !errors.Is(err, credtype.ErrLookupCycle) {
		t.Fatalf("SetCredentialInputSources() error = %v, want a cycle refusal", err)
	}
}

// TestADiamondIsWalkedOnceAndAccepted is the negative control for the walk
// above. Two inputs reaching one source by different routes is legal, and a
// walk that did not remember where it had been would either refuse it or
// revisit the shared node forever.
func TestADiamondIsWalkedOnceAndAccepted(t *testing.T) {
	ctx := context.Background()
	store, _, _, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	shared, err := store.CreateCredential(ctx, orgID, sourceTypeID, "shared vault", "",
		map[string]string{"token": "s.shared"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	left, err := store.CreateCredential(ctx, orgID, sourceTypeID, "left", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: shared.ID, Metadata: map[string]string{"path": "l"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	right, err := store.CreateCredential(ctx, orgID, sourceTypeID, "right", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: shared.ID, Metadata: map[string]string{"path": "r"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// One credential reaching the shared vault down both arms at once.
	if _, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{
			{InputID: "api_token", SourceCredentialID: left.ID, Metadata: map[string]string{"path": "p"}},
			{InputID: "region", SourceCredentialID: right.ID, Metadata: map[string]string{"path": "p"}},
		})); err != nil {
		t.Fatalf("a diamond must be accepted, got error = %v", err)
	}
}

// TestASourceThatCannotBeBuiltNamesTheSource covers the failure where the
// binding is well formed and the source credential's own values are not.
func TestASourceThatCannotBeBuiltNamesTheSource(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "broken vault", "",
		map[string]string{"token": "unusable"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "api_token", SourceCredentialID: source.ID, Metadata: map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	_, err = resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err == nil {
		t.Fatal("Resolve() accepted a source that cannot be built")
	}
	// The SOURCE's id, because that is the row whoever fixes this edits.
	if !strings.Contains(err.Error(), fmt.Sprintf("source credential %d", source.ID)) {
		t.Errorf("Resolve() error = %v, want it to name the source credential", err)
	}
}

// TestABindingWithNoUsableMetadataIsRefused covers the other half of a
// half-filled binding: the source builds, and there is nowhere in it to
// look.
func TestABindingWithNoUsableMetadataIsRefused(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "api_token", SourceCredentialID: source.ID,
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	_, err = resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if !errors.Is(err, credtype.ErrLookupReference) {
		t.Fatalf("Resolve() error = %v, want a reference refusal", err)
	}
}

// TestAResolverWithNoSourcesFailsOnlyTheBoundCredential covers the
// deployment that has bindings and no sources wired at all, which is a
// misconfiguration rather than bad data and must say so.
func TestAResolverWithNoSourcesFailsOnlyTheBoundCredential(t *testing.T) {
	ctx := context.Background()
	store, client, _, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	bound, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "api_token", SourceCredentialID: source.ID, Metadata: map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	plain, err := store.CreateCredential(ctx, orgID, targetTypeID, "ordinary", "",
		map[string]string{"api_token": "stored"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// No WithLookups at all, which is what a Controller with no external
	// secret source configured has.
	resolver := resolve.NewEntResolver(client)

	if _, err := resolver.Resolve(ctx, []int{plain.ID}); err != nil {
		t.Fatalf("a credential that stores its own values must still resolve, got error = %v", err)
	}
	if _, err := resolver.Resolve(ctx, []int{bound.ID}); err == nil {
		t.Fatal("Resolve() accepted a bound credential with no sources configured")
	}
}

// TestASecretMissingFromItsSourceFailsTheRun covers the binding that is
// entirely well formed and points at a secret the source does not hold,
// which is what a rotation that removed one looks like from here.
func TestASecretMissingFromItsSourceFailsTheRun(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, sourceTypeID := graphFixture(t)

	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "api_token", SourceCredentialID: source.ID,
			Metadata: map[string]string{"path": "missing"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	_, err = resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if !errors.Is(err, credtype.ErrLookupReference) {
		t.Fatalf("Resolve() error = %v, want a reference refusal", err)
	}
	// The target credential is named, because that is what a job record
	// carries and what the operator launched.
	if !strings.Contains(err.Error(), fmt.Sprintf("credential %d", target.ID)) {
		t.Errorf("Resolve() error = %v, want it to name the credential the job bound", err)
	}
}

// newResolver builds a resolver over a client and a lookup set.
//
// One line, and it exists so the container-backed release gate reads about
// Vault rather than about wiring.
func newResolver(client *ent.Client, lookups *credtype.Lookups) resolve.Resolver {
	return resolve.NewEntResolver(client, resolve.WithLookups(lookups))
}

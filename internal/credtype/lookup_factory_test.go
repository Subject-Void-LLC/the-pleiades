package credtype_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// The factory registry: the half of the lookup set that builds a source
// from a credential ROW rather than from process configuration.
//
// What these tests are really protecting is the claim lookup_factory.go
// makes in prose, that the row form was added BESIDE the Lookup port rather
// than by changing it. A test that could only be written by widening
// Lookup's own interface would be the evidence that claim was wrong.

// fakeFactory builds a fakeLookup for one namespace.
type fakeFactory struct {
	namespace string
	err       error
}

func (f fakeFactory) Namespace() string { return f.namespace }

func (f fakeFactory) New(map[string]string) (credtype.Lookup, error) {
	if f.err != nil {
		return nil, f.err
	}
	return fakeLookup{name: f.namespace}, nil
}

func (f fakeFactory) Reference(metadata map[string]string) (string, error) {
	return metadata["path"], nil
}

// fakeLookup is a source that returns its own name, which is enough to tell
// which factory built it.
type fakeLookup struct{ name string }

func (f fakeLookup) Name() string { return f.name }

func (f fakeLookup) Resolve(context.Context, string) (string, error) { return "value-" + f.name, nil }

// TestFactoriesAreSelectedByNamespace covers the lookup a resolver does
// once per binding, in both directions.
func TestFactoriesAreSelectedByNamespace(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{
		fakeFactory{namespace: "alpha"},
		fakeFactory{namespace: "beta"},
	})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}

	got, ok := lookups.Factory("alpha")
	if !ok {
		t.Fatal("Factory(\"alpha\") reported no factory for a namespace that was registered")
	}
	if got.Namespace() != "alpha" {
		t.Errorf("Factory(\"alpha\").Namespace() = %q, want alpha", got.Namespace())
	}

	// The negative half. An empty result from a correctly aimed lookup and
	// one from a misaimed lookup look identical, so both directions are
	// asserted in the same test.
	if _, ok := lookups.Factory("gamma"); ok {
		t.Error("Factory(\"gamma\") reported a factory for a namespace nobody registered")
	}
}

// TestNamespacesReportsTheRealSet is what lets an error or a form offer the
// operator the built sources rather than restate a list that drifts.
func TestNamespacesReportsTheRealSet(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{
		fakeFactory{namespace: "beta"},
		fakeFactory{namespace: "alpha"},
	})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}
	if got, want := lookups.Namespaces(), []string{"alpha", "beta"}; !slices.Equal(got, want) {
		t.Errorf("Namespaces() = %v, want %v sorted", got, want)
	}

	// A set with no factories reports an empty list rather than nil-vs-empty
	// ambiguity, which is what a caller formatting it into an error needs.
	plain, err := credtype.NewLookups()
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}
	if got := plain.Namespaces(); len(got) != 0 {
		t.Errorf("Namespaces() on a set with no factories = %v, want empty", got)
	}
}

// TestNewLookupsWithRefusesAMisconfiguredFactory covers the wiring errors,
// which are process-start failures rather than runtime ones: a composition
// root that registered two factories for one namespace has an ambiguity
// nothing downstream could resolve.
func TestNewLookupsWithRefusesAMisconfiguredFactory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		factories []credtype.LookupFactory
	}{
		{name: "a nil factory", factories: []credtype.LookupFactory{nil}},
		{name: "a factory with no namespace", factories: []credtype.LookupFactory{fakeFactory{}}},
		{
			name: "two factories for one namespace",
			factories: []credtype.LookupFactory{
				fakeFactory{namespace: "alpha"},
				fakeFactory{namespace: "alpha"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := credtype.NewLookupsWith(nil, tt.factories); !errors.Is(err, credtype.ErrLookupUnknown) {
				t.Fatalf("NewLookupsWith() error = %v, want a wiring refusal", err)
			}
		})
	}
}

// TestNewLookupsStillTakesDeploymentSources is the compatibility assertion
// that matters most in this file.
//
// The row form was added beside the string form rather than replacing it,
// and NewLookups kept its own signature so an existing composition root
// compiles unchanged. If this stopped holding, every deployment resolving a
// "file:" reference would break on upgrade.
func TestNewLookupsStillTakesDeploymentSources(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookups(fakeLookup{name: "alpha"})
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}
	if !slices.Contains(lookups.Names(), "alpha") {
		t.Errorf("Names() = %v, want the wired source present", lookups.Names())
	}
	// And it registers no factories, so the two halves stay independent.
	if got := lookups.Namespaces(); len(got) != 0 {
		t.Errorf("Namespaces() = %v, want none from a source-only constructor", got)
	}
}

// TestResolveThroughAppliesTheSameRefusals is why the row form calls
// ResolveThrough rather than resolving directly: an empty value must be
// refused wherever it came from.
func TestResolveThroughAppliesTheSameRefusals(t *testing.T) {
	t.Parallel()

	lookups, err := credtype.NewLookups()
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}
	ctx := context.Background()

	// A source that resolves to nothing. Injecting that empty value is how
	// a rotation that emptied a secret presents as an authentication
	// failure against the target device instead of as a broken reference.
	if _, err := lookups.ResolveThrough(ctx, "api_token", emptyLookup{}, "x"); !errors.Is(err, credtype.ErrLookupReference) {
		t.Errorf("ResolveThrough() with an empty value error = %v, want a reference refusal", err)
	}

	// No source at all, which is a wiring failure reaching the dispatch
	// path rather than a data one.
	if _, err := lookups.ResolveThrough(ctx, "api_token", nil, "x"); !errors.Is(err, credtype.ErrLookupUnknown) {
		t.Errorf("ResolveThrough(nil) error = %v, want an unknown-source refusal", err)
	}

	// The positive control, so the two refusals above are not passing
	// because everything fails.
	got, err := lookups.ResolveThrough(ctx, "api_token", fakeLookup{name: "alpha"}, "x")
	if err != nil || got != "value-alpha" {
		t.Errorf("ResolveThrough() = %q, %v, want the source's own value", got, err)
	}
}

// emptyLookup resolves everything to the empty string.
type emptyLookup struct{}

func (emptyLookup) Name() string { return "empty" }

func (emptyLookup) Resolve(context.Context, string) (string, error) { return "", nil }

// TestCheckValuesTreatsASourcedInputAsSupplied covers the fourth way a
// required input is legitimately absent, alongside a default, a string-form
// external reference and a launch-time prompt.
//
// It is unit tested here rather than only through the store because the
// exemption is what makes a Vault-backed credential storable at all: get it
// wrong in the strict direction and no such credential can be created, and
// wrong in the loose direction and a credential with nothing behind a
// required input saves cleanly and fails at three in the morning.
func TestCheckValuesTreatsASourcedInputAsSupplied(t *testing.T) {
	t.Parallel()

	schema := credtype.InputSchema{
		Fields: []credtype.InputField{
			{ID: "api_token", Label: "Token", Secret: true},
			{ID: "region", Label: "Region"},
		},
		Required: []string{"api_token"},
	}

	tests := []struct {
		name    string
		values  map[string]string
		sourced []string
		wantErr error
	}{
		{
			name:    "a required input with nothing behind it is still refused",
			wantErr: credtype.ErrInvalidCredential,
		},
		{
			name:    "the same input named as sourced is accepted",
			sourced: []string{"api_token"},
		},
		{
			name:   "a stored value still satisfies it with no sourced ids",
			values: map[string]string{"api_token": "stored"},
		},
		{
			name:    "naming an input the type does not declare is refused",
			values:  map[string]string{"api_token": "stored"},
			sourced: []string{"not_an_input"},
			wantErr: credtype.ErrInvalidCredential,
		},
		{
			name:    "a sourced id for a non-required input is accepted",
			values:  map[string]string{"api_token": "stored"},
			sourced: []string{"region"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := schema.CheckValues(tt.values, nil, tt.sourced...)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckValues() error = %v, want none", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckValues() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestAReferenceStringNamingARowBackedSourceSaysWhy covers the answer a
// deployment gives when an AWX import carries the string form for a source
// this platform implements only as a row.
//
// The distinction it protects is small to write and large to get wrong.
// "Nobody has built this yet" tells an operator to wait for a release.
// "This exists, and you have asked for it the one way it cannot work" tells
// them to move the address and token into a source credential, which is
// something they can do this afternoon. The same eight names answer both
// ways depending on what the deployment has wired, so the branch is chosen
// at run time rather than baked into a table.
func TestAReferenceStringNamingARowBackedSourceSaysWhy(t *testing.T) {
	t.Parallel()

	// With a factory registered, the row-only answer wins over the
	// declared-not-implemented stub of the same name.
	withFactory, err := credtype.NewLookupsWith(nil, []credtype.LookupFactory{fakeFactory{namespace: "hashivault_kv"}})
	if err != nil {
		t.Fatalf("NewLookupsWith() error = %v", err)
	}
	_, err = withFactory.Resolve(context.Background(), "api_token", "hashivault_kv:secret/data/prod")
	if !errors.Is(err, credtype.ErrLookupRowOnly) {
		t.Fatalf("Resolve() error = %v, want a row-only refusal", err)
	}
	if errors.Is(err, credtype.ErrLookupNotImplemented) {
		t.Error("a source with a registered factory was reported as not implemented")
	}

	// Without one, the same reference gets the declared-not-implemented
	// answer, which is what makes the branch above meaningful rather than
	// a rename of it.
	plain, err := credtype.NewLookups()
	if err != nil {
		t.Fatalf("NewLookups() error = %v", err)
	}
	_, err = plain.Resolve(context.Background(), "api_token", "hashivault_kv:secret/data/prod")
	if !errors.Is(err, credtype.ErrLookupNotImplemented) {
		t.Fatalf("Resolve() error = %v, want a not-implemented refusal", err)
	}
	if errors.Is(err, credtype.ErrLookupRowOnly) {
		t.Error("a source with no factory was reported as row-backed")
	}
}

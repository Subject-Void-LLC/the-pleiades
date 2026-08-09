package auth_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
)

// newRealChain builds the AdmissionChain a composition root actually
// builds: one NewTokenScopeRule over a real jwtEvaluator, not a stub.
// RULE 0 applies to this file specifically, because the whole point of
// routing affordance decisions through the chain rather than reading
// Identity.Scopes is that the chain applies Identity.HasScope's admin
// bypass and wildcard handling. A fake rule would prove none of that.
func newRealChain(t testing.TB) auth.AdmissionChain {
	t.Helper()
	issuer := authtest.New(t, "hateoas-test-issuer", "hateoas-test-audience")
	return auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
}

// deviceCandidates is the candidate set a device resource offers: a read
// affordance and a write one, the exact shape Phase 13's Release Gate
// asserts against.
func deviceCandidates() []auth.Affordance {
	return []auth.Affordance{
		{Rel: auth.RelSelf, Scope: auth.ScopeInventoryRead},
		{Rel: auth.RelDelete, Scope: auth.ScopeInventoryWrite},
	}
}

func TestNewAdmissionHATEOASGenerator_RejectsEmptyChain(t *testing.T) {
	// An empty AdmissionChain denies every request (chain.go's own
	// fail-closed branch), so a generator built on one would answer "no
	// affordances" for every caller including an admin, and the Release
	// Gate's "omits the delete URL" would pass by omitting everything.
	// That has to be a startup error, not a silent runtime behavior.
	gen, err := auth.NewAdmissionHATEOASGenerator(nil)
	if err == nil {
		t.Fatal("expected an empty AdmissionChain to be rejected at construction")
	}
	if gen != nil {
		t.Fatalf("expected a nil generator alongside the error, got %#v", gen)
	}

	gen, err = auth.NewAdmissionHATEOASGenerator(auth.AdmissionChain{})
	if err == nil {
		t.Fatal("expected a zero-length AdmissionChain to be rejected at construction")
	}
	if gen != nil {
		t.Fatalf("expected a nil generator alongside the error, got %#v", gen)
	}
}

func TestAdmissionGenerator_Permitted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity *auth.Identity
		want     []auth.LinkRel
		why      string
	}{
		{
			name:     "read only caller",
			identity: &auth.Identity{Subject: "viewer", Role: auth.RoleViewer, Scopes: []auth.Scope{auth.ScopeInventoryRead}},
			want:     []auth.LinkRel{auth.RelSelf},
			why:      "this is the Release Gate itself: the delete relation must be absent, and self must be present so the absence is not vacuous",
		},
		{
			name:     "read and write caller",
			identity: &auth.Identity{Subject: "operator", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite}},
			want:     []auth.LinkRel{auth.RelSelf, auth.RelDelete},
			why:      "the positive half; without it, 'omits delete' would be satisfied by a generator that omits everything",
		},
		{
			name:     "admin holding no explicit scopes",
			identity: &auth.Identity{Subject: "root", Role: auth.RoleAdmin},
			want:     []auth.LinkRel{auth.RelSelf, auth.RelDelete},
			why:      "Identity.HasScope bypasses scope checks for RoleAdmin; a generator reading Identity.Scopes directly would return nothing here, which is the vacuous pass this phase closes",
		},
		{
			name:     "wildcard scope",
			identity: &auth.Identity{Subject: "machine", Role: auth.RoleOperator, Scopes: []auth.Scope{"*"}},
			want:     []auth.LinkRel{auth.RelSelf, auth.RelDelete},
			why:      "the wildcard is handled by HasScope, so it must reach the caller through the chain without this file knowing it exists",
		},
		{
			name:     "caller holding an unrelated scope",
			identity: &auth.Identity{Subject: "runner", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeRunbookExecute}},
			want:     []auth.LinkRel{},
			why:      "an empty result is legitimate; it is distinguishable from a failure only because Permitted returns a nil error alongside it",
		},
		{
			name:     "write only caller",
			identity: &auth.Identity{Subject: "writer", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryWrite}},
			want:     []auth.LinkRel{auth.RelDelete},
			why:      "scopes do not imply one another: inventory:write does not grant inventory:read, so self is absent",
		},
		{
			name:     "unauthenticated",
			identity: nil,
			want:     []auth.LinkRel{},
			why:      "fail closed, matching scopeRule's own nil-identity branch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gen, err := auth.NewAdmissionHATEOASGenerator(newRealChain(t))
			if err != nil {
				t.Fatalf("building generator: %v", err)
			}

			got, err := gen.Permitted(context.Background(), tc.identity, deviceCandidates())
			if err != nil {
				t.Fatalf("Permitted returned an error for a decidable request: %v", err)
			}
			assertRelsEqual(t, got, tc.want, tc.why)
		})
	}
}

func TestAdmissionGenerator_PermittedIsAlwaysASubsetOfCandidates(t *testing.T) {
	// The port returns []LinkRel rather than []Affordance so that an
	// implementation can only ever select from what it was offered. This
	// asserts the property for the implementation shipped here; the
	// caller re-intersects regardless, but a generator that invented a
	// relation would still be a defect worth catching at this layer.
	gen, err := auth.NewAdmissionHATEOASGenerator(newRealChain(t))
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}

	candidates := deviceCandidates()
	offered := make(map[auth.LinkRel]bool, len(candidates))
	for _, c := range candidates {
		offered[c.Rel] = true
	}

	for _, id := range []*auth.Identity{
		nil,
		{Subject: "a", Role: auth.RoleAdmin},
		{Subject: "b", Role: auth.RoleViewer},
		{Subject: "c", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryRead}},
	} {
		got, err := gen.Permitted(context.Background(), id, candidates)
		if err != nil {
			t.Fatalf("Permitted: %v", err)
		}
		for _, rel := range got {
			if !offered[rel] {
				t.Errorf("generator returned relation %q that was never offered as a candidate", rel)
			}
		}
	}
}

func TestAdmissionGenerator_EmptyCandidatesYieldsEmptyNotNil(t *testing.T) {
	// An empty slice and a nil slice marshal to [] and null respectively.
	// FAILURE_PATTERNS.md #73's whole point is that "computed, and you may
	// do nothing" must be distinguishable from "could not be computed", so
	// this layer must never hand the encoder a nil.
	gen, err := auth.NewAdmissionHATEOASGenerator(newRealChain(t))
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}

	got, err := gen.Permitted(context.Background(), &auth.Identity{Subject: "x", Role: auth.RoleAdmin}, nil)
	if err != nil {
		t.Fatalf("Permitted: %v", err)
	}
	if got == nil {
		t.Fatal("expected an empty, non-nil slice so the encoder emits [] rather than null")
	}
	if len(got) != 0 {
		t.Fatalf("expected no relations from no candidates, got %v", got)
	}
}

func TestAdmissionGenerator_PreservesCandidateOrder(t *testing.T) {
	// Link order is part of the response body a client sees. Deriving it
	// from the caller's own candidate order, rather than from map
	// iteration, is what keeps a response byte-stable across requests.
	gen, err := auth.NewAdmissionHATEOASGenerator(newRealChain(t))
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}

	candidates := []auth.Affordance{
		{Rel: auth.RelDelete, Scope: auth.ScopeInventoryWrite},
		{Rel: auth.RelSelf, Scope: auth.ScopeInventoryRead},
		{Rel: auth.RelExecute, Scope: auth.ScopeRunbookExecute},
	}
	admin := &auth.Identity{Subject: "root", Role: auth.RoleAdmin}

	for i := 0; i < 8; i++ {
		got, err := gen.Permitted(context.Background(), admin, candidates)
		if err != nil {
			t.Fatalf("Permitted: %v", err)
		}
		assertRelsEqual(t, got,
			[]auth.LinkRel{auth.RelDelete, auth.RelSelf, auth.RelExecute},
			"an admin is permitted everything, in the order the candidates were supplied")
	}
}

// assertRelsEqual compares two relation slices for exact equality,
// including order, and reports why the assertion exists when it fails.
func assertRelsEqual(t *testing.T, got, want []auth.LinkRel, why string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v (%s)", got, want, why)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (%s)", got, want, why)
		}
	}
}

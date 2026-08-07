package auth_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// BenchmarkAdmissionGenerator_Permitted prices the affordance probe in
// isolation, one admission-chain evaluation per candidate.
//
// The candidate counts are the ones a real resource reaches. A REST
// resource declares a handful of actions, not hundreds, so 32 is already
// well past anything this platform will serve; it is included to show the
// curve is linear in candidates and to name where that stops being cheap.
// The chain here carries one rule, NewTokenScopeRule, which is pure
// in-memory scope matching. The number would change entirely once
// NewScopeRule joins the chain, because that rule does a TeamLookup
// database round trip per evaluation, and at that point affordance
// probing needs a per-request memo rather than N independent calls. That
// is the concrete scale at which this design breaks, stated rather than
// left as "it scales."
func BenchmarkAdmissionGenerator_Permitted(b *testing.B) {
	evaluator := newTestEvaluator(b, []byte("hateoas-benchmark-secret-32-bytes-minimum"))
	gen, err := auth.NewAdmissionHATEOASGenerator(auth.AdmissionChain{auth.NewTokenScopeRule(evaluator)})
	if err != nil {
		b.Fatalf("building generator: %v", err)
	}

	id := &auth.Identity{
		Subject: "operator@example.com",
		Role:    auth.RoleOperator,
		Scopes:  []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite},
	}

	rels := []auth.LinkRel{auth.RelSelf, auth.RelDelete, auth.RelUpdate, auth.RelCreate, auth.RelExecute, auth.RelLogs}
	scopes := []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite, auth.ScopeRunbookExecute, auth.ScopeJobRead}

	for _, count := range []int{2, 8, 32} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			candidates := make([]auth.Affordance, 0, count)
			for i := 0; i < count; i++ {
				candidates = append(candidates, auth.Affordance{
					Rel:   rels[i%len(rels)],
					Scope: scopes[i%len(scopes)],
				})
			}

			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := gen.Permitted(ctx, id, candidates); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAdmissionGenerator_AdminBypass measures the path an admin
// takes, which short-circuits inside Identity.HasScope rather than
// scanning the scope slice. It is separate because an admin is the caller
// most likely to be offered every affordance, so it is the worst case for
// candidate count and the best case per candidate.
func BenchmarkAdmissionGenerator_AdminBypass(b *testing.B) {
	evaluator := newTestEvaluator(b, []byte("hateoas-benchmark-secret-32-bytes-minimum"))
	gen, err := auth.NewAdmissionHATEOASGenerator(auth.AdmissionChain{auth.NewTokenScopeRule(evaluator)})
	if err != nil {
		b.Fatalf("building generator: %v", err)
	}

	id := &auth.Identity{Subject: "root@example.com", Role: auth.RoleAdmin}
	candidates := []auth.Affordance{
		{Rel: auth.RelSelf, Scope: auth.ScopeInventoryRead},
		{Rel: auth.RelDelete, Scope: auth.ScopeInventoryWrite},
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := gen.Permitted(ctx, id, candidates); err != nil {
			b.Fatal(err)
		}
	}
}

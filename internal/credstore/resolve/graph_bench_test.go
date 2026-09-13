package resolve_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
)

// What resolution costs, measured rather than asserted, per checkbox rule 2.
//
// The number that matters is not the absolute time, which is dominated by
// an embedded database on a development machine. It is the SLOPE: this runs
// once per bound credential per dispatch, with a device fan-out waiting
// behind it, so the question the depth bound exists to answer is what one
// extra hop costs. A per-hop cost close to the no-source case would mean
// the bound is guarding nothing; a steep one means the bound is the control
// it claims to be.

// benchChain builds a chain of source credentials of the given depth and
// returns the id of the credential at its head.
func benchChain(b *testing.B, hops int) (resolve.Resolver, int) {
	b.Helper()

	ctx := context.Background()
	store, client, lookups, orgID, _, sourceTypeID := graphFixture(b)

	last, err := store.CreateCredential(ctx, orgID, sourceTypeID, "vault end", "",
		map[string]string{"token": "s.end"}, nil)
	if err != nil {
		b.Fatalf("CreateCredential() error = %v", err)
	}
	next := last.ID
	for i := hops - 1; i >= 0; i-- {
		cred, err := store.CreateCredential(ctx, orgID, sourceTypeID,
			fmt.Sprintf("vault %d", i), "", nil, nil,
			credstore.WithInputSources([]credstore.InputSourceBinding{{
				InputID:            "token",
				SourceCredentialID: next,
				Metadata:           map[string]string{"path": "p"},
			}}))
		if err != nil {
			b.Fatalf("CreateCredential() error = %v", err)
		}
		next = cred.ID
	}
	return resolve.NewEntResolver(client, resolve.WithLookups(lookups)), next
}

// BenchmarkResolveNoSource is the control: a credential that stores its own
// values and walks nothing. Everything below is measured against this.
func BenchmarkResolveNoSource(b *testing.B) {
	resolver, id := benchChain(b, 0)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := resolver.Resolve(ctx, []int{id}); err != nil {
			b.Fatalf("Resolve() error = %v", err)
		}
	}
}

// BenchmarkResolveOneHop is AWX parity: one credential sourced from one
// external credential, which is the only depth AWX itself allows.
func BenchmarkResolveOneHop(b *testing.B) {
	resolver, id := benchChain(b, 1)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := resolver.Resolve(ctx, []int{id}); err != nil {
			b.Fatalf("Resolve() error = %v", err)
		}
	}
}

// BenchmarkResolveAtDepthLimit is the worst case the bound permits, and the
// figure the bound itself should be argued from.
func BenchmarkResolveAtDepthLimit(b *testing.B) {
	resolver, id := benchChain(b, 4)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := resolver.Resolve(ctx, []int{id}); err != nil {
			b.Fatalf("Resolve() error = %v", err)
		}
	}
}

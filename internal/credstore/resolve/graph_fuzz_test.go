package resolve_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// Fuzzing the resolution graph, which is a different target from fuzzing a
// parser and is fuzzed for a different reason.
//
// A parser is fuzzed to find the input that panics. This walk is fuzzed to
// find the SHAPE that does not terminate. Every edge in it is a row an
// operator can write, so the arrangement of edges is attacker-controlled
// even though no individual value is, and the failure that matters is not a
// panic but a walk that runs until the Controller falls over.
//
// So the property asserted is deliberately weak on values and strong on
// termination: whatever the graph, Resolve must come back, and it must come
// back either with an answer or with an error this platform named.

// FuzzResolutionGraph builds an arbitrary source graph and asserts the walk
// always terminates with a classified outcome.
//
// The rows are written BEHIND the store, using the ent client directly,
// which is the point: the store refuses cycles at write time, so a fuzzer
// going through it could never produce one. The resolve-time bound is what
// holds for rows the store never saw, and that is what this exercises.
func FuzzResolutionGraph(f *testing.F) {
	// Seeds are the shapes worth naming: no edges, a self loop, a two
	// cycle, a chain at the limit, and a chain past it.
	f.Add([]byte{0xff, 0xff, 0xff})
	f.Add([]byte{0x00})
	f.Add([]byte{0x01, 0x00})
	f.Add([]byte{0x01, 0x02, 0x03, 0x04, 0xff})
	f.Add([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0xff})
	f.Add([]byte{0x02, 0x02, 0x01, 0xff, 0x00})

	f.Fuzz(func(t *testing.T, edges []byte) {
		// Bounded so one iteration stays a graph rather than a database
		// benchmark. Eight nodes is more than enough to express every
		// shape that matters: a cycle, a diamond, a chain past the depth
		// limit, and a forest of them at once.
		const maxNodes = 8
		if len(edges) == 0 || len(edges) > maxNodes {
			return
		}

		ctx := context.Background()
		store, client, lookups, orgID, _, sourceTypeID := graphFixture(t)

		// Every node stores a token, so a node with no source is always
		// resolvable and the only failures possible are the ones this walk
		// is responsible for.
		ids := make([]int, len(edges))
		for i := range edges {
			cred, err := store.CreateCredential(ctx, orgID, sourceTypeID,
				fmt.Sprintf("node %d", i), "", map[string]string{"token": "s.t"}, nil)
			if err != nil {
				t.Fatalf("CreateCredential() error = %v", err)
			}
			ids[i] = cred.ID
		}

		// Each byte points its node at another node, or at nothing. Read
		// modulo the node count so every byte is meaningful rather than
		// mostly discarded.
		for i, b := range edges {
			if b == 0xff {
				continue
			}
			bindBehindTheStore(t, client, ids[i], ids[int(b)%len(ids)], "token", "p")
		}

		resolver := resolve.NewEntResolver(client, resolve.WithLookups(lookups))

		// A deadline rather than trusting the bound: the assertion is that
		// the walk terminates, and a test that hangs to prove a walk hangs
		// reports as a timeout on the whole package with nothing naming
		// the input.
		deadlined, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		for _, id := range ids {
			_, err := resolver.Resolve(deadlined, []int{id})
			if err == nil {
				continue
			}
			// Every failure must be one this platform named. An
			// unclassified error here would mean the walk found a state
			// nobody described, which is the finding.
			switch {
			case errors.Is(err, credtype.ErrLookupCycle),
				errors.Is(err, credtype.ErrLookupDepth),
				errors.Is(err, credtype.ErrLookupReference),
				errors.Is(err, credtype.ErrLookupUnknown):
			default:
				t.Fatalf("Resolve(%d) with edges %v returned an unclassified error: %v", id, edges, err)
			}
			if deadlined.Err() != nil {
				t.Fatalf("Resolve(%d) with edges %v did not terminate within the deadline", id, edges)
			}
		}
	})
}

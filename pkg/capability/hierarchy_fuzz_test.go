package capability_test

import (
	"sort"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// allNamesSorted returns every registered Name in a stable order, so a
// fuzz-generated index deterministically picks the same capability across
// runs given the same seed.
func allNamesSorted() []capability.Name {
	all := capability.All()
	names := make([]capability.Name, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// FuzzResolves fuzzes Resolves against random subsets of the real,
// registered vocabulary (conflicting siblings declared together,
// redundant/overlapping bits, the empty set via mask=0) and a random
// required Name, including unregistered garbage via bogusRequired. It
// must never panic, and must always agree with resolvesReference, an
// independently written reimplementation using only exported API.
func FuzzResolves(f *testing.F) {
	f.Add(uint32(0), uint8(0), "")
	f.Add(uint32(1), uint8(1), "")
	f.Add(^uint32(0), uint8(5), "")
	f.Add(uint32(0b101), uint8(2), "totally-bogus-capability")

	f.Fuzz(func(t *testing.T, mask uint32, requiredIdx uint8, bogusRequired string) {
		names := allNamesSorted()
		if len(names) == 0 {
			t.Skip("no capabilities registered")
		}

		declared := make(map[capability.Name]struct{})
		for i, n := range names {
			if i >= 32 {
				break
			}
			if mask&(1<<uint(i)) != 0 {
				declared[n] = struct{}{}
			}
		}

		required := names[int(requiredIdx)%len(names)]
		if bogusRequired != "" {
			required = capability.Name(bogusRequired)
		}

		got := capability.Resolves(declared, required)
		want := resolvesReference(declared, required)
		if got != want {
			t.Fatalf("Resolves(%v, %q) = %v, want %v (reference)", declared, required, got, want)
		}

		if len(declared) == 0 && got {
			t.Fatalf("Resolves(<empty>, %q) = true, want false", required)
		}
		if _, ok := declared[required]; ok && !got {
			t.Fatalf("Resolves(%v, %q) = false, want true (required is directly declared)", declared, required)
		}
	})
}

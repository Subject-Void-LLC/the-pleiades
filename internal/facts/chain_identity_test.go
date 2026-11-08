// Tests that the Collection identity on a provenance (Phase 136b) leaves
// every chain stored before it verifying, and is covered by the chain once
// it is set.
package facts_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// factsIdentity is a well-formed identity of the facts group, which
// facts.gather belongs to.
var factsIdentity = wire.CollectionIdentity{Group: "facts", Version: "0.4.0", Manifests: "sha256:" + strings.Repeat("ab", 32)}

// goldenProvenance is a provenance with every field this package had before
// Phase 136b set, so the golden hash covers each of them.
func goldenProvenance() facts.Provenance {
	return facts.Provenance{
		Job: "job-7", Run: "run-3", Node: "tasks[1]", Method: "facts.gather",
		Attempt: 2, Runner: "runner-a",
		Order: facts.Order{Source: "lease:e1", Token: 41},
	}
}

// goldenChainHash computes the chain hash of one fixed value under by.
func goldenChainHash(t *testing.T, by facts.Provenance) string {
	t.Helper()
	at := time.Date(2026, 11, 8, 9, 30, 15, 123456789, time.UTC)
	hash, err := facts.Hash("os.kernel.release", "6.18.33")
	if err != nil {
		t.Fatal(err)
	}
	chain, err := facts.ChainHash("edge-1", "9f2c0e6b1a7d", "os.kernel.release", hash, at, by)
	if err != nil {
		t.Fatal(err)
	}
	return chain
}

// TestChainHashUnchangedByZeroIdentity: a provenance with no Collection
// identity hashes exactly as it did before the field existed. The literal
// was computed on the commit before Phase 136b's facts change (9d031fe0),
// so every chain a 0.4.0 store holds from before it still verifies. It fails
// if the identity's key is ever written for a zero identity.
func TestChainHashUnchangedByZeroIdentity(t *testing.T) {
	got := goldenChainHash(t, goldenProvenance())
	const want = "8eb767393059943064bd18c1a1b054bb336f2b554531ad2ab93fd3ae6242dce4"
	if got != want {
		t.Fatalf("a provenance with no Collection identity hashes to %s, and before Phase 136b it hashed to %s: every stored chain would break", got, want)
	}
}

// TestChainHashCoversTheIdentity: once set, the identity is part of what the
// chain covers, so editing which Collection a stored value names breaks the
// chain at that value.
func TestChainHashCoversTheIdentity(t *testing.T) {
	zero := goldenChainHash(t, goldenProvenance())
	by := goldenProvenance()
	by.Collection = factsIdentity
	with := goldenChainHash(t, by)
	if with == zero {
		t.Fatal("a provenance with a Collection identity hashes as one without: the chain does not cover it")
	}
	by.Collection.Manifests = "sha256:" + strings.Repeat("cd", 32)
	if goldenChainHash(t, by) == with {
		t.Fatal("two identities with different manifest digests hash alike: the chain does not cover the digest")
	}
}

// TestProvenance_CheckCollection: what the engine writes passes, and an
// identity no build writes for its method is refused by name.
func TestProvenance_CheckCollection(t *testing.T) {
	program := wire.CollectionIdentity{Group: "netbox-sync", Program: true, Manifests: factsIdentity.Manifests}
	for _, c := range []struct {
		name   string
		method string
		id     wire.CollectionIdentity
		ok     bool
	}{
		{"no identity", "facts.gather", wire.CollectionIdentity{}, true},
		{"no identity and no method", "", wire.CollectionIdentity{}, true},
		{"the method's own group", "facts.gather", factsIdentity, true},
		{"the longest group", "virt.clab.lab.deploy", wire.CollectionIdentity{Group: "virt.clab", Version: "0.4.0", Manifests: factsIdentity.Manifests}, true},
		{"a program's method", "netbox.devices", program, true},
		{"another group", "file.copy", factsIdentity, false},
		{"a shorter group", "virt.clab.lab.deploy", wire.CollectionIdentity{Group: "virt", Version: "0.4.0", Manifests: factsIdentity.Manifests}, false},
		{"a group for a method in none", "netbox.devices", wire.CollectionIdentity{Group: "netbox", Version: "0.4.0", Manifests: factsIdentity.Manifests}, false},
		{"a program for a first-party method", "facts.gather", program, false},
		{"no method", "", factsIdentity, false},
		{"a short digest", "facts.gather", wire.CollectionIdentity{Group: "facts", Manifests: "sha256:abc"}, false},
		{"a program with a version", "netbox.devices", wire.CollectionIdentity{Group: "netbox-sync", Program: true, Version: "1.0.0", Manifests: factsIdentity.Manifests}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := facts.Provenance{Method: c.method, Collection: c.id}.CheckCollection()
			if c.ok != (err == nil) {
				t.Fatalf("CheckCollection(%q, %+v) = %v, want ok %v", c.method, c.id, err, c.ok)
			}
			if err != nil && !errors.Is(err, facts.ErrCollectionIdentity) {
				t.Fatalf("CheckCollection's refusal %v is not ErrCollectionIdentity", err)
			}
		})
	}
}

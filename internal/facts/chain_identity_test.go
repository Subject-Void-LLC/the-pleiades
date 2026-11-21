// Tests that the Collection identity on a provenance (Phase 136b) leaves what
// a chain link covers unchanged when it is zero, and is covered by the chain
// once it is set; and that the keyed link itself does not move.
package facts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
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

// goldenAt is the fixed time the golden links cover.
var goldenAt = time.Date(2026, 11, 8, 9, 30, 15, 123456789, time.UTC)

// goldenHash is the fact hash of the one fixed value the golden links cover.
func goldenHash(t *testing.T) string {
	t.Helper()
	hash, err := facts.Hash("os.kernel.release", "6.18.33")
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// goldenChainHash computes the chain hash of one fixed value under by, keyed
// under factstest's secret.
func goldenChainHash(t *testing.T, by facts.Provenance) string {
	t.Helper()
	chain, err := factstest.Keys().ChainHash("edge-1", "9f2c0e6b1a7d", "os.kernel.release", goldenHash(t), goldenAt, by)
	if err != nil {
		t.Fatal(err)
	}
	return chain
}

// TestChainInputUnchangedByZeroIdentity: a provenance with no Collection
// identity is covered exactly as it was before the field existed. The literal
// is the unkeyed chain hash computed on the commit before Phase 136b's facts
// change (9d031fe0), which was SHA-256 over exactly the bytes a link is now
// keyed over (chainInput), so it still pins them. It fails if the identity's
// key is ever written for a zero identity, or the input's encoding moves.
func TestChainInputUnchangedByZeroIdentity(t *testing.T) {
	input, err := facts.ChainInput("edge-1", "9f2c0e6b1a7d", "os.kernel.release", goldenHash(t), goldenAt, goldenProvenance())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(input)
	const want = "8eb767393059943064bd18c1a1b054bb336f2b554531ad2ab93fd3ae6242dce4"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("what a link covers for a provenance with no Collection identity hashes to %s, and before Phase 136b it hashed to %s: its encoding moved", got, want)
	}
}

// TestChainHashIsFrozen: the keyed link of one fixed value under factstest's
// secret is a literal, computed when the chain was first keyed. It fails if
// the chain key's label or derivation, or the MAC over the input, ever moves:
// every chain a store holds would then break, on both tiers.
func TestChainHashIsFrozen(t *testing.T) {
	const want = "b9ebfb900ec2ec7050e6c1c99cd34ce5a0737e44990f937e97b61323b9e87198"
	if got := goldenChainHash(t, goldenProvenance()); got != want {
		t.Fatalf("the golden link is %s, want %s: every stored chain would break", got, want)
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

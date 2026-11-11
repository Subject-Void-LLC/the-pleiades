// Package facts_test: a Collection identity another process made is held to
// wire's frozen Deliverable rule, never to Valid, so whether the Controller
// keeps a Runner's identity cannot depend on the toolchain either was built
// with; the process that makes an identity still holds it to Valid.
package facts_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestDeliveredIdentityKeepsGroupOutsideThisToolchainsTables: a program's
// group holding a code point this toolchain has not assigned, so Valid
// refuses it here, is what a newer Runner's group holding a newly assigned
// letter looks like to this build. Delivered, it is kept; made here, it is
// refused.
func TestDeliveredIdentityKeepsGroupOutsideThisToolchainsTables(t *testing.T) {
	id := wire.CollectionIdentity{Group: "probe" + string(firstUnassigned(t)), Program: true, Manifests: factsIdentity.Manifests}
	if id.Shape() != nil || id.Valid() == nil {
		t.Fatal("the fixture must pass Shape and fail Valid")
	}
	by := facts.Provenance{Method: "probe.gather", Collection: id}
	if err := by.CheckDeliveredCollection(); err != nil {
		t.Errorf("a delivered identity only this toolchain's tables refuse was refused: %v", err)
	}
	if err := by.CheckCollection(); !errors.Is(err, facts.ErrCollectionIdentity) {
		t.Errorf("an identity this build would make with a group Valid refuses: CheckCollection gave %v", err)
	}
}

// TestDeliveredIdentityRefusalDoesNotEchoGroup: a group holding a direction
// override is still refused when delivered, and the refusal repeats none of
// the group.
func TestDeliveredIdentityRefusalDoesNotEchoGroup(t *testing.T) {
	const marker = "zq9marker"
	for _, r := range []rune{0x202E, 0x200B, 0x2028, 0x3164, 0xE0041, 0xE000, 0xFE0F} {
		id := wire.CollectionIdentity{Group: marker + string(r) + marker, Program: true, Manifests: factsIdentity.Manifests}
		err := facts.Provenance{Method: "probe.gather", Collection: id}.CheckDeliveredCollection()
		if !errors.Is(err, facts.ErrCollectionIdentity) {
			t.Errorf("U+%04X: CheckDeliveredCollection gave %v, want it refused", r, err)
			continue
		}
		if strings.Contains(err.Error(), marker) || strings.ContainsRune(err.Error(), r) {
			t.Errorf("U+%04X: the refusal repeats the group: %q", r, err)
		}
	}
}

// TestCollectionRoutingRepeatsNothingUnbounded: how a delivered identity
// disagrees with this build's routing is said at info on the Controller,
// naming the batch's method and group, which a Runner can make a megabyte
// long and fill with direction overrides. Each is named bounded and escaped.
func TestCollectionRoutingRepeatsNothingUnbounded(t *testing.T) {
	override := string(rune(0x202E))
	huge := strings.Repeat("zq9"+override, 1<<20/6)
	for name, by := range map[string]facts.Provenance{
		"a group for a method in none": {Method: "acme." + huge, Collection: wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: factsIdentity.Manifests}},
		"another group":                {Method: "facts.gather", Collection: wire.CollectionIdentity{Group: huge, Version: "0.4.0", Manifests: factsIdentity.Manifests}},
	} {
		err := by.CollectionRouting()
		if !errors.Is(err, facts.ErrCollectionIdentity) {
			t.Errorf("%s: CollectionRouting gave %v, want a difference", name, err)
			continue
		}
		if len(err.Error()) > 1024 || strings.Contains(err.Error(), override) {
			t.Errorf("%s: the difference repeats %d bytes: %.200q", name, len(err.Error()), err.Error())
		}
	}
}

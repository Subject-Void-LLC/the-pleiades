package capability_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// resolvesReference is an independent reimplementation of Resolves, built
// only from exported API (Lookup), used as a control query per this
// project's own cross-validation discipline: a test that reimplements the
// invariant a different way catches a bug the real implementation and its
// own test would otherwise agree on by sharing the same mistake.
func resolvesReference(declared map[capability.Name]struct{}, required capability.Name) bool {
	if _, ok := declared[required]; ok {
		return true
	}
	for name := range declared {
		seen := map[capability.Name]bool{}
		cur := name
		for {
			d, ok := capability.Lookup(cur)
			if !ok || d.Parent == "" {
				break
			}
			if d.Parent == required {
				return true
			}
			if seen[d.Parent] {
				break
			}
			seen[d.Parent] = true
			cur = d.Parent
		}
	}
	return false
}

// init registers a deliberately cyclic pair of test-only capabilities
// (never part of the real blessed vocabulary) so
// TestIsAncestor_CycleGuardTerminates can prove isAncestor's cycle guard
// actually fires, rather than trusting it by inspection alone. Registered
// in init, not inside the test body, so it runs exactly once regardless
// of -count.
func init() {
	capability.Register(capability.Descriptor{
		Name:   capability.Name("testCycleA"),
		Parent: capability.Name("testCycleB"),
		Assert: func(any) bool { return false },
	})
	capability.Register(capability.Descriptor{
		Name:   capability.Name("testCycleB"),
		Parent: capability.Name("testCycleA"),
		Assert: func(any) bool { return false },
	})
}

// TestIsAncestor_CycleGuardTerminates proves the cycle guard fires: a
// Parent chain that loops back on itself must terminate (returning false
// for an unrelated real capability) instead of hanging.
func TestIsAncestor_CycleGuardTerminates(t *testing.T) {
	declared := declaredSet(capability.Name("testCycleA"))
	if capability.Resolves(declared, capability.NameLinux) {
		t.Error("expected a cyclic Parent chain to never resolve an unrelated real capability")
	}
}

func declaredSet(names ...capability.Name) map[capability.Name]struct{} {
	out := make(map[capability.Name]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// TestAllParentsAreRegistered guards against a typo'd Parent string:
// without this, a dangling Parent silently truncates the hierarchy walk
// instead of failing anything.
func TestAllParentsAreRegistered(t *testing.T) {
	for name, d := range capability.All() {
		if d.Parent == "" {
			continue
		}
		if _, ok := capability.Lookup(d.Parent); !ok {
			t.Errorf("capability %q declares Parent %q, which is not registered", name, d.Parent)
		}
	}
}

// TestNoCapabilityAcceptsEmptyStruct proves every registered Assert is a
// real structural check, never a trivial "always true": struct{}{}
// implements no capability interface in this package, so every one of
// them must reject it.
func TestNoCapabilityAcceptsEmptyStruct(t *testing.T) {
	for name := range capability.All() {
		if capability.Implements(struct{}{}, name) {
			t.Errorf("capability %q accepted an empty struct -- Assert is not a real structural check", name)
		}
	}
}

func TestResolves_DirectMatch(t *testing.T) {
	declared := declaredSet(capability.NameApt)
	if !capability.Resolves(declared, capability.NameApt) {
		t.Error("expected a directly declared capability to resolve")
	}
}

func TestResolves_SingleHopAncestor(t *testing.T) {
	declared := declaredSet(capability.NameApt)

	if !capability.Resolves(declared, capability.NamePackageManager) {
		t.Error("expected AptCapable to resolve its parent PackageManagerCapable")
	}
	if capability.Resolves(declared, capability.NameDnf) {
		t.Error("expected AptCapable to NOT resolve its sibling DnfCapable")
	}
}

func TestResolves_MultiHopAncestor(t *testing.T) {
	declared := declaredSet(capability.NameFirewalld)

	if !capability.Resolves(declared, capability.NameSystemd) {
		t.Error("expected FirewalldCapable to resolve its parent SystemdCapable (1 hop)")
	}
	if !capability.Resolves(declared, capability.NameServiceManager) {
		t.Error("expected FirewalldCapable to resolve its grandparent ServiceManagerCapable (2 hops)")
	}
	if capability.Resolves(declared, capability.NameWindowsService) {
		t.Error("expected FirewalldCapable to NOT resolve its uncle WindowsServiceCapable")
	}
}

func TestResolves_EmptyDeclaredSet(t *testing.T) {
	if capability.Resolves(nil, capability.NamePackageManager) {
		t.Error("expected an empty declared set to never resolve anything")
	}
}

func TestResolves_UnregisteredRequiredNeverResolves(t *testing.T) {
	declared := declaredSet(capability.NameApt)
	if capability.Resolves(declared, capability.Name("NotARealCapability")) {
		t.Error("expected an unregistered required name to never resolve")
	}
}

func TestResolves_UnregisteredDeclaredDoesNotPanic(t *testing.T) {
	declared := declaredSet(capability.Name("NotARealCapability"))
	if capability.Resolves(declared, capability.NamePackageManager) {
		t.Error("expected an unregistered declared name to never resolve anything real")
	}
}

// TestResolves_MatchesReferenceAcrossVocabulary cross-checks every
// registered name against every other, the exhaustive (non-fuzzed)
// counterpart to FuzzResolves.
func TestResolves_MatchesReferenceAcrossVocabulary(t *testing.T) {
	all := capability.All()
	for leaf := range all {
		declared := declaredSet(leaf)
		for required := range all {
			got := capability.Resolves(declared, required)
			want := resolvesReference(declared, required)
			if got != want {
				t.Errorf("Resolves(%q declared, %q required) = %v, want %v", leaf, required, got, want)
			}
		}
	}
}

// packageManagerDevice implements AptCapable structurally, without ever
// mentioning PackageManagerCapable by name -- proving interface embedding
// alone makes Implements resolve the parent capability for free, with no
// hierarchy-walking code needed on the structural side.
type packageManagerDevice struct{}

func (packageManagerDevice) PackageManagerName() string { return "apt" }
func (packageManagerDevice) AptSourcesList() string     { return "/etc/apt/sources.list" }

func TestImplements_StructuralEmbeddingResolvesParentForFree(t *testing.T) {
	dev := packageManagerDevice{}
	if !capability.Implements(dev, capability.NameApt) {
		t.Fatal("expected packageManagerDevice to implement AptCapable")
	}
	if !capability.Implements(dev, capability.NamePackageManager) {
		t.Error("expected a device implementing AptCapable to also structurally implement PackageManagerCapable, via interface embedding")
	}
	if capability.Implements(dev, capability.NameDnf) {
		t.Error("expected packageManagerDevice to NOT implement the unrelated DnfCapable")
	}
}

// TestDeclaresVsImplements_NeitherSideTrustedAlone proves the granularity
// decision's own claim: a device that declares (data) a capability but
// does not structurally implement it is not satisfied, and a device that
// structurally implements a capability but never declared it is not
// satisfied either.
func TestDeclaresVsImplements_NeitherSideTrustedAlone(t *testing.T) {
	dev := packageManagerDevice{}

	// Declared, not implemented: declare DnfCapable (data) on a device
	// that structurally only implements AptCapable.
	declaredOnly := declaredSet(capability.NameDnf)
	if capability.Resolves(declaredOnly, capability.NameDnf) && capability.Implements(dev, capability.NameDnf) {
		t.Error("a device declaring DnfCapable but not structurally implementing it must not be satisfied")
	}

	// Implemented, not declared: dev structurally implements AptCapable,
	// but nothing declared it.
	if capability.Implements(dev, capability.NameApt) && capability.Resolves(nil, capability.NameApt) {
		t.Error("a device structurally implementing AptCapable but never declaring it must not be satisfied")
	}
}

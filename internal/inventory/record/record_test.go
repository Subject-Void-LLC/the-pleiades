package record_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// aptDevice is a synthetic device type embedding *record.Base and
// structurally implementing capability.AptCapable, the same shape
// Router/Server use in production. It exists to prove the Release Gate's
// worked example -- a task requiring the broad PackageManagerCapable
// against a device declaring only the narrower AptCapable resolves true
// at the capability layer this phase owns -- without needing a real
// generated device type (Phase 33/34 territory).
type aptDevice struct {
	*record.Base
}

func (aptDevice) PackageManagerName() string { return "apt" }
func (aptDevice) AptSourcesList() string     { return "/etc/apt/sources.list" }

func (d aptDevice) HasCapability(name capability.Name) bool {
	return d.Declares(name) && capability.Implements(d, name)
}

func newAptDevice(caps []capability.Name) aptDevice {
	base := record.NewBase(record.Record{ID: "dev1", Name: "dev1"}, caps)
	return aptDevice{Base: base}
}

func TestBase_Declares_DirectMatch(t *testing.T) {
	dev := newAptDevice([]capability.Name{capability.NameApt})
	if !dev.Declares(capability.NameApt) {
		t.Error("expected a directly declared capability to be Declared")
	}
}

// TestBase_HasCapability_HierarchyResolution is the Release Gate's own
// worked example: a task calling pkg.install against a device declaring
// only AptCapable resolves to apt at plan time (IMPLEMENTATION.md Phase
// 32). At this layer that means HasCapability(PackageManagerCapable)
// resolves true through Declares' Parent-chain walk plus Implements'
// structural interface embedding, while an unrelated sibling
// (DnfCapable) stays false.
func TestBase_HasCapability_HierarchyResolution(t *testing.T) {
	dev := newAptDevice([]capability.Name{capability.NameApt})

	if !dev.HasCapability(capability.NamePackageManager) {
		t.Error("expected a device declaring only AptCapable to satisfy the broader PackageManagerCapable")
	}
	if dev.HasCapability(capability.NameDnf) {
		t.Error("expected a device declaring only AptCapable to NOT satisfy the unrelated sibling DnfCapable")
	}
}

func TestBase_Declares_EmptyCapabilitySet(t *testing.T) {
	dev := newAptDevice(nil)
	if dev.Declares(capability.NameApt) {
		t.Error("expected an empty capability set to declare nothing")
	}
	if dev.HasCapability(capability.NamePackageManager) {
		t.Error("expected an empty capability set to satisfy nothing, including via the hierarchy")
	}
}

func TestBase_Capabilities_ReturnsDeclaredSet(t *testing.T) {
	dev := newAptDevice([]capability.Name{capability.NameApt, capability.NameSSHTransport})
	got := dev.Capabilities()

	want := map[capability.Name]bool{capability.NameApt: true, capability.NameSSHTransport: true}
	if len(got) != len(want) {
		t.Fatalf("Capabilities() = %v, want exactly %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("Capabilities() included unexpected %q", name)
		}
	}
}

func TestNewBase_CapabilitiesFieldIsNotConsultedDirectly(t *testing.T) {
	// NewBase's caps parameter, not rec.Capabilities, determines what a
	// Base declares -- callers (Router/Server) decide how the two combine
	// (Phase 32's constructors union them; this proves NewBase itself does
	// no implicit merging of its own).
	rec := record.Record{ID: "dev1", Capabilities: []capability.Name{capability.NameDnf}}
	base := record.NewBase(rec, []capability.Name{capability.NameApt})

	if base.Declares(capability.NameDnf) {
		t.Error("expected NewBase to ignore rec.Capabilities and use only its own caps parameter")
	}
	if !base.Declares(capability.NameApt) {
		t.Error("expected NewBase's explicit caps parameter to be what Declares reflects")
	}
}

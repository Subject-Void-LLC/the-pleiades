package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

func TestResolveHostCapabilities_ExplicitTypeReturnsNil(t *testing.T) {
	h := inventory.HostSpec{Name: "web1", Type: "linux_server"}
	caps, err := inventory.ResolveHostCapabilities(h, classification.DefaultRuleSet())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps != nil {
		t.Errorf("expected nil capabilities for an explicit Type, got %v", caps)
	}
}

// TestResolveHostCapabilities_TypeShortCircuitsUnresolvableClassify mirrors
// TestHydrateHosts_TypeWinsOverClassify: an unresolvable Classify path
// paired with an explicit Type must not error, for capabilities any more
// than for Type -- Classify is provenance only once Type is present.
func TestResolveHostCapabilities_TypeShortCircuitsUnresolvableClassify(t *testing.T) {
	h := inventory.HostSpec{Name: "web1", Type: "linux_server", Classify: []string{"totally_unresolvable_path"}}
	caps, err := inventory.ResolveHostCapabilities(h, classification.DefaultRuleSet())
	if err != nil {
		t.Fatalf("unexpected error (Type should have won over an unresolvable Classify): %v", err)
	}
	if caps != nil {
		t.Errorf("expected nil capabilities, got %v", caps)
	}
}

func TestResolveHostCapabilities_NoTypeNoClassifyReturnsNil(t *testing.T) {
	h := inventory.HostSpec{Name: "web1"}
	caps, err := inventory.ResolveHostCapabilities(h, classification.DefaultRuleSet())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps != nil {
		t.Errorf("expected nil capabilities, got %v", caps)
	}
}

func TestResolveHostCapabilities_ClassifyOnlyResolvesRootCapabilities(t *testing.T) {
	h := inventory.HostSpec{Name: "web1", Classify: []string{"linux_server"}}
	caps, err := inventory.ResolveHostCapabilities(h, classification.DefaultRuleSet())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[capability.Name]bool{capability.NameLinux: true, capability.NameSSHTransport: true}
	if len(caps) != len(want) {
		t.Fatalf("capabilities = %v, want exactly %v", caps, want)
	}
	for _, c := range caps {
		if !want[c] {
			t.Errorf("unexpected capability %q", c)
		}
	}
}

// TestResolveHostCapabilities_DebianFamilyUnionsAptCapable is the
// checklist's own worked example made concrete through the real
// production hydration seam: classifying into debian_family adds
// AptCapable on top of what the linux_server root already granted.
func TestResolveHostCapabilities_DebianFamilyUnionsAptCapable(t *testing.T) {
	h := inventory.HostSpec{Name: "web1", Classify: []string{"linux_server", "debian_family", "ubuntu"}}
	caps, err := inventory.ResolveHostCapabilities(h, classification.DefaultRuleSet())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[capability.Name]bool{
		capability.NameLinux:        true,
		capability.NameSSHTransport: true,
		capability.NameApt:          true,
	}
	if len(caps) != len(want) {
		t.Fatalf("capabilities = %v, want exactly %v", caps, want)
	}
	for _, c := range caps {
		if !want[c] {
			t.Errorf("unexpected capability %q", c)
		}
	}
}

func TestResolveHostCapabilities_UnresolvableClassifyErrors(t *testing.T) {
	h := inventory.HostSpec{Name: "mystery", Classify: []string{"totally_unresolvable_path"}}
	if _, err := inventory.ResolveHostCapabilities(h, classification.DefaultRuleSet()); err == nil {
		t.Error("expected an error for a Classify path DefaultRuleSet has no rule for")
	}
}

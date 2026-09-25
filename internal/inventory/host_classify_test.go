package inventory_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
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
		capability.NamePosixAccount: true,
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

// TestResolveHostCapabilities_ClassifyBesideType covers what add-host
// writes, a Type and the Classify path it came from: the path's
// capabilities count when it resolves to that same type, and a path that
// resolves elsewhere, or not at all, contributes nothing without an error
// (FAILURE_PATTERNS 344).
func TestResolveHostCapabilities_ClassifyBesideType(t *testing.T) {
	rs := classification.DefaultRuleSet()
	for _, tc := range []struct {
		name string
		h    inventory.HostSpec
		want []capability.Name
	}{
		{"matching type", inventory.HostSpec{Name: "a", Type: "linux_server", Classify: []string{"linux_server", "debian_family"}}, []capability.Name{capability.NameLinux, capability.NameSSHTransport, capability.NameApt, capability.NamePosixAccount}},
		{"another type's path", inventory.HostSpec{Name: "b", Type: "cisco_router", Classify: []string{"linux_server", "debian_family"}}, nil},
		{"unresolvable path", inventory.HostSpec{Name: "c", Type: "linux_server", Classify: []string{"no_such_family"}}, nil},
		{"no path", inventory.HostSpec{Name: "d", Type: "linux_server"}, nil},
	} {
		got, err := inventory.ResolveHostCapabilities(tc.h, rs)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !sameNames(got, tc.want) {
			t.Errorf("%s: capabilities %v, want %v", tc.name, got, tc.want)
		}
	}
}

// sameNames reports whether a and b hold the same names, in any order.
func sameNames(a, b []capability.Name) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[capability.Name]int{}
	for _, n := range a {
		seen[n]++
	}
	for _, n := range b {
		seen[n]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}

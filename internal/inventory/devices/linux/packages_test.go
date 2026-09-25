// Tests that a linux_server really satisfies the package manager, firewall
// and account capabilities it declares, built the way a real device is:
// classified by the default rule set, then constructed.
package linux_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// classified builds a linux_server classified along path, with props.
func classified(t *testing.T, props map[string]inventory.PropertyValue, path ...string) inventory.InventoryItem {
	t.Helper()
	res, err := classification.DefaultRuleSet().Classify(append([]string{"linux_server"}, path...))
	if err != nil {
		t.Fatal(err)
	}
	item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server", Capabilities: res.Value.Capabilities, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// TestServer_SatisfiesWhatItDeclares: each capability is both declared
// where it is true and structurally satisfied, so HasCapability answers
// true, and nowhere else.
func TestServer_SatisfiesWhatItDeclares(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  []string
		props map[string]inventory.PropertyValue
		want  map[capability.Name]bool
		pm    string
	}{
		{name: "unclassified", want: map[capability.Name]bool{
			capability.NamePosixAccount: false, capability.NameApt: false, capability.NameDnf: false,
			capability.NamePackageManager: false, capability.NameFirewalld: false,
		}},
		{name: "debian family", path: []string{"debian_family"}, pm: "apt", want: map[capability.Name]bool{
			capability.NameApt: true, capability.NamePackageManager: true, capability.NameDnf: false, capability.NamePosixAccount: true,
		}},
		{name: "rhel family", path: []string{"rhel_family"}, pm: "dnf", want: map[capability.Name]bool{
			capability.NameDnf: true, capability.NamePackageManager: true, capability.NameApt: false, capability.NamePosixAccount: true,
		}},
		{name: "firewalld declared", props: map[string]inventory.PropertyValue{linux.FirewalldProperty: true}, want: map[capability.Name]bool{
			capability.NameFirewalld: true, capability.NameSystemd: true,
		}},
		{name: "firewalld declared false", props: map[string]inventory.PropertyValue{linux.FirewalldProperty: false}, want: map[capability.Name]bool{
			capability.NameFirewalld: false,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := classified(t, tc.props, tc.path...)
			for c, want := range tc.want {
				if got := item.HasCapability(c); got != want {
					t.Errorf("HasCapability(%s) = %v, want %v", c, got, want)
				}
			}
			if pm, ok := item.(capability.PackageManagerCapable); !ok || pm.PackageManagerName() != tc.pm {
				t.Errorf("PackageManagerName = %q, want %q", pm.PackageManagerName(), tc.pm)
			}
		})
	}
}

// TestServer_FirewalldPropertyMustBeBoolean refuses a value that could be
// read either way.
func TestServer_FirewalldPropertyMustBeBoolean(t *testing.T) {
	if _, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server", Properties: map[string]inventory.PropertyValue{linux.FirewalldProperty: "yes"}}); err == nil {
		t.Fatal(`firewalld: "yes" was accepted`)
	}
}

// TestServer_AccessorDefaultsAndOverrides covers each accessor's default
// and its property override.
func TestServer_AccessorDefaultsAndOverrides(t *testing.T) {
	type accessors interface {
		AptSourcesList() string
		DnfRepoDir() string
		FirewalldZone() string
		PasswdPath() string
	}
	plain := classified(t, nil).(accessors)
	set := classified(t, map[string]inventory.PropertyValue{
		"apt_sources_list": "/srv/sources.list", "dnf_repo_dir": "/srv/repos", "firewalld_zone": "internal", "passwd_path": "/srv/passwd",
	}).(accessors)
	for _, tc := range []struct{ got, want string }{
		{plain.AptSourcesList(), "/etc/apt/sources.list"}, {set.AptSourcesList(), "/srv/sources.list"},
		{plain.DnfRepoDir(), "/etc/yum.repos.d"}, {set.DnfRepoDir(), "/srv/repos"},
		{plain.FirewalldZone(), "public"}, {set.FirewalldZone(), "internal"},
		{plain.PasswdPath(), "/etc/passwd"}, {set.PasswdPath(), "/srv/passwd"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

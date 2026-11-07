// Tests for the static first-party lists (Phase 136a).
package collection_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestFirstPartyGroupOf_LongestPrefixAtADot: a method belongs to the longest
// group prefix that ends at a dot, so a dotted group wins over its namespace,
// and a name that only begins like a namespace belongs to none.
func TestFirstPartyGroupOf_LongestPrefixAtADot(t *testing.T) {
	for fqcn, want := range map[string]string{
		"virt.clab.lab.deploy": "virt.clab",
		"net.batfish.check":    "net.batfish",
		"net.ios.config":       "net",
		"exec.winrm.shell":     "exec.winrm",
		"exec.command":         "exec",
		"svc.windows.start":    "svc.windows",
		"facts.gather":         "facts",
		"netbox.devices":       "",
		"virtual.x":            "",
		"net":                  "",
	} {
		g, ok := collection.FirstPartyGroupOf(fqcn)
		if want == "" {
			if ok {
				t.Errorf("%s belongs to %q, want no group", fqcn, g.Name)
			}
			continue
		}
		if !ok || g.Name != want {
			t.Errorf("%s belongs to %q (%v), want %q", fqcn, g.Name, ok, want)
		}
	}
}

// TestFirstPartyNamespaces_DoNotDependOnWhatIsLinked: every group's
// namespace, Ansible's, and each reserved fact family's are reserved, in a
// package that links no Collection at all, which is the point of a static
// list.
func TestFirstPartyNamespaces_DoNotDependOnWhatIsLinked(t *testing.T) {
	got := collection.FirstPartyNamespaces()
	for _, ns := range []string{"container", "virt", "kubernetes", "ansible", "pleiades", "os", "hardware", "service_state"} {
		if !slices.Contains(got, ns) {
			t.Errorf("%q is not reserved: %v", ns, got)
		}
	}
	if slices.Contains(got, "netbox") || slices.Contains(got, "acme") {
		t.Errorf("a namespace no group or family names is reserved: %v", got)
	}
	if !slices.IsSorted(got) {
		t.Errorf("not sorted: %v", got)
	}
}

// TestRegister_RefusesAnExternalMethodInAFirstPartyNamespace: Register is the
// authority, so a method with a Provider in a first-party namespace is
// refused with no loader involved, while one in its own namespace is
// registered.
func TestRegister_RefusesAnExternalMethodInAFirstPartyNamespace(t *testing.T) {
	defer collection.SnapshotForTest()()
	external := func(name string) collection.Descriptor {
		return collection.Descriptor{
			Name:     name,
			Manifest: collection.Manifest{Status: collection.StatusDeclared},
			Provider: &collection.Provider{Program: "acme-tools", Digest: strings.Repeat("a", 64)},
		}
	}
	for _, name := range []string{"container.image.pull", "Container.image.pull", "kubernetes.apply", "os.release", "ansible.builtin.copy"} {
		if err := collection.Register(external(name)); err == nil || !strings.Contains(err.Error(), "reserved for first-party Collections") {
			t.Errorf("Register(%s) = %v, want a refusal naming the first-party reservation", name, err)
		}
	}
	if err := collection.Register(external("acme.widget.make")); err != nil {
		t.Errorf("Register(acme.widget.make) = %v, want it registered", err)
	}
}

// TestReservedFact_TheFloorCoversEveryFirstPartyNamespace: a fact under a
// first-party namespace is reserved even where no built-in declares it, and
// a namespace that only begins like one is not.
func TestReservedFact_TheFloorCoversEveryFirstPartyNamespace(t *testing.T) {
	for _, key := range []string{"edge.site", "Container.x", "kubernetes.version", "os.family"} {
		if _, reserved := collection.ReservedFact(collection.FactSpec{Key: key}); !reserved {
			t.Errorf("fact %s is not reserved", key)
		}
	}
	for _, key := range []string{"netbox.site", "acme.version"} {
		if by, reserved := collection.ReservedFact(collection.FactSpec{Key: key}); reserved {
			t.Errorf("fact %s is reserved by %s", key, by)
		}
	}
}

// TestFirstPartyRoutes_EveryMethodHasAGroup: the routing table names each
// method's group, and refuses a method that belongs to none.
func TestFirstPartyRoutes_EveryMethodHasAGroup(t *testing.T) {
	routes, err := collection.FirstPartyRoutes(map[string]collection.Manifest{"file.copy": {}, "virt.cml.node.start": {}})
	if err != nil || routes["file.copy"] != "file" || routes["virt.cml.node.start"] != "virt.cml" {
		t.Fatalf("routes = %v, %v", routes, err)
	}
	if _, err := collection.FirstPartyRoutes(map[string]collection.Manifest{"acme.widget.make": {}}); err == nil {
		t.Error("a method in no first-party group was routed")
	}
}

// TestFactFamilyOwners_NamesTheGroupsThatFillEachPrefix: each floor prefix
// lists the groups whose methods declare facts inside it, and a prefix no
// method fills lists none.
func TestFactFamilyOwners_NamesTheGroupsThatFillEachPrefix(t *testing.T) {
	owners := collection.FactFamilyOwners(map[string]collection.Manifest{
		"facts.gather":   {Facts: []collection.FactSpec{{Key: "os.", Prefix: true}, {Key: "package.", Prefix: true}}},
		"net.ios.facts":  {Facts: []collection.FactSpec{{Key: "os.version"}}},
		"acme.not.first": {Facts: []collection.FactSpec{{Key: "os.x"}}},
	})
	if got := owners["os."]; !slices.Equal(got, []string{"facts", "net"}) {
		t.Errorf("os. owners = %v, want facts and net", got)
	}
	if got := owners["package."]; !slices.Equal(got, []string{"facts"}) {
		t.Errorf("package. owners = %v", got)
	}
	if got, ok := owners["ansible_"]; !ok || len(got) != 0 {
		t.Errorf("ansible_ owners = %v (%v), want present and empty", got, ok)
	}
}

// TestFirstPartyGroups_SortedAndACopy: the groups come back sorted, each once,
// with the core ones marked, and changing the result changes nothing stored.
func TestFirstPartyGroups_SortedAndACopy(t *testing.T) {
	groups := collection.FirstPartyGroups()
	names := make([]string, len(groups))
	core := map[string]bool{}
	for i, g := range groups {
		names[i] = g.Name
		core[g.Name] = g.Core
	}
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Fatalf("groups not sorted and unique: %v", names)
	}
	if !core["facts"] || !core["pleiades"] || core["file"] {
		t.Errorf("core marks = %v, want facts and pleiades only", core)
	}
	groups[0].Name = "changed"
	if collection.FirstPartyGroups()[0].Name == "changed" {
		t.Error("FirstPartyGroups returned the stored slice")
	}
}

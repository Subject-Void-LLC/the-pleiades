// Package collection: the first-party Collections, as a static list the core
// ships (Phase 136a, the Collection split's S1).
//
// Until now a namespace was reserved by what this binary links
// (BuiltinNamespaces): a build that did not link a group left its namespace
// claimable by any program in the collections directory. The list below
// reserves every first-party namespace whether or not its methods are linked,
// so the day a group ships as its own program (v0.5.0) nobody else can take
// its name first. It is data the core keeps, compared on every release by
// tools/schemacompat: it may grow on a minor release, never on a patch, since
// a newly reserved name refuses a program the release before loaded.
package collection

import (
	"fmt"
	"sort"
	"strings"
)

// FirstPartyGroup is one first-party Collection: the FQCN prefix its methods
// carry (a namespace such as "file", or a dotted group under one such as
// "virt.clab"), and whether the core keeps it in process rather than shipping
// it as a program of its own.
type FirstPartyGroup struct {
	// Name is the FQCN prefix: a method belongs to the group whose Name is
	// the longest prefix of its name ending at a dot (FirstPartyGroupOf).
	Name string `json:"name"`
	// Core is true for what stays in the core process when the rest moves
	// out: facts.gather and pleiades.builtin.* (PLAN.md Section 38, D3).
	Core bool `json:"core,omitempty"`
}

// firstPartyGroups is every first-party Collection, by the waves of
// `.SPECIFICATION/COLLECTION_SPLIT_GUIDE.md` section 6 (each wave's groups
// move together, each group is one program). kubernetes has no method yet
// (Phase 119g adds them) and is reserved here so nobody takes it first.
var firstPartyGroups = []FirstPartyGroup{
	// Core.
	{Name: "facts", Core: true},
	{Name: "pleiades", Core: true},
	// Wave 1.
	{Name: "archive"}, {Name: "build"}, {Name: "edge"}, {Name: "exec"}, {Name: "file"}, {Name: "fs"},
	{Name: "fw"}, {Name: "gate"}, {Name: "identity"}, {Name: "pkg"}, {Name: "scan"}, {Name: "svc"},
	{Name: "wait"},
	// Wave 2.
	{Name: "ai"}, {Name: "cloud.aws"}, {Name: "http"}, {Name: "kubernetes"}, {Name: "net.batfish"},
	{Name: "virt.clab"}, {Name: "virt.cml"},
	// Wave 3.
	{Name: "container"},
	// Wave 4.
	{Name: "db.pg"},
	// Wave 5.
	{Name: "exec.winrm"}, {Name: "svc.windows"}, {Name: "win"},
	// Wave 6.
	{Name: "net"},
	// Wave 7.
	{Name: "virt.fusion"}, {Name: "virt.vbox"},
}

// reservedOnlyNamespaces are namespaces no group fills that are reserved all
// the same: Ansible's, whose module names a migrating playbook uses and a third
// party must not impersonate.
var reservedOnlyNamespaces = []string{"ansible"}

// FirstPartyGroups returns every first-party group, sorted by name.
func FirstPartyGroups() []FirstPartyGroup {
	out := append([]FirstPartyGroup(nil), firstPartyGroups...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FirstPartyGroupOf returns the first-party group a method name belongs to:
// the group whose name is the longest prefix of fqcn that ends at a dot, so
// "virt.clab.lab.deploy" is virt.clab's and "net.batfish.check" net.batfish's,
// not net's. "netbox.x" belongs to none: a prefix counts only at a dot.
func FirstPartyGroupOf(fqcn string) (FirstPartyGroup, bool) {
	var found FirstPartyGroup
	ok := false
	for _, g := range firstPartyGroups {
		if strings.HasPrefix(fqcn, g.Name+".") && (!ok || len(g.Name) > len(found.Name)) {
			found, ok = g, true
		}
	}
	return found, ok
}

// FirstPartyNamespaces returns, sorted, every namespace (the first dotted part
// of a method name) an external Collection may not use: each first-party
// group's, Ansible's, and the namespace of every reserved fact family, since
// a program named after one would be told its facts belong under a family it
// may not declare into (OwnFactName). Unlike BuiltinNamespaces it does not
// depend on what this binary links.
func FirstPartyNamespaces() []string {
	seen := map[string]bool{}
	for _, g := range firstPartyGroups {
		ns, _, _ := strings.Cut(g.Name, ".")
		seen[ns] = true
	}
	for _, ns := range reservedOnlyNamespaces {
		seen[ns] = true
	}
	for _, family := range ReservedFactFamilies {
		if ns, _, dotted := strings.Cut(family, "."); dotted {
			seen[ns] = true
		}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// IsFirstPartyNamespace reports whether ns is one of FirstPartyNamespaces.
func IsFirstPartyNamespace(ns string) bool {
	for _, n := range FirstPartyNamespaces() {
		if n == ns {
			return true
		}
	}
	return false
}

// FactFloor returns, sorted, the fact prefixes no external Collection may
// declare into: every reserved fact family and every first-party namespace as
// a "<ns>." prefix. An external program's facts go under its own namespace,
// which can never be a first-party one.
func FactFloor() []string {
	seen := map[string]bool{}
	for _, family := range ReservedFactFamilies {
		seen[family] = true
	}
	for _, ns := range FirstPartyNamespaces() {
		seen[ns+"."] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// FirstPartyRoutes maps each method in manifests (a build's own catalog) to
// the first-party group it belongs to: the routing table the core ships, so a
// runbook naming a method of a group this build does not include can be told
// which Collection holds it. A method in no group is an error: every built-in
// belongs to a first-party Collection.
func FirstPartyRoutes(manifests map[string]Manifest) (map[string]string, error) {
	routes := make(map[string]string, len(manifests))
	var missing []string
	for name := range manifests {
		g, ok := FirstPartyGroupOf(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		routes[name] = g.Name
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("collection: %s belong to no first-party group (pkg/collection firstPartyGroups)", strings.Join(missing, ", "))
	}
	return routes, nil
}

// FactFamilyOwners maps each prefix of the fact floor to the first-party
// groups whose methods in manifests declare a fact inside it, each list sorted
// (empty for a prefix no method fills yet, ansible_ among them). It is the map
// a later release holds each first-party program's declarations to (the
// split's S4). A declaration outside the floor would be dropped here, so
// internal/archtest holds every built-in declaration inside it.
func FactFamilyOwners(manifests map[string]Manifest) map[string][]string {
	owners := map[string]map[string]bool{}
	floor := FactFloor()
	for _, prefix := range floor {
		owners[prefix] = map[string]bool{}
	}
	for name, m := range manifests {
		g, ok := FirstPartyGroupOf(name)
		if !ok {
			continue
		}
		for _, f := range m.Facts {
			folded := FactSpec{Key: strings.ToLower(f.Key), Prefix: f.Prefix}
			for _, prefix := range floor {
				if folded.Key == prefix || overlaps(folded, FactSpec{Key: prefix, Prefix: true}) {
					owners[prefix][g.Name] = true
				}
			}
		}
	}
	out := make(map[string][]string, len(owners))
	for prefix, set := range owners {
		list := make([]string, 0, len(set))
		for g := range set {
			list = append(list, g)
		}
		sort.Strings(list)
		out[prefix] = list
	}
	return out
}

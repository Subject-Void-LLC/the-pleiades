// The static first-party lists (Phase 136a) held to the registry this build
// links: a namespace a built-in uses is always on the list, every built-in
// belongs to a first-party group, and the routing table and fact-family map
// the core ships are the ones the catalog gives.
package archtest

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// shippedFirstParty is the part of wellknown's first-party.json compared here.
type shippedFirstParty struct {
	Namespaces       []string            `json:"namespaces"`
	FactFloor        []string            `json:"fact_floor"`
	FactFamilyOwners map[string][]string `json:"fact_family_owners"`
	Methods          map[string]string   `json:"methods"`
}

// TestFirstPartyLists_MatchTheRegistry: the static lists cover the registry,
// and the generated document the core embeds says what the catalog says.
func TestFirstPartyLists_MatchTheRegistry(t *testing.T) {
	static := collection.FirstPartyNamespaces()
	for _, ns := range collection.BuiltinNamespaces() {
		if !slices.Contains(static, ns) {
			t.Errorf("built-in namespace %q is not on collection.FirstPartyNamespaces: a build without that group would leave it claimable", ns)
		}
	}
	var catalog map[string]collection.Manifest
	if err := json.Unmarshal(wellknown.ModuleCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	for name := range catalog {
		if _, ok := collection.Lookup(name); !ok {
			t.Errorf("%s is in the module catalog and not registered", name)
		}
	}
	routes, err := collection.FirstPartyRoutes(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var shipped shippedFirstParty
	if err := json.Unmarshal(wellknown.FirstParty, &shipped); err != nil {
		t.Fatalf("first-party.json: %v", err)
	}
	if !maps.Equal(shipped.Methods, routes) {
		t.Error("first-party.json's routing table is not the catalog's; run go run ./tools/gendocs")
	}
	if !slices.Equal(shipped.Namespaces, static) || !slices.Equal(shipped.FactFloor, collection.FactFloor()) {
		t.Error("first-party.json's namespaces or fact floor are not pkg/collection's; run go run ./tools/gendocs")
	}
	owners := collection.FactFamilyOwners(catalog)
	if !maps.EqualFunc(shipped.FactFamilyOwners, owners, slices.Equal[[]string]) {
		t.Error("first-party.json's fact family owners are not the catalog's; run go run ./tools/gendocs")
	}
	if got := owners["ansible_"]; len(got) != 0 {
		t.Errorf("ansible_ has owners %v; no method may declare an Ansible fact name", got)
	}
}

// TestFirstPartyGroups_EachHoldsAMethod: a group on the list with no method
// is a reservation nobody can explain later, so each must hold one, but for
// the groups reserved ahead of their methods, each named with why.
func TestFirstPartyGroups_EachHoldsAMethod(t *testing.T) {
	ahead := map[string]string{
		"kubernetes": "the cluster device type exists; its methods arrive with Phase 119g",
	}
	var catalog map[string]collection.Manifest
	if err := json.Unmarshal(wellknown.ModuleCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	filled := map[string]bool{}
	for name := range catalog {
		if g, ok := collection.FirstPartyGroupOf(name); ok {
			filled[g.Name] = true
		}
	}
	for _, g := range collection.FirstPartyGroups() {
		if !filled[g.Name] && ahead[g.Name] == "" {
			t.Errorf("first-party group %q holds no method; remove it or say why it is reserved ahead", g.Name)
		}
	}
}

// TestFirstPartyFacts_EveryBuiltInDeclarationIsInsideTheFloor: a built-in
// fact declared outside the floor would be dropped from the fact family map,
// and the day each first-party program is held to that map it would be refused
// for a fact it has always written.
func TestFirstPartyFacts_EveryBuiltInDeclarationIsInsideTheFloor(t *testing.T) {
	var catalog map[string]collection.Manifest
	if err := json.Unmarshal(wellknown.ModuleCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	// Against the floor alone: collection.ReservedFact also matches the
	// registry's own declarations, so every built-in would match itself.
	floor := collection.FactFloor()
	inside := func(key string) bool {
		key = strings.ToLower(key)
		for _, prefix := range floor {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return false
	}
	if inside("vlan.x") || !inside("OS.family") {
		t.Fatal("control: the floor check reads vlan.x as inside or OS.family as outside")
	}
	for _, name := range slices.Sorted(maps.Keys(catalog)) {
		for _, f := range catalog[name].Facts {
			if !inside(f.Key) {
				t.Errorf("%s declares fact %q outside the fact floor; put it under a reserved family or its own namespace", name, f.Key)
			}
		}
	}
}

// Every first-party group's Collection identity (Phase 136b) held to the
// module catalog the core embeds: a Controller registers no Collection and
// digests a group from the catalog, a Runner from its registry, and the two
// must agree or every group would read as changed between them.
package archtest

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// embeddedCatalog decodes the module catalog this build embeds.
func embeddedCatalog(t *testing.T) map[string]collection.Manifest {
	t.Helper()
	var catalog map[string]collection.Manifest
	if err := json.Unmarshal(wellknown.ModuleCatalog, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

// TestCatalogDigestMatchesRegistry: each first-party group's manifest-set
// digest taken from the embedded catalog is the one this build's registry
// gives (collection.GroupDigest), so the catalog is neither stale nor
// missing a registered built-in, and its JSON carries each manifest whole.
func TestCatalogDigestMatchesRegistry(t *testing.T) {
	// The registry alone answers GroupDigest here, as it does on a Runner.
	t.Cleanup(collection.CatalogForTest())
	byGroup := map[string]map[string]collection.Manifest{}
	for name, m := range embeddedCatalog(t) {
		g, ok := collection.FirstPartyGroupOf(name)
		if !ok {
			t.Errorf("%s is in the catalog and in no first-party group", name)
			continue
		}
		if byGroup[g.Name] == nil {
			byGroup[g.Name] = map[string]collection.Manifest{}
		}
		byGroup[g.Name][name] = m
	}
	compared := 0
	for _, g := range collection.FirstPartyGroups() {
		registry := collection.GroupManifests(g.Name)
		catalog := byGroup[g.Name]
		if len(registry) == 0 && len(catalog) == 0 {
			continue
		}
		if !slices.Equal(slices.Sorted(maps.Keys(registry)), slices.Sorted(maps.Keys(catalog))) {
			t.Errorf("group %s: the registry holds %v and the catalog %v; run go run ./tools/gendocs",
				g.Name, slices.Sorted(maps.Keys(registry)), slices.Sorted(maps.Keys(catalog)))
			continue
		}
		installed, err := collection.GroupDigest(g.Name)
		if err != nil {
			t.Errorf("group %s: %v", g.Name, err)
			continue
		}
		if fromCatalog := collection.ManifestSetDigest(catalog); fromCatalog != installed {
			t.Errorf("group %s: the catalog digests to %s and the registry to %s; run go run ./tools/gendocs, or a manifest does not survive JSON",
				g.Name, fromCatalog, installed)
		}
		compared++
	}
	// Control: every group the catalog holds was compared, and the catalog
	// holds groups at all (a run that compared nothing would pass on any).
	if compared != len(byGroup) || compared == 0 {
		t.Fatalf("compared %d groups of the catalog's %d; the catalog or the registry is not linked here", compared, len(byGroup))
	}
}

// TestEveryBuiltinHasAnIdentity: every built-in method in the catalog
// resolves to an identity naming its routed group, the build's version and
// that group's digest, and the identity is one Valid accepts.
func TestEveryBuiltinHasAnIdentity(t *testing.T) {
	t.Cleanup(collection.CatalogForTest())
	catalog := embeddedCatalog(t)
	routes, err := collection.FirstPartyRoutes(catalog)
	if err != nil {
		t.Fatal(err)
	}
	version := buildinfo.Version()
	digests := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(catalog)) {
		id, err := collection.BuiltinIdentity(name, version)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if id.Group != routes[name] {
			t.Errorf("%s: identity %+v, want group %s", name, id, routes[name])
		}
		if err := id.Valid(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if prev, ok := digests[id.Group]; ok && prev != id.Manifests {
			t.Errorf("%s: group %s digests to %s here and %s for another of its methods", name, id.Group, id.Manifests, prev)
		}
		digests[id.Group] = id.Manifests
	}
	if len(catalog) == 0 {
		t.Fatal("control: the embedded catalog holds no method")
	}
}

// Tests for the manifest-set digest and a built-in method's Collection
// identity (identity.go).
package collection_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// identityInvoke is a method body that does nothing, for fixtures that must
// register as implemented.
func identityInvoke(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
	return collection.Result{}, nil
}

// identityManifest is an implemented manifest that passes Register, told
// apart from another by notes.
func identityManifest(notes string) collection.Manifest {
	return collection.Manifest{
		Status:              collection.StatusImplemented,
		SupportedTransports: []string{"ssh"},
		Reversibility:       collection.Reversibility{Notes: notes},
		Doc:                 collection.Doc{Params: []collection.Param{{Name: "path", Type: "string", Description: "a path"}}},
	}
}

// TestManifestSetDigest_IsOrderFree: the digest is the same whatever order
// the manifests, or the keys inside each one, arrived in; it is the SHA-256
// of the canonical form documented on ManifestSetDigest; and it moves when a
// name or a declaration does.
func TestManifestSetDigest_IsOrderFree(t *testing.T) {
	// The same two manifests, as two documents whose objects list their keys
	// in different orders, the way two programs might describe them.
	first := `{"a.x":{"status":"declared","reversibility":{"notes":"one","reversible":false},"supportedTransports":["ssh","winrm"]},
		"a.y":{"supportsCheck":false,"status":"declared","reversibility":{"reversible":false}}}`
	second := `{"a.y":{"reversibility":{"reversible":false},"status":"declared"},
		"a.x":{"supportedTransports":["ssh","winrm"],"reversibility":{"reversible":false,"notes":"one"},"status":"declared"}}`
	decode := func(doc string) map[string]collection.Manifest {
		var m map[string]collection.Manifest
		if err := json.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := collection.ManifestSetDigest(decode(first)), collection.ManifestSetDigest(decode(second))
	if a != b {
		t.Errorf("one set in two orders gave %s and %s", a, b)
	}

	// The canonical form, pinned, so another implementation can reproduce it.
	canonical := `{"b.y":{"doc":{},"executionContext":{},"reversibility":{"reversible":false},"status":"declared","supportsCheck":false}}`
	sum := sha256.Sum256([]byte(canonical))
	want := wire.ManifestDigestPrefix + hex.EncodeToString(sum[:])
	if got := collection.ManifestSetDigest(map[string]collection.Manifest{"b.y": {Status: collection.StatusDeclared}}); got != want {
		t.Errorf("digest %s, want the SHA-256 of %s (%s)", got, canonical, want)
	}

	if collection.ManifestSetDigest(nil) != collection.ManifestSetDigest(map[string]collection.Manifest{}) {
		t.Error("a nil set and an empty one digest differently")
	}
	renamed := decode(first)
	renamed["a.z"] = renamed["a.y"]
	delete(renamed, "a.y")
	edited := decode(first)
	x := edited["a.x"]
	x.Reversibility.Notes = "two"
	edited["a.x"] = x
	for name, set := range map[string]map[string]collection.Manifest{"a renamed method": renamed, "an edited declaration": edited} {
		if collection.ManifestSetDigest(set) == a {
			t.Errorf("%s left the digest unchanged", name)
		}
	}
	if err := (wire.CollectionIdentity{Group: "a", Manifests: a}).Valid(); err != nil {
		t.Errorf("the digest is not a valid identity digest: %v", err)
	}
}

// TestManifestEncodingCannotFail holds Manifest to the kinds encoding/json
// always encodes, which is why ManifestSetDigest returns no error. A field
// of interface, func, channel, float or complex type, or a map not keyed by
// strings, fails here and needs ManifestSetDigest to grow an error.
func TestManifestEncodingCannotFail(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(path string, typ reflect.Type)
	walk = func(path string, typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.String, reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		case reflect.Slice, reflect.Array:
			walk(path+"[]", typ.Elem())
		case reflect.Map:
			if typ.Key().Kind() != reflect.String {
				t.Errorf("%s is a map keyed by %s", path, typ.Key())
			}
			walk(path+"{}", typ.Elem())
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				if f := typ.Field(i); f.IsExported() && f.Tag.Get("json") != "-" {
					walk(path+"."+f.Name, f.Type)
				}
			}
		default:
			t.Errorf("%s is a %s, which encoding/json can fail to encode", path, typ.Kind())
		}
		for _, m := range []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[interface{ MarshalText() ([]byte, error) }]()} {
			if typ.Implements(m) || reflect.PointerTo(typ).Implements(m) {
				t.Errorf("%s has its own encoder, which may fail", path)
			}
		}
	}
	walk("Manifest", reflect.TypeFor[collection.Manifest]())
	// DescriptionDigest encodes a program's capability definitions too.
	walk("DataDefinition", reflect.TypeFor[capability.DataDefinition]())
}

// TestDescriptionDigest_IsPinned: a program's description digest is the
// SHA-256 of one canonical form written out here, its keys sorted, so a change
// to DescriptionDigest's shape (its keys, its sort) fails here rather than
// moving every program's identity unseen.
func TestDescriptionDigest_IsPinned(t *testing.T) {
	got := collection.DescriptionDigest(nil, []capability.DataDefinition{
		{Name: "acme.b", Doc: "second"},
		{Name: "acme.a", Doc: "first", Provider: "acme_tools"},
	})
	canonical := `{"capabilities":[{"doc":"first","name":"acme.a","provider":"acme_tools"},{"doc":"second","name":"acme.b"}],"methods":{}}`
	sum := sha256.Sum256([]byte(canonical))
	if want := "sha256:" + hex.EncodeToString(sum[:]); got != want {
		t.Errorf("DescriptionDigest = %s, want %s, the SHA-256 of %s", got, want, canonical)
	}
}

// TestGroupManifests_RegistryThenCatalog: a group's set is its registered
// built-ins and, for a name nothing registered, the installed catalog's
// entry; a Controller holding only the catalog digests the same set the
// registry did.
func TestGroupManifests_RegistryThenCatalog(t *testing.T) {
	restoreRegistry := collection.SnapshotForTest()
	t.Cleanup(restoreRegistry)
	t.Cleanup(collection.CatalogForTest())
	set := map[string]collection.Manifest{
		"gate.identitytest_a": identityManifest("a"),
		"gate.identitytest_b": identityManifest("b"),
	}
	for name, m := range set {
		collection.MustRegister(collection.Descriptor{Name: name, Manifest: m, Invoke: identityInvoke})
	}
	// Outside the group, and so in no group's set.
	collection.MustRegister(collection.Descriptor{Name: "gatex.identitytest", Manifest: identityManifest("c"), Invoke: identityInvoke})
	if got := collection.GroupManifests("gate"); !reflect.DeepEqual(got, set) {
		t.Fatalf("GroupManifests(gate) = %v, want %v", got, set)
	}
	runner, err := collection.GroupDigest("gate")
	if err != nil {
		t.Fatal(err)
	}
	if runner != collection.ManifestSetDigest(set) {
		t.Error("GroupDigest is not ManifestSetDigest over GroupManifests")
	}

	// The registry answers first; a catalog entry nothing registered joins.
	catalog, err := json.Marshal(map[string]collection.Manifest{
		"gate.identitytest_a":    identityManifest("catalog's a"),
		"gate.identitytest_only": identityManifest("only"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	got := collection.GroupManifests("gate")
	if got["gate.identitytest_a"].Reversibility.Notes != "a" || got["gate.identitytest_only"].Reversibility.Notes != "only" || len(got) != 3 {
		t.Errorf("GroupManifests over a registry and a catalog = %v", got)
	}

	// A Controller: nothing registered, and the catalog the registry wrote.
	restoreRegistry()
	restoreCatalog := collection.CatalogForTest()
	defer restoreCatalog()
	written, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(written); err != nil {
		t.Fatal(err)
	}
	controller, err := collection.GroupDigest("gate")
	if err != nil {
		t.Fatal(err)
	}
	if controller != runner {
		t.Errorf("the catalog's digest %s is not the registry's %s", controller, runner)
	}
}

// TestGroupDigest_RefusesWhatNoBuildHas: a name that is not a first-party
// group, and a group with no method in this build, have no digest.
func TestGroupDigest_RefusesWhatNoBuildHas(t *testing.T) {
	if _, err := collection.GroupDigest("nosuchgroup"); err == nil {
		t.Error("a name that is no first-party group got a digest")
	}
	if _, err := collection.GroupDigest("kubernetes"); !errors.Is(err, collection.ErrGroupNotLinked) {
		t.Errorf("a group with no method got %v, want ErrGroupNotLinked", err)
	}
}

// TestBuiltinIdentity: a built-in method's identity is its group, the
// version the caller passed, and the group's digest, which the version does
// not move; a name that is not a first-party built-in has none.
func TestBuiltinIdentity(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	collection.MustRegister(collection.Descriptor{Name: "virt.clab.identitytest", Manifest: identityManifest("a"), Invoke: identityInvoke})
	collection.MustRegister(collection.Descriptor{Name: "identitytest.nogroup", Manifest: identityManifest("b"), Invoke: identityInvoke})
	collection.MustRegister(collection.Descriptor{Name: "identitytest.external", Manifest: identityManifest("c"), Invoke: identityInvoke,
		Provider: &collection.Provider{Program: "/opt/collections/identitytest", Digest: "sha256:00"}})

	release, err := collection.BuiltinIdentity("virt.clab.identitytest", "0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := collection.GroupDigest("virt.clab")
	if err != nil {
		t.Fatal(err)
	}
	if want := (wire.CollectionIdentity{Group: "virt.clab", Version: "0.4.0", Manifests: digest}); release != want {
		t.Errorf("BuiltinIdentity = %+v, want %+v", release, want)
	}
	if err := release.Valid(); err != nil {
		t.Errorf("a built-in's identity is not valid: %v", err)
	}
	dirty, err := collection.BuiltinIdentity("virt.clab.identitytest", "0.0.0-dev+0123456789ab.dirty")
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Manifests != release.Manifests {
		t.Error("the version moved the digest")
	}
	for _, name := range []string{"identitytest.nogroup", "identitytest.external", "virt.clab.absent"} {
		if id, err := collection.BuiltinIdentity(name, "0.4.0"); !errors.Is(err, collection.ErrNotBuiltin) || !id.IsZero() {
			t.Errorf("BuiltinIdentity(%s) = %+v, %v; want ErrNotBuiltin and no identity", name, id, err)
		}
	}
}

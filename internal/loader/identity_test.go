//go:build unix

// Tests for an external program's Collection identity (identity.go): the
// loader digests the manifests it decoded from describe, and nothing the
// program says about itself reaches the identity.
package loader

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// identityMethods are the two methods the identity fixtures describe, the
// second with a declaration the first lacks so the two cannot be confused.
func identityMethods() []external.DescribedMethod {
	second := implemented(true)
	second.SupportedTransports = []string{"ssh"}
	return []external.DescribedMethod{
		{Name: "acmeid.first", Manifest: implemented(false)},
		{Name: "acmeid.second", Manifest: second},
	}
}

// TestLoad_ProviderIdentityIsTheDescribedManifestSet: each loaded method's
// Provider carries the program's file name and the digest of the description
// its describe gave; a program describing the same methods in the other
// order, with a self-description of its own added, has the same identity,
// while its file digest differs.
func TestLoad_ProviderIdentityIsTheDescribedManifestSet(t *testing.T) {
	requireConfinement(t)
	methods := identityMethods()
	want := wire.CollectionIdentity{
		Group:   "acme_identity",
		Program: true,
		Manifests: collection.DescriptionDigest(map[string]collection.Manifest{
			methods[0].Name: methods[0].Manifest,
			methods[1].Name: methods[1].Manifest,
		}, nil),
	}

	// load loads a directory holding one program described by out and
	// returns both methods' providers, leaving the registry as it was.
	load := func(out string) [2]*collection.Provider {
		t.Helper()
		dir := programDir(t)
		writeProgram(t, dir, "acme_identity", script(out, "exit 0"))
		restore := collection.SnapshotForTest()
		defer restore()
		if _, err := Load(t.Context(), dir, testOptions()); err != nil {
			t.Fatalf("Load: %v", err)
		}
		var providers [2]*collection.Provider
		for i, m := range methods {
			d, ok := collection.Lookup(m.Name)
			if !ok || d.Provider == nil {
				t.Fatalf("%s was not registered with a provider", m.Name)
			}
			providers[i] = d.Provider
		}
		return providers
	}

	inOrder := load(describeJSON(t, 0, methods...))
	for _, p := range inOrder {
		if p.Identity != want {
			t.Errorf("Provider.Identity = %+v, want %+v", p.Identity, want)
		}
	}
	if err := inOrder[0].Identity.Valid(); err != nil {
		t.Errorf("the loader's identity is not valid: %v", err)
	}

	// The same methods the other way round, and a program claiming to be
	// the file Collection at a release, in fields this build does not read.
	reversed := describeJSON(t, 0, methods[1], methods[0])
	claim := `"collection":{"group":"file","version":"9.9.9","manifests":"sha256:` + strings.Repeat("0", 64) + `"},`
	reversed = "{" + claim + strings.TrimPrefix(reversed, "{")
	reversed = strings.Replace(reversed, `"manifest":{`, `"manifest":{"identity":"file@9.9.9",`, 1)
	other := load(reversed)
	if other[0].Identity != want || other[1].Identity != want {
		t.Errorf("the reordered program's identity is %+v, want %+v", other[0].Identity, want)
	}
	if other[0].Digest == inOrder[0].Digest {
		t.Error("control: two different program files share a file digest")
	}
}

// TestRegister_RefusesAProgramDescribingAMethodTwice: a program with no
// identity is refused before any of its methods registers. Pass one refuses
// the same program first, so this is register's own guard.
func TestRegister_RefusesAProgramDescribingAMethodTwice(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	m := external.DescribedMethod{Name: "acmeid.twice", Manifest: implemented(false)}
	c := candidate{path: "/collections/acme_twice", digest: "sha256:" + strings.Repeat("0", 64), desc: external.Description{Methods: []external.DescribedMethod{m, m}}}
	if _, err := register([]candidate{c}, testOptions()); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("register = %v, want a refusal naming the method described twice", err)
	}
	if _, ok := collection.Lookup("acmeid.twice"); ok {
		t.Error("a method of the refused program was registered")
	}
}

// TestProgramIdentity_DigestsTheCapabilitiesItDefines: a program that changes
// what one of its data capabilities says, with every method manifest the
// same, has a new identity (owner, 2026-11-07); one that lists the same
// capabilities in another order has the same identity.
func TestProgramIdentity_DigestsTheCapabilitiesItDefines(t *testing.T) {
	methods := identityMethods()
	defs := func(doc string) []capability.DataDefinition {
		return []capability.DataDefinition{
			{Name: "acmeid.widget", Doc: doc},
			{Name: "acmeid.gadget", Doc: "a gadget"},
		}
	}
	identity := func(caps []capability.DataDefinition) wire.CollectionIdentity {
		t.Helper()
		id, err := programIdentity("/collections/acme_identity", external.Description{Methods: methods, Capabilities: caps})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	before, after := identity(defs("a widget")), identity(defs("a widget, on any host"))
	if before == after {
		t.Error("a changed capability definition left the program's identity as it was")
	}
	reordered := defs("a widget")
	reordered[0], reordered[1] = reordered[1], reordered[0]
	if got := identity(reordered); got != before {
		t.Errorf("the same capabilities in another order give %+v, want %+v", got, before)
	}
	if none := identity(nil); none == before || none.Manifests == collection.ManifestSetDigest(map[string]collection.Manifest{methods[0].Name: methods[0].Manifest, methods[1].Name: methods[1].Manifest}) {
		t.Errorf("a description with no capabilities digests as a manifest set or as one with capabilities: %+v", none)
	}
}

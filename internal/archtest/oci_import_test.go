// Package archtest: pkg/collection, and everything it reaches, imports no
// OCI package.
//
// A Collection's manifest describes a method. How a Collection is packaged,
// fetched or laid out as an image is the distribution layer's business
// (the artifact work moved to Phase 43), and the manifest must not come to
// depend on it. github.com/opencontainers is already an indirect
// dependency of this module, so an import of it into pkg/collection would
// compile with no go.mod change and no reviewer would see a new line in a
// dependency file. This guard exists before the violation does, which is
// when it is worth most.
package archtest

import (
	"sort"
	"strings"
	"testing"
)

// collectionPackage is the package whose whole dependency closure must
// stay free of OCI code.
const collectionPackage = modulePath + "/pkg/collection"

// ociImportPrefixes are the import path prefixes this guard treats as an
// OCI package: the Open Container Initiative's own Go modules and the
// libraries that read or write OCI images and registries. Each ends in a
// slash or names a whole module path, so a package that merely shares a
// prefix (github.com/opencontainersfake) is not flagged.
var ociImportPrefixes = []string{
	"github.com/opencontainers/",
	"github.com/google/go-containerregistry/",
	"github.com/containers/image/",
	"github.com/distribution/distribution/",
	"github.com/docker/distribution/",
	"oras.land/",
}

// isOCIPackage reports whether importPath is an OCI package or a package
// inside one, by ociImportPrefixes. A prefix's module root itself (the
// prefix without its trailing slash) counts too.
func isOCIPackage(importPath string) bool {
	for _, p := range ociImportPrefixes {
		if strings.HasPrefix(importPath, p) || importPath == strings.TrimSuffix(p, "/") {
			return true
		}
	}
	return false
}

// ociImporters returns, for every package in pkgs, the OCI packages it
// imports directly, keyed by the importer. It looks at every listed
// package, this module's and third parties' alike: a dependency of
// pkg/collection that pulls OCI code in is as much a violation as
// pkg/collection importing it itself, and naming the direct importer is
// what tells the reader which edge to cut.
func ociImporters(pkgs []listedPackage) map[string][]string {
	hits := map[string][]string{}
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if isOCIPackage(imp) {
				hits[pkg.ImportPath] = append(hits[pkg.ImportPath], imp)
			}
		}
	}
	return hits
}

// TestCollectionReachesNoOCIPackage asserts that no package in
// pkg/collection's dependency closure, its own or a third party's,
// imports an OCI package. TestCollectionReachesNoOCIPackage_Control and
// TestOCIImporters_Detector prove the same listing and matcher find one
// where one exists, so an empty result here is evidence rather than a
// listing that examined nothing.
func TestCollectionReachesNoOCIPackage(t *testing.T) {
	pkgs := goList(t, true, collectionPackage)
	if !listsPackage(pkgs, collectionPackage) {
		t.Fatalf("go list -deps %s did not list the package itself, so this rule examined nothing", collectionPackage)
	}

	hits := ociImporters(pkgs)
	importers := make([]string, 0, len(hits))
	for p := range hits {
		importers = append(importers, p)
	}
	sort.Strings(importers)
	for _, p := range importers {
		t.Errorf("%s imports %v and is reached from %s: a Collection's manifest must not depend on an OCI package; packaging belongs to the distribution layer", p, hits[p], collectionPackage)
	}
	t.Logf("examined %d packages reached from %s", len(pkgs), collectionPackage)
}

// TestCollectionReachesNoOCIPackage_Control runs the identical listing and
// matcher over internal/testsupport, which reaches the real
// github.com/opencontainers/image-spec through testcontainers-go and the
// Docker client. If this finds nothing, the listing or the matcher is
// broken and the clean result above proves nothing. Should testsupport
// ever stop reaching OCI code, pick another real package that does; do not
// delete the control.
func TestCollectionReachesNoOCIPackage_Control(t *testing.T) {
	const root = modulePath + "/internal/testsupport"
	pkgs := goList(t, true, root)
	if !listsPackage(pkgs, root) {
		t.Fatalf("go list -deps %s did not list the package itself", root)
	}
	if hits := ociImporters(pkgs); len(hits) == 0 {
		t.Fatalf("no package reached from %s was seen importing an OCI package, so the check is misaimed", root)
	}
}

// TestOCIImporters_Detector holds the matcher to a synthetic import graph:
// an OCI import is flagged wherever it sits in the closure and whatever
// OCI library it is, and a package that only shares a name prefix is not.
func TestOCIImporters_Detector(t *testing.T) {
	for name, tc := range map[string]struct {
		pkgs []listedPackage
		want map[string][]string
	}{
		"pkg/collection importing image-spec directly": {
			pkgs: []listedPackage{{ImportPath: collectionPackage, Imports: []string{"fmt", "github.com/opencontainers/image-spec/specs-go/v1"}}},
			want: map[string][]string{collectionPackage: {"github.com/opencontainers/image-spec/specs-go/v1"}},
		},
		"a module package pkg/collection reaches importing go-digest": {
			pkgs: []listedPackage{
				{ImportPath: collectionPackage, Imports: []string{modulePath + "/pkg/capability"}},
				{ImportPath: modulePath + "/pkg/capability", Imports: []string{"github.com/opencontainers/go-digest"}},
			},
			want: map[string][]string{modulePath + "/pkg/capability": {"github.com/opencontainers/go-digest"}},
		},
		"a third-party dependency pulling in go-containerregistry": {
			pkgs: []listedPackage{
				{ImportPath: collectionPackage, Imports: []string{"example.com/lib"}},
				{ImportPath: "example.com/lib", Imports: []string{"github.com/google/go-containerregistry/pkg/v1"}},
			},
			want: map[string][]string{"example.com/lib": {"github.com/google/go-containerregistry/pkg/v1"}},
		},
		"an oras client": {
			pkgs: []listedPackage{{ImportPath: collectionPackage, Imports: []string{"oras.land/oras-go/v2"}}},
			want: map[string][]string{collectionPackage: {"oras.land/oras-go/v2"}},
		},
		"a package that only shares a prefix": {
			pkgs: []listedPackage{{ImportPath: collectionPackage, Imports: []string{"github.com/opencontainersfake/x", "example.com/opencontainers/spec", "oras.landing/x"}}},
			want: map[string][]string{},
		},
		"no imports at all": {
			pkgs: []listedPackage{{ImportPath: collectionPackage}},
			want: map[string][]string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := ociImporters(tc.pkgs)
			if len(got) != len(tc.want) {
				t.Fatalf("ociImporters = %v, want %v", got, tc.want)
			}
			for importer, imports := range tc.want {
				if strings.Join(got[importer], ",") != strings.Join(imports, ",") {
					t.Errorf("ociImporters[%s] = %v, want %v", importer, got[importer], imports)
				}
			}
		})
	}
}

// listsPackage reports whether pkgs holds importPath itself.
func listsPackage(pkgs []listedPackage, importPath string) bool {
	for _, p := range pkgs {
		if p.ImportPath == importPath {
			return true
		}
	}
	return false
}

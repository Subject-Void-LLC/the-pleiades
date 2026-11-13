// This file holds every rule about which devices a method may run on to the
// one check pkg/collection keeps: nothing outside that package reads a
// manifest's requirements itself.
//
// A Collection method's manifest says what a device needs in three places:
// its own RequiredCapabilities, a call shape's (selected by a parameter),
// and an alternative's (selected by the device, Phase 127b). A rule that
// reads RequiredCapabilities directly sees the first and skips the other
// two, which is how the loader's rule that an external method names only
// its own program's capabilities came to let a call shape name another
// program's: the rule read one field, and the manifest said it in another.
// collection.Admits (validate, the engine and a Controller's admission),
// ChooseAlternative (the method), NamedCapabilities (the loader's ownership
// rule) and Requires (a planner) are the ways in, and each reads all three.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// requirementFields are the manifest fields only pkg/collection reads.
var requirementFields = map[string]bool{"RequiredCapabilities": true, "Alternatives": true}

// requirementReaders are the files outside pkg/collection allowed to read
// one, each with why. A renderer prints what a manifest declares and decides
// nothing with it; a field of the same name on another type is not a
// manifest's at all (the scan is by name, so it errs toward flagging).
var requirementReaders = map[string]string{
	filepath.Join("tools", "gendocs", "modules.go"):        "renders a method's reference page, every part of its requirements",
	filepath.Join("cmd", "pleiades", "doc.go"):             "renders pleiades doc for a method, every part of its requirements",
	filepath.Join("cmd", "pleiades", "collection.go"):      "lists an external program's methods with what each requires, for the person approving it",
	filepath.Join("internal", "api", "templates.go"):       "a job template's own RequiredCapabilities field, not a manifest's",
	filepath.Join("internal", "api", "templates_patch.go"): "a job template's own RequiredCapabilities field, not a manifest's",
	filepath.Join("internal", "journal", "nodes.go"):       "a journal node's Alternatives, the undo methods a claim may name, not a manifest's",
	filepath.Join("cmd", "controller", "node_resolver.go"): "a journal node's Alternatives, the undo methods a claim may name, not a manifest's",
}

// TestNothingOutsideCollectionReadsAManifestsRequirements scans every
// non-test Go file outside pkg/collection for a selection of
// RequiredCapabilities or Alternatives. A composite literal's key is not a
// selection, so a descriptor declaring its manifest is not flagged; reading
// the field back is.
func TestNothingOutsideCollectionReadsAManifestsRequirements(t *testing.T) {
	root := repoRoot(t)
	collectionDir := filepath.Join("pkg", "collection")
	used := map[string]bool{}
	scanned := 0
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") || filepath.Dir(rel) == collectionDir {
				return
			}
			scanned++
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !requirementFields[sel.Sel.Name] {
					return true
				}
				if _, allowed := requirementReaders[rel]; allowed {
					used[rel] = true
					return true
				}
				t.Errorf("%s:%d reads %s itself. Hold a device to a manifest through collection.Admits (or "+
					"AdmitsAnyShape, ChooseAlternative), ask which capabilities it names through NamedCapabilities, "+
					"or whether every call needs one through Requires, so call shapes and alternatives are never skipped",
					rel, fset.Position(sel.Pos()).Line, sel.Sel.Name)
				return true
			})
		})
	}
	if scanned < 500 {
		t.Fatalf("scanned %d files: the walk is not reading the tree it guards", scanned)
	}
	// An allowlist entry nothing needs is a hole waiting for a reader.
	for rel := range requirementReaders {
		if !used[rel] {
			t.Errorf("%s is allowed to read a manifest's requirements and no longer does; remove it from requirementReaders", rel)
		}
	}
}

// FAILURE_PATTERNS 837 and 841: the Controller links no Collection, so a
// registry lookup there finds nothing. Its device admission checked no
// method and its rollback route refused every recorded undo, while every
// test passed in binaries that link the catalog. Code the Controller links
// reads a built-in method's manifest through collection.BuiltinManifest or
// ManifestOf, which fall back to the module catalog main installs.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// controllerRegistryReaders are the packages the Controller links that may
// still call collection.Lookup, each with why that call never runs there.
var controllerRegistryReaders = map[string]string{
	// The executor's lookups (dispatch, check, render, journal projection,
	// undo steps) run in the Runner and the CLI, which register the catalog;
	// the Controller calls only engine helpers that read no registry
	// (LifecycleAdmitsIn, CapabilityAdmits, Batches, ParseLimit, Select), and
	// ListParam, which reads ManifestOf.
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine": "executor paths run only in the Runner and the CLI",
}

// TestTheControllerReadsNoRegistryItDoesNotFill: no package the Controller
// links calls collection.Lookup but those listed above, and the Controller
// still links no Collection, which is what makes the rule necessary. A new
// caller fails here until it reads through BuiltinManifest or ManifestOf, or
// is listed with the reason it never runs on the Controller.
func TestTheControllerReadsNoRegistryItDoesNotFill(t *testing.T) {
	const module = "github.com/Subject-Void-LLC/the-pleiades/"
	pkgs := goList(t, true, module+"cmd/controller")
	fset := token.NewFileSet()
	callers := 0
	for _, p := range pkgs {
		if !strings.HasPrefix(p.ImportPath, module) {
			continue
		}
		rel := strings.TrimPrefix(p.ImportPath, module)
		if strings.HasPrefix(rel, "internal/catalog/") && rel != "internal/catalog/fragment" {
			t.Errorf("the Controller links %s; if it now registers Collections, this rule and its catalog are worth revisiting", rel)
		}
		if rel == "pkg/collection" {
			continue
		}
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Lookup" {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "collection" {
					return true
				}
				callers++
				if _, allowed := controllerRegistryReaders[p.ImportPath]; !allowed {
					t.Errorf("%s calls collection.Lookup, which finds nothing in the Controller; read through collection.BuiltinManifest or ManifestOf", fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
	if callers == 0 {
		t.Fatal("found no collection.Lookup call at all, so this rule proved nothing")
	}
	for k := range controllerRegistryReaders {
		if !slices.ContainsFunc(pkgs, func(p listedPackage) bool { return p.ImportPath == k }) {
			t.Errorf("%s is listed but the Controller no longer links it; remove it", k)
		}
	}
}

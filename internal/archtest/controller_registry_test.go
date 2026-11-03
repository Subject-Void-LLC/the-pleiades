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

// executorOnly is why most of internal/engine's registry reads never run on
// the Controller: they belong to running a task, which the Runner and the
// CLI do, and both register the catalog.
const executorOnly = "runs a task, which only the Runner and the CLI do, and both register every built-in method"

// controllerRegistryReaders are the functions, by package, that the
// Controller links and that may still call collection.Lookup, each with why
// that is safe there. A new caller anywhere the Controller links fails the
// rule until it reads through collection.BuiltinManifest or ManifestOf or
// is listed here with its reason.
var controllerRegistryReaders = map[string]map[string]string{
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine": {
		// The runbook builder asks it of every handler, on the Controller
		// too; it falls back to the installed catalog (ManifestOf).
		"TaskNeedsDevice":   "falls back to collection.ManifestOf where the registry is empty",
		"externalProvider":  executorOnly,
		"Execute":           executorOnly,
		"Check":             executorOnly,
		"keepsUndo":         executorOnly,
		"declaredFacts":     executorOnly,
		"resolveFQCN":       executorOnly + " (the journal projection is the Runner's)",
		"projectResult":     executorOnly + " (the journal projection is the Runner's)",
		"predictsPaths":     executorOnly,
		"undoSteps":         executorOnly,
		"templateFileParam": executorOnly,
		"templateOptions":   executorOnly,
		"paramDecls":        executorOnly + " (rendering a task's params)",
		// Exported, so held below: no Controller-linked package outside
		// internal/engine may call them.
		"Checkable":       "validation only, which the Controller does not link",
		"ParamFormats":    "rendering and validation only",
		"ParamSecretRefs": "rendering and validation only",
		"ParamTypes":      "rendering and validation only",
	},
}

// registryReadingExports are internal/engine's exported functions that read
// the registry with no catalog fallback. No package the Controller links
// outside internal/engine may call one: there each would find nothing.
var registryReadingExports = []string{"Checkable", "ParamFormats", "ParamSecretRefs", "ParamTypes"}

// TestTheControllerReadsNoRegistryItDoesNotFill: no function the Controller
// links calls collection.Lookup but those listed above, no package it links
// outside internal/engine calls engine's registry-reading exports, and the
// Controller still links no Collection, which is what makes the rule
// necessary. It reads selector calls only: an aliased import or a method
// value of Lookup is not seen.
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
		allowed := controllerRegistryReaders[p.ImportPath]
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					switch {
					case ok && x.Name == "collection" && sel.Sel.Name == "Lookup":
						callers++
						if _, listed := allowed[fn.Name.Name]; !listed {
							t.Errorf("%s: %s calls collection.Lookup, which finds nothing in the Controller; read through collection.BuiltinManifest or ManifestOf", fset.Position(call.Pos()), fn.Name.Name)
						}
					case ok && x.Name == "engine" && slices.Contains(registryReadingExports, sel.Sel.Name):
						t.Errorf("%s: engine.%s reads a registry the Controller never fills, and this package is linked into the Controller", fset.Position(call.Pos()), sel.Sel.Name)
					}
					return true
				})
			}
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
	for _, name := range registryReadingExports {
		if _, listed := controllerRegistryReaders[module+"internal/engine"][name]; !listed {
			t.Errorf("engine.%s is held as a registry-reading export but not listed as a reader", name)
		}
	}
}

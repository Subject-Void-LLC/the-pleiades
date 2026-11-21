// Phase 41b: a built-in method that emits a fact declares it, and what each
// one emits in its tests is held to that declaration.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
)

// facttestImport is the import path of the package every fact-emitting
// package's tests run their audit through.
const facttestImport = modulePath + "/pkg/collection/facttest"

// emittingFiles returns every non-test Go file under internal/catalog that
// calls a method named EmitFact, by absolute path.
func emittingFiles(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join(repoRoot(t), "internal", "catalog")
	out := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "EmitFact" {
					out[path] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestEveryFactEmitterDeclaresItsFacts: a built-in method whose Invoke is
// defined in a file that calls EmitFact declares its facts
// (Manifest.Facts). It is the backstop for an emitter no test drives, which
// TestEveryFactEmittingPackageAuditsItsTests cannot see: it reads source, so
// it sees only an EmitFact in the very file of Invoke, never one in a helper
// file or a pkg/ package (pkg/yangfacts, pkg/tlsfacts), and never which keys
// are emitted. The registry-based audit is what holds those; the engine's own
// refusal at run time stays the last gate.
func TestEveryFactEmitterDeclaresItsFacts(t *testing.T) {
	emitting := emittingFiles(t)
	if len(emitting) == 0 {
		t.Fatal("no file under internal/catalog calls EmitFact, so this test proved nothing")
	}
	found := 0
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Invoke == nil {
			continue
		}
		pc := reflect.ValueOf(desc.Invoke).Pointer()
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		file, _ := fn.FileLine(pc)
		if !emitting[filepath.Clean(file)] {
			continue
		}
		found++
		if len(desc.Manifest.Facts) == 0 {
			t.Errorf("%s emits facts (%s) and declares none (Manifest.Facts)", cfg.Name, file)
		}
	}
	// facts.gather, net.ios.facts, net.frr.facts and the four net.catalyst
	// fact methods.
	if found < 7 {
		t.Errorf("found %d emitting methods, want at least 7: the attribution by Invoke's file stopped working", found)
	}
}

// TestEveryFactEmittingPackageAuditsItsTests: every package holding a
// fact-declaring built-in's Invoke or Check (read from the registry), and
// every package under internal/catalog whose code calls EmitFact, runs its
// tests through facttest.Main from its TestMain and feeds facttest.Observe
// from a fake RunbookContext. That audit is the registry-based check: it fails
// the package's test binary for a fact a method emitted that it does not
// declare, a value of another type, and a fact-declaring method no test made
// emit unless an exemption names the gate that does. This test only holds
// that the audit is wired where it has something to hold.
func TestEveryFactEmittingPackageAuditsItsTests(t *testing.T) {
	root := repoRoot(t)
	packages := map[string][]string{}
	for _, d := range facttest.Builtins() {
		if len(d.Manifest.Facts) == 0 {
			continue
		}
		for _, fn := range []collection.Method{d.Invoke, d.Check} {
			if name := facttest.FunctionName(fn); name != "" {
				pkg := facttest.PackageOf(name)
				if !containsName(packages[pkg], d.Name) {
					packages[pkg] = append(packages[pkg], d.Name)
				}
			}
		}
	}
	declaring := len(packages)
	for file := range emittingFiles(t) {
		rel, err := filepath.Rel(root, filepath.Dir(file))
		if err != nil {
			t.Fatal(err)
		}
		pkg := modulePath + "/" + filepath.ToSlash(rel)
		if _, ok := packages[pkg]; !ok {
			packages[pkg] = nil
		}
	}
	// The control: the registry walk found the fact-declaring methods the
	// generated catalog lists, in the packages it found them in.
	if declaring < 10 {
		t.Fatalf("found fact-declaring built-ins in %d packages, want at least 10: the registry walk stopped finding them", declaring)
	}
	names := make([]string, 0, len(packages))
	for pkg := range packages {
		names = append(names, pkg)
	}
	sort.Strings(names)
	t.Logf("%d packages hold fact-emitting methods: %s", len(names), strings.Join(names, ", "))
	for _, pkg := range names {
		rel, ok := strings.CutPrefix(pkg, modulePath+"/")
		if !ok {
			t.Errorf("%s holds a fact-emitting method and is outside this module", pkg)
			continue
		}
		mains, observes := auditWiring(t, filepath.Join(root, filepath.FromSlash(rel)))
		if !mains {
			t.Errorf("%s holds fact-emitting methods %v, and no TestMain of its tests calls facttest.Main", pkg, packages[pkg])
		}
		if !observes {
			t.Errorf("%s holds fact-emitting methods %v, and no fake RunbookContext of its tests calls facttest.Observe", pkg, packages[pkg])
		}
	}
}

// TestEveryBuiltinIsInAFactAuditGroup: facttest finds the built-ins through
// the first-party groups, so a built-in in no group would go unaudited. Every
// method the generated catalog lists that this binary registered is one
// facttest.Builtins returns.
func TestEveryBuiltinIsInAFactAuditGroup(t *testing.T) {
	found := map[string]bool{}
	for _, d := range facttest.Builtins() {
		found[d.Name] = true
	}
	checked := 0
	for _, cfg := range catalogdata.Collections {
		if d, ok := collection.Lookup(cfg.Name); !ok || d.Provider != nil {
			continue
		}
		checked++
		if !found[cfg.Name] {
			t.Errorf("%s is a registered built-in that facttest.Builtins does not return: it belongs to no first-party group", cfg.Name)
		}
	}
	if checked < 200 {
		t.Errorf("checked %d registered built-ins, want at least 200: the catalog walk stopped finding them", checked)
	}
}

// TestFactTypeRuleIsTheStores: facttest.TypeProblem, which a package under
// pkg/ needs because it cannot import internal/facts, accepts exactly the
// values internal/facts.Prepare stores under each declared type.
func TestFactTypeRuleIsTheStores(t *testing.T) {
	values := []any{"x", "", 0, 3, -7, 3.0, 3.5, 1e300, true, false, nil,
		[]string{"a"}, []string(nil), []any{}, map[string]any{"a": 1}, map[string]int(nil), struct{ A int }{1}}
	types := []collection.FactType{collection.FactString, collection.FactInteger, collection.FactNumber,
		collection.FactBoolean, collection.FactList, collection.FactObject}
	for _, want := range types {
		guard := facts.Guard{Declared: func(string) (facts.Spec, bool) {
			return facts.SpecOf(string(collection.FactStable), string(want), "facttest.value", false), true
		}}
		for _, v := range values {
			entries, _, err := facts.Prepare(map[string]any{"facttest.value": v}, guard)
			if err != nil {
				t.Fatal(err)
			}
			stored := len(entries) == 1
			if passes := facttest.TypeProblem(want, v) == ""; passes != stored {
				t.Errorf("a %s fact holding %#v: facttest passes it %v, and the store stores it %v", want, v, passes, stored)
			}
		}
	}
}

// auditWiring reports whether the package in dir has a TestMain calling
// facttest.Main, and a test file calling facttest.Observe.
func auditWiring(t *testing.T, dir string) (mains, observes bool) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range paths {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		local := importName(f, facttestImport, "facttest")
		if local == "" {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Recv == nil && fn.Name.Name == "TestMain" && callsSelector(fn.Body, local, "Main") {
				mains = true
			}
			if callsSelector(fn.Body, local, "Observe") {
				observes = true
			}
		}
	}
	return mains, observes
}

// callsSelector reports whether body calls pkg.name.
func callsSelector(body ast.Node, pkg, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// containsName reports whether list holds name.
func containsName(list []string, name string) bool {
	for _, x := range list {
		if x == name {
			return true
		}
	}
	return false
}

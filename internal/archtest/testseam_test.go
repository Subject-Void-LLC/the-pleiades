// This file enforces the rule that makes an exported test seam safe to
// have at all: production code may not call one.
//
// pkg/registry.Registry[T].SnapshotForTest, and the per-package
// SnapshotForTest wrappers over it, exist because a registry built at
// package scope outlives the test that writes to it -- nine packages could
// not run under `go test -count>1` at once, two of them panicking, and no
// gate here saw it because `go test` defaults to -count=1.
//
// The seam could not be an export_test.go, which is the shape this
// repository normally reaches for and the shape Phase 12 moved
// api.IdentityKeyForTest into after it was abused as an auth bypass (see
// testonly_test.go's own header). A _test.go file cannot be imported
// across package boundaries, and most callers here isolate a table some
// OTHER package owns: four packages register into pkg/collection's
// registry alone. So the seam is ordinary exported surface, and this rule
// is what an export_test.go would otherwise have given for free.
//
// LESSONS_LEARNED.md #156 is why this file ships in the same commit as
// those doc comments rather than after them: a doc comment claiming an
// invariant no compiler enforces is a repository-wide assertion no reader
// can check, so it ships with its AST rule or it gets written weaker. Each
// SnapshotForTest doc comment says "internal/archtest forbids production
// code from calling it". This is that rule, and without it every one of
// those sentences would be false on arrival.
//
// The check is a source scan rather than a type check because what is
// forbidden is not a type. SnapshotForTest returns a plain func(), and a
// production caller would write `defer registry.SnapshotForTest()()` or
// stash the closure -- both invisible to go vet and to every linter, and
// both perfectly legal Go.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// testSeamSuffix is the naming convention the rule keys off. Everything
// reachable from production that exists only to serve tests carries it, so
// one rule covers all of them without naming any individually: the method
// on pkg/registry, the six package-level wrappers over it, and
// pkg/remoteexec's own seam, which is not a registry wrapper at all but a
// snapshot of its process-wide Runner memo. It covers the next one somebody
// adds without this file being touched, which is the point of keying on the
// name rather than on a list.
const testSeamSuffix = "ForTest"

// seamScanRoots are the trees both rules below scan.
//
// tools/ is on the list for a specific reason rather than for symmetry:
// tools/gendocs reads these registries to generate the committed pages
// under docs/reference, so a seam called there would not fail anything
// loudly, it would produce a documentation diff that is quietly wrong.
// tests/ is here because tests/e2e drives the real binaries and is
// ordinary code from these rules' point of view.
var seamScanRoots = []string{"internal", "cmd", "pkg", "tools", "tests"}

// TestNoProductionCodeCallsATestSeam scans every non-test Go file under
// internal/, cmd/ and pkg/ for a call to anything whose name ends in
// ForTest, from anywhere that is not itself a test seam.
//
// Two exemptions, and only two. A _test.go file is exempt because calling
// these is the entire point. A function whose OWN name ends in ForTest is
// exempt because that is what the package-level wrappers are: each
// SnapshotForTest is one line forwarding to the registry's own, and a rule
// that flagged those would flag the mechanism instead of its misuse. The
// exemption is expressed as "a seam may call a seam" rather than as a list
// of the ones that exist today, so it keeps holding for the next one.
//
// There is no allowlist beyond that, deliberately: the first entry on one
// would be the thing this rule exists to prevent. gosec-waivers.json's
// per-finding written reasons are the model for a waiver that is actually
// justified rather than a habit, and nothing here has earned one.
//
// Controlled live rather than assumed, twice. Written without the
// seam-calls-seam exemption it reported every wrapper in the module, which
// is how the scan was shown to reach production call sites at all rather
// than merely finding nothing. And with the exemption in place, adding one
// real production caller to internal/launch turned it red on that exact
// line, and removing it turned it green again.
func TestNoProductionCodeCallsATestSeam(t *testing.T) {
	root := repoRoot(t)

	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") {
				return
			}

			report := func(pos token.Pos, name string) {
				t.Errorf("%s:%d calls %s from production code. "+
					"A %s function exists only so a test can put process-wide state back the way it "+
					"found it; reachable from a shipped binary it is a way to silently empty a registry "+
					"at run time. If production genuinely needs this behaviour, it needs its own "+
					"named operation with its own reason, not the test seam",
					rel, fset.Position(pos).Line, name, testSeamSuffix)
			}

			for _, decl := range file.Decls {
				// A seam may call a seam; that is what the wrappers do.
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil &&
					strings.HasSuffix(fn.Name.Name, testSeamSuffix) {
					continue
				}

				ast.Inspect(decl, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}

					name := ""
					switch fn := call.Fun.(type) {
					case *ast.Ident: // SnapshotForTest()
						name = fn.Name
					case *ast.SelectorExpr: // collection.SnapshotForTest(), r.SnapshotForTest()
						name = fn.Sel.Name
					}
					if name != "" && strings.HasSuffix(name, testSeamSuffix) {
						report(call.Pos(), name)
					}
					return true
				})
			}
		})
	}
}

// TestEveryTestSeamIsNamedForOne is the other half, and the reason the
// rule above can be a simple suffix match.
//
// It walks the declarations rather than the calls: a function that hands
// out a restore closure over a package-level registry, but is NOT named
// ForTest, would be invisible to the scan above while being exactly as
// dangerous. Catching the naming at the point of declaration is what keeps
// the convention load-bearing instead of decorative.
//
// The shape it looks for is narrow on purpose -- a niladic function
// returning exactly one func() -- because that is the seam's signature and
// widening it would flag ordinary constructors that return cleanups, of
// which this module has many legitimate ones in its test helpers.
func TestEveryTestSeamIsNamedForOne(t *testing.T) {
	root := repoRoot(t)

	// Collected per DIRECTORY, not per file. A package-level var is visible
	// to every file in its package, so a seam moved into a seams.go beside
	// the file declaring the registry would otherwise disarm this rule
	// silently, which is the failure mode it exists to prevent.
	packageRegistries := map[string]map[string]bool{}
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, _ *token.FileSet) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			names := packageRegistryNames(file)
			if len(names) == 0 {
				return
			}
			pkgDir := filepath.Dir(path)
			if packageRegistries[pkgDir] == nil {
				packageRegistries[pkgDir] = map[string]bool{}
			}
			for name := range names {
				packageRegistries[pkgDir][name] = true
			}
		})
	}

	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") {
				return
			}

			registries := packageRegistries[filepath.Dir(path)]
			if len(registries) == 0 {
				return
			}

			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil || !fn.Name.IsExported() {
					continue
				}
				if strings.HasSuffix(fn.Name.Name, testSeamSuffix) {
					continue
				}
				if !returnsOnlyARestoreFunc(fn) || fn.Type.Params.NumFields() != 0 {
					continue
				}
				if !mentionsAPackageRegistry(fn, registries) {
					continue
				}

				t.Errorf("%s:%d declares %s, which takes nothing and returns a lone func() over a "+
					"package-level registry -- that is a restore closure, so it must be named with the "+
					"%s suffix or TestNoProductionCodeCallsATestSeam cannot see it",
					rel, fset.Position(fn.Pos()).Line, fn.Name.Name, testSeamSuffix)
			}
		})
	}
}

// returnsOnlyARestoreFunc reports whether fn returns exactly one value and
// that value is a func with no parameters and no results.
func returnsOnlyARestoreFunc(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	ft, ok := fn.Type.Results.List[0].Type.(*ast.FuncType)
	if !ok || len(fn.Type.Results.List[0].Names) > 1 {
		return false
	}
	return ft.Params.NumFields() == 0 && ft.Results.NumFields() == 0
}

// packageRegistryNames returns the file-scope variables initialised from
// registry.New, which is how this module declares a process-wide table.
//
// It reads the declarations rather than asking go/types because the
// question is not what type the variable has. Two variables of the same
// *registry.Registry[T] type are different things here depending on
// whether one is package scope and outlives every test in the binary.
func packageRegistryNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, rhs := range value.Values {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(value.Names) {
					continue
				}
				// registry.New[T](), whose Fun is an IndexExpr over the
				// selector once the type argument is written out.
				fun := call.Fun
				if idx, ok := fun.(*ast.IndexExpr); ok {
					fun = idx.X
				}
				sel, ok := fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "New" {
					continue
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "registry" {
					names[value.Names[i].Name] = true
				}
			}
		}
	}
	return names
}

// mentionsAPackageRegistry reports whether fn's body reads one of the
// package-level registries names holds.
func mentionsAPackageRegistry(fn *ast.FuncDecl, names map[string]bool) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && names[ident.Name] {
			found = true
		}
		return !found
	})
	return found
}

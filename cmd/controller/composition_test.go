package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryCryptoHookIsComposed fails the build when internal/crypto
// exports an ent hook or interceptor that this composition root never
// registers.
//
// It exists because that exact thing had already happened, silently, for
// as long as SavedLaunchConfig has had an answers column.
// crypto.SavedLaunchConfigAnswersHook and its interceptor were written,
// were correct, had their own passing tests, and were referenced from
// nowhere but those tests. The column was plaintext in every deployment,
// while internal/apispec's saved-configuration schema told API callers it
// was encrypted at rest.
//
// Nothing could have caught that. The package's tests passed because the
// code works. This binary's tests passed because it never mentioned the
// code. The gap was exactly the space between the two, which is the shape
// LESSONS_LEARNED.md #21 and #105 both describe from different directions,
// and which internal/launch/store.go's own comment says this repository has
// now recorded three separate times.
//
// So this is a note turned into a test. It reads both sides as source: what
// internal/crypto exports, and what cmd/controller registers.
func TestEveryCryptoHookIsComposed(t *testing.T) {
	root := moduleRoot(t)

	exported := exportedHookConstructors(t, filepath.Join(root, "internal", "crypto"))
	if len(exported) == 0 {
		t.Fatal("found no exported hook or interceptor constructors in internal/crypto, so this guard is not reading what it thinks it is")
	}

	registered := referencedCryptoSymbols(t, filepath.Join(root, "cmd", "controller"))

	missing := make([]string, 0)
	for _, name := range exported {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf(
			"internal/crypto exports these ent hooks or interceptors and this composition root registers none of them: %v\n"+
				"An unregistered encryption hook means the column it protects is stored in plaintext while everything "+
				"else in the system says otherwise. That has already happened once here, to "+
				"SavedLaunchConfig.answers, and nothing noticed because the hook's own tests passed.\n"+
				"Either register it on the client, or, if it genuinely should not be composed, say so explicitly rather "+
				"than leaving it looking wired.",
			missing)
	}
}

// exportedHookConstructors returns every exported function in dir whose
// result type is ent.Hook or ent.Interceptor.
//
// Matching on the RETURN TYPE rather than on a name suffix is what makes
// this guard hard to slip past. A hook named something unexpected is still
// a hook, and the compiler already knows it.
func exportedHookConstructors(t *testing.T, dir string) []string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}

	var out []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				if fn.Type.Results == nil {
					continue
				}
				for _, result := range fn.Type.Results.List {
					if isEntHookType(result.Type) {
						out = append(out, fn.Name.Name)
						break
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// isEntHookType reports whether expr names ent.Hook or ent.Interceptor.
func isEntHookType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "ent" {
		return false
	}
	return sel.Sel.Name == "Hook" || sel.Sel.Name == "Interceptor"
}

// referencedCryptoSymbols returns every crypto.X selector this package
// mentions.
func referencedCryptoSymbols(t *testing.T, dir string) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}

	seen := make(map[string]bool)
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != "crypto" {
					return true
				}
				seen[sel.Sel.Name] = true
				return true
			})
		}
	}
	return seen
}

// moduleRoot walks up from the working directory to the directory holding
// go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}

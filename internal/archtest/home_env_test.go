// Every test that points the home directory somewhere points Windows'
// home variable there too, so the test reads the home it set on every
// operating system it runs on.
package archtest

import (
	"go/ast"
	"go/build"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryHomeOverrideCoversWindows holds every test function that sets
// HOME to setting USERPROFILE as well. os.UserHomeDir reads HOME on Unix
// and USERPROFILE on Windows, so a test that set HOME alone read the real
// login's home on Windows: #73 fixed pkg/remoteexec's known_hosts tests
// (FAILURE_PATTERNS 510), and internal/catalog/net/ssh's failed on
// win-check the next time a branch put it in scope.
func TestEveryHomeOverrideCoversWindows(t *testing.T) {
	root := repoRoot(t)
	var setsHome int
	for _, dir := range []string{"cmd", "internal", "pkg", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			if !strings.HasSuffix(path, "_test.go") || !buildsOnWindows(t, path) {
				return
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				home, profile := homeEnvSets(fn.Body)
				if home == token.NoPos {
					continue
				}
				setsHome++
				if !profile {
					t.Errorf("%s:%d: %s sets HOME and not USERPROFILE, so on Windows, where os.UserHomeDir reads USERPROFILE, "+
						"the test reads the real login's home instead of the one it made; set both",
						filepath.ToSlash(rel), fset.Position(home).Line, fn.Name.Name)
				}
			}
		})
	}
	if setsHome == 0 {
		t.Fatal("no test function sets HOME, so this test proved nothing: has t.Setenv stopped being how tests do it?")
	}
	t.Logf("%d test function(s) set HOME", setsHome)
}

// windowsBuild is how a Windows run builds the tests: its GOOS, and the
// integration tag the test passes use.
var windowsBuild = func() build.Context {
	c := build.Default
	c.GOOS, c.GOARCH, c.CgoEnabled = "windows", "amd64", false
	c.BuildTags = []string{"integration"}
	return c
}()

// buildsOnWindows reports whether path is part of a Windows build, by its
// name and its build constraints: a //go:build linux file never runs there.
func buildsOnWindows(t *testing.T, path string) bool {
	t.Helper()
	ok, err := windowsBuild.MatchFile(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		t.Fatalf("reading %s's build constraints: %v", path, err)
	}
	return ok
}

// homeEnvSets reports where body calls Setenv("HOME", ...) on anything, and
// whether it also calls Setenv("USERPROFILE", ...), closures included.
func homeEnvSets(body *ast.BlockStmt) (home token.Pos, profile bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Setenv" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		switch name, _ := strconv.Unquote(lit.Value); name {
		case "HOME":
			if home == token.NoPos {
				home = call.Pos()
			}
		case "USERPROFILE":
			profile = true
		}
		return true
	})
	return home, profile
}

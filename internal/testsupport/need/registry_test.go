// Tests that Names is complete: every name a Require call is given, and every
// name CI's workflow lists in PLEIADES_TEST_REQUIRE, is a registered need.
package need

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot returns the directory holding go.mod, found by walking up from
// the test's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// TestEveryRequireCallNamesAKnownNeed parses every Go file in the module and
// checks each Require call whose need is a string literal against Names, so
// a new need cannot be added without registering it, and CI can list it.
// (A call that passes a constant, such as refusing.Need, takes its value
// from a name in this package, which TestRequire_EveryKnownNameAndAllAreAccepted
// and the compiler hold.)
func TestEveryRequireCallNamesAKnownNeed(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Hidden directories hold worktrees, caches and tool state,
			// not this module's source.
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil // a file that does not parse is the compiler's to report
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if name != "Require" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			need, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			seen[need] = true
			if !slices.Contains(Names, need) {
				t.Errorf("%s: Require(..., %q, ...) names a need that is not in need.Names; register it", fset.Position(call.Pos()), need)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The walk must have found the calls, or a broken parse would pass.
	for _, want := range []string{Docker, Userns, NonRoot} {
		if !seen[want] {
			t.Errorf("no Require(..., %q, ...) call was found: the walk is not seeing the module", want)
		}
	}
}

// TestCIRequireListsOnlyKnownNeeds parses the workflow and checks every
// PLEIADES_TEST_REQUIRE it sets against Names, since an unknown name makes
// the first Require of that job fail.
func TestCIRequireListsOnlyKnownNeeds(t *testing.T) {
	path := filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml")
	data, err := os.ReadFile(path) // #nosec G304 -- the workflow in this module
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^\s*` + Env + `:\s*(\S+)\s*$`)
	matches := line.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("%s sets no %s: the check is not reading the workflow", path, Env)
	}
	for _, m := range matches {
		for _, item := range strings.Split(m[1], ",") {
			if item != All && !slices.Contains(Names, item) {
				t.Errorf("ci.yml lists %q in %s, which is not in need.Names", item, Env)
			}
		}
	}
}

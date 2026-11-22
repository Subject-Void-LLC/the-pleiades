// Package archtest: only internal/credential's ResolveMasterKey makes a
// project's master key, so the rule it holds (a key is made only where nothing
// is sealed under one yet, FAILURE_PATTERNS 1344) is one no writer can skip.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cryptoImportPath is the package whose ResolveKey writes a new key file.
const cryptoImportPath = "github.com/Subject-Void-LLC/the-pleiades/internal/crypto"

// TestOnlyResolveMasterKeyMakesTheMasterKey: across every non-test Go file of
// the module, crypto.ResolveKey (which makes and writes a key when there is
// none) is called only by internal/credential's master_key.go, and inside
// internal/crypto the writer it reaches, generateAndSaveKey, only by
// ResolveKey. Anything else that made a key would make one beside sealed
// material without looking. The controls are that the walk finds the one
// sanctioned call of each.
func TestOnlyResolveMasterKeyMakesTheMasterKey(t *testing.T) {
	root := moduleRoot(t)
	allowed := map[string]string{
		"ResolveKey":         filepath.Join("internal", "credential", "master_key.go"),
		"generateAndSaveKey": filepath.Join("internal", "crypto", "key_resolve.go"),
	}
	found := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Hidden directories (.claude worktrees, .git) and test data are not
			// the module's own source.
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		inCrypto := filepath.Dir(rel) == filepath.Join("internal", "crypto")
		// The names this file imports internal/crypto as.
		aliases := map[string]bool{}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == cryptoImportPath {
				name := "crypto"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				aliases[name] = true
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if x, ok := fun.X.(*ast.Ident); ok && aliases[x.Name] && fun.Sel.Name == "ResolveKey" {
					name = "ResolveKey"
				}
			case *ast.Ident:
				if inCrypto && (fun.Name == "ResolveKey" || fun.Name == "generateAndSaveKey") {
					name = fun.Name
				}
			}
			if name == "" {
				return true
			}
			found[name]++
			if rel != allowed[name] {
				t.Errorf("%s calls %s, which makes a master key file; only %s may, so a key is never made beside material sealed under a lost one: call credential.ResolveMasterKey",
					fset.Position(call.Pos()), name, allowed[name])
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	for name := range allowed {
		if found[name] == 0 {
			t.Errorf("control: found no call of %s, so this test checks nothing", name)
		}
	}
}

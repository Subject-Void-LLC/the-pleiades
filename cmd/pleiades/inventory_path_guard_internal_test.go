// A guard that every command finds the inventory file through
// inventoryFile, so --inventory reaches each one, including a command added
// after the flag was.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInventoryPathComesFromInventoryFile: outside project_paths.go, no
// non-test file of this package passes DefaultInventoryFilename to a
// filepath.Join. A command that built the path itself ignored --inventory:
// revoke-capability --build, added on main while --inventory was swept into
// every other command, read the --inventory file and wrote --dir's.
func TestInventoryPathComesFromInventoryFile(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "project_paths.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fun, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || fun.Sel.Name != "Join" {
				return true
			}
			if pkg, ok := fun.X.(*ast.Ident); !ok || pkg.Name != "filepath" {
				return true
			}
			for _, arg := range call.Args {
				if sel, ok := arg.(*ast.SelectorExpr); ok && sel.Sel.Name == "DefaultInventoryFilename" {
					t.Errorf("%s: builds the inventory path itself, so --inventory never reaches it; call inventoryFile(dir)", fset.Position(call.Pos()))
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("checked no file, so this guard proves nothing")
	}
}

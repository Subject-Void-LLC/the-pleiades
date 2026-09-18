// Tests that pair every exported write hook with an entry in the list of sealed
// columns.
package crypto

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// exportedWriteHooks returns the name of every exported function in this
// package whose name ends in "Hook" and whose result is an ent.Hook: the
// write side of every column this package encrypts.
func exportedWriteHooks(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var hooks []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !strings.HasSuffix(fn.Name.Name, "Hook") {
				continue
			}
			if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			sel, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Hook" {
				continue
			}
			hooks = append(hooks, fn.Name.Name)
		}
	}
	sort.Strings(hooks)
	return hooks
}

// TestEveryEncryptedColumnIsListed fails when this package exports a write
// hook that seals a column encryptedColumns does not list.
//
// That list drives both rotation and the setup command's census, so a
// column missing from it is rewritten by neither and counted by neither.
// The first half of that gap is the defect RotateAll was written to close:
// two of the three passes that existed were never called, and a fourth
// column had no pass at all, so a rotation the production guide described
// as complete left credentials, saved survey answers and mesh signing keys
// on the old key. The second half would be worse, since a census that skips
// a column reports a database holding it as safe to give a new key.
// Pairing each entry with the hook that seals its column, by name, means a
// new encrypted column cannot land without an entry, and an entry cannot
// silently point at a hook that no longer exists.
func TestEveryEncryptedColumnIsListed(t *testing.T) {
	hooks := exportedWriteHooks(t)

	// A control: the query has to find the hooks it is supposed to find, or
	// an empty list on both sides would pass this test vacuously.
	if len(hooks) < 4 {
		t.Fatalf("found %d exported write hooks (%v), want at least the four this package is known to have; the scan is misaimed", len(hooks), hooks)
	}

	var paired []string
	for _, col := range encryptedColumns {
		if col.noun == "" || col.table == "" || col.column == "" || col.rotate == nil {
			t.Errorf("encrypted column for %q is missing its noun, table, column or rotation pass", col.hook)
		}
		paired = append(paired, col.hook)
	}
	sort.Strings(paired)

	if strings.Join(hooks, ",") != strings.Join(paired, ",") {
		t.Fatalf("exported write hooks %v do not match the hooks encryptedColumns lists %v; every column this package seals needs an entry", hooks, paired)
	}
}

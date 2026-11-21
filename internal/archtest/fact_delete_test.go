// Architecture rule for deleting stored fact history: the fact tables are
// insert-only (Phase 41h) to everything but a volatile value's replacement,
// a re-key's merge of two coverages it made the same, and the two repairs of
// a device whose history no controller can serve (internal/facts/entstore's
// repair.go and truncate.go).
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// factHistoryClients are the generated clients of the fact tables, by the
// field name a client or a transaction holds each under.
var factHistoryClients = map[string]bool{"Fact": true, "FactCoverage": true, "FactObservation": true}

// factDeletes are the generated client methods that delete rows.
var factDeletes = map[string]bool{"Delete": true, "DeleteOne": true, "DeleteOneID": true}

// factDeleters are the declarations allowed to delete from a fact table, by
// package directory, each for its reason:
//
//   - writer.recordVolatile: a volatile key keeps one row, replaced when its
//     value changes (store.go); a stable row is never deleted there.
//   - rekeyCoverages: two coverages a re-key makes the same, which only a
//     dropped token can make, are kept as one (rekey_coverages.go).
//   - removal.removeFacts, removeObservations, removeCoverages: the repairs,
//     discard and truncate, each run by an operator on one device by name
//     with a confirmation flag, under the device's exclusive hold, digested
//     and recorded in the activity stream in the same transaction
//     (repair_remove.go).
var factDeleters = map[string]map[string]bool{
	"internal/facts/entstore": {
		"writer.recordVolatile":      true,
		"rekeyCoverages":             true,
		"removal.removeFacts":        true,
		"removal.removeObservations": true,
		"removal.removeCoverages":    true,
	},
}

// factTableDeletes are the statements no production SQL may hold: every
// delete from a fact table goes through a declaration above.
var factTableDeletes = []string{"delete from facts", "delete from fact_coverages", "delete from fact_observations"}

// TestOnlyTheRepairsDeleteFactHistory keeps the fact history insert-only to
// every writer but the ones factDeleters names. A deletion anywhere else
// could cut a device's history so its chain still verifies (cutting values
// off the end is the one edit the chain cannot see), or drop the gathers a
// value's last confirmation is read from. internal/ent defines the methods
// and is not scanned.
func TestOnlyTheRepairsDeleteFactHistory(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]bool{}
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			pkgDir := filepath.ToSlash(filepath.Dir(rel))
			if strings.HasSuffix(rel, "_test.go") || pkgDir == "internal/ent" || strings.HasPrefix(pkgDir, "internal/ent/") {
				return
			}
			for _, use := range factDeleteUses(file) {
				if factDeleters[pkgDir][use.decl] {
					seen[pkgDir+"."+use.decl] = true
					continue
				}
				t.Errorf("%s:%d deletes from ent's %s in %s, which is not one of the writers allowed to. The fact history is "+
					"insert-only: a value changes by a new row, and a device whose history cannot be served is repaired by "+
					"internal/facts/entstore's discard or truncate", rel, fset.Position(use.pos).Line, use.entry, use.decl)
			}
			for _, lit := range factTableDeleteLiterals(file) {
				t.Errorf("%s:%d holds SQL that deletes from a fact table; delete through the writers allowed to", rel, fset.Position(lit).Line)
			}
		})
	}
	// Every listed deleter still deletes, so the list cannot outlive the
	// code it allows.
	for dir, decls := range factDeleters {
		for decl := range decls {
			if !seen[dir+"."+decl] {
				t.Errorf("%s.%s is listed as allowed to delete fact history but deletes none; take it off the list", dir, decl)
			}
		}
	}
}

// TestFactDeleteUses_Control proves both scans see what they look for, so
// an empty result above means something.
func TestFactDeleteUses_Control(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		uses    []string
		deletes int
	}{
		{"a delete through a transaction", `package p
func (r *removal) work(tx *ent.Tx) { _, _ = tx.Fact.Delete().Exec(nil) }`, []string{"removal.work"}, 0},
		{"one row of an observation", `package p
func gone(c *ent.Client) { _ = c.FactObservation.DeleteOneID(1).Exec(nil) }`, []string{"gone"}, 0},
		{"a coverage through a field", `package p
func (w writer) drop() { _, _ = w.tx.FactCoverage.Delete().Exec(nil) }`, []string{"writer.drop"}, 0},
		{"another table's delete is not one", `package p
func work(tx *ent.Tx) { _, _ = tx.Session.Delete().Exec(nil) }`, nil, 0},
		{"a fact query is not one", `package p
func work(tx *ent.Tx) { _ = tx.Fact.Query() }`, nil, 0},
		{"SQL deleting the facts table", `package p
const q = "DELETE FROM facts WHERE id = ?"`, nil, 1},
		{"SQL deleting observations, spaced out", "package p\nconst q = `delete   from\n fact_observations`", nil, 1},
		{"SQL deleting a table merely named like one is not one", `package p
const q = "DELETE FROM facts_archive"`, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			var got []string
			for _, use := range factDeleteUses(file) {
				got = append(got, use.decl)
			}
			if strings.Join(got, ",") != strings.Join(tc.uses, ",") {
				t.Errorf("uses in %v, want %v", got, tc.uses)
			}
			if n := len(factTableDeleteLiterals(file)); n != tc.deletes {
				t.Errorf("%d delete literal(s), want %d", n, tc.deletes)
			}
		})
	}
}

// factDeleteUses returns every call in file of a delete method on a fact
// table's client (X.Fact.Delete(), X.FactObservation.DeleteOneID(...)), by
// the declaration holding it, in factRekeyUse's form.
func factDeleteUses(file *ast.File) []factRekeyUse {
	var uses []factRekeyUse
	collect := func(node ast.Node, decl string) {
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !factDeletes[sel.Sel.Name] {
				return true
			}
			if client, ok := sel.X.(*ast.SelectorExpr); ok && factHistoryClients[client.Sel.Name] {
				uses = append(uses, factRekeyUse{pos: sel.Pos(), entry: client.Sel.Name + "." + sel.Sel.Name, decl: decl})
			}
			return true
		})
	}
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			collect(decl, funcDeclName(decl))
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				if s, ok := spec.(*ast.ValueSpec); ok && len(s.Names) > 0 {
					collect(s, s.Names[0].Name)
				}
			}
		}
	}
	return uses
}

// factTableDeleteLiterals returns the position of every string literal in
// file that holds SQL deleting from a fact table, whatever its case or
// spacing.
func factTableDeleteLiterals(file *ast.File) []token.Pos {
	var out []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		folded := strings.Join(strings.Fields(strings.ToLower(text)), " ")
		for _, stmt := range factTableDeletes {
			// A table name is followed by a space or the end.
			if strings.Contains(folded+" ", stmt+" ") {
				out = append(out, lit.Pos())
				break
			}
		}
		return true
	})
	return out
}

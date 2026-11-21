// Architecture rule for the one write of a stored fact's keyed columns
// (internal/ent's fact_rekey.go): only the re-key of a change of the facts
// key may call it, and no production SQL updates the fact tables.
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

// factRekeyWrites are internal/ent's methods that rewrite columns the schema
// makes immutable: a fact's token, keyed hash, keys and chain link, a
// coverage's tokens and digest, and an observation's coverage.
var factRekeyWrites = map[string]bool{
	"RekeyFact":             true,
	"RekeyFactCoverage":     true,
	"RekeyFactObservations": true,
}

// factRekeyCallers are the declarations allowed to call them, by package
// directory: the re-key itself (internal/facts/entstore's rekey.go), which
// verifies the device under the previous keys first and rewrites it whole in
// one transaction.
var factRekeyCallers = map[string]map[string]bool{
	"internal/facts/entstore": {"Store.rekey": true, "rekeyCoverages": true},
}

// factTableUpdates are the statements no production SQL may hold: the fact
// tables are insert-only to everything but the re-key, which writes through
// the methods above rather than SQL of its own.
var factTableUpdates = []string{"update facts", "update fact_coverages", "update fact_observations"}

// TestOnlyTheRekeyRewritesFactKeys keeps the schema's immutability true for
// every writer but one. The generated builders have no setter for the keyed
// columns (entstore's TestFact_TheSchemaRefusesEditingAValue); the methods
// in fact_rekey.go are the way round that, and a caller that used them for
// anything but a whole, verified re-key could rewrite a history so its chain
// still verified. So outside internal/ent no production file may name one
// except in a declaration factRekeyCallers lists, and none may hold SQL
// that updates a fact table.
func TestOnlyTheRekeyRewritesFactKeys(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]bool{}
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") {
				return
			}
			pkgDir := filepath.ToSlash(filepath.Dir(rel))
			for _, use := range factRekeyUses(file) {
				if factRekeyCallers[pkgDir][use.decl] {
					seen[pkgDir+"."+use.decl] = true
					continue
				}
				t.Errorf("%s:%d calls ent's %s in %s, which is not the re-key. It rewrites columns the schema makes "+
					"immutable; a stored fact changes only by a new row, and a change of the facts key goes through "+
					"internal/facts/entstore's rekey", rel, fset.Position(use.pos).Line, use.entry, use.decl)
			}
			for _, lit := range factTableUpdateLiterals(file) {
				t.Errorf("%s:%d holds SQL that updates a fact table; the fact tables are insert-only", rel, fset.Position(lit).Line)
			}
		})
	}
	// Every listed caller still calls one, so the list cannot outlive the
	// code it allows.
	for dir, decls := range factRekeyCallers {
		for decl := range decls {
			if !seen[dir+"."+decl] {
				t.Errorf("%s.%s is listed as the re-key but calls no fact re-key write; take it off the list", dir, decl)
			}
		}
	}
}

// TestFactRekeyUses_Control proves both scans see what they look for, so an
// empty result above means something. Each snippet is parsed as a real file
// is.
func TestFactRekeyUses_Control(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		uses    []string
		updates int
	}{
		{"a call through a transaction", `package p
func (s *Store) work(tx *ent.Tx) { _ = tx.RekeyFact(nil, 1, 2, ent.FactKeyed{}, ent.FactKeyed{}) }`, []string{"Store.work"}, 0},
		{"a coverage rewrite in a function", `package p
func cover(tx *ent.Tx) { _ = tx.RekeyFactCoverage(nil, 1, 2, "", "", nil, nil) }`, []string{"cover"}, 0},
		{"a method value in a variable", `package p
var rewrite = (*ent.Tx).RekeyFactObservations`, []string{"rewrite"}, 0},
		{"a generated update is not one", `package p
func work(tx *ent.Tx) { _ = tx.Fact.UpdateOneID(1).SetPayload(nil).Exec(nil) }`, nil, 0},
		{"SQL updating the facts table", `package p
const q = "UPDATE facts SET hash = ? WHERE id = ?"`, nil, 1},
		{"SQL updating an observation, in a raw string", "package p\nconst q = `update fact_observations set coverage = $1`", nil, 1},
		{"SQL reading the facts table is not an update", `package p
const q = "SELECT hash FROM facts"`, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			var got []string
			for _, use := range factRekeyUses(file) {
				got = append(got, use.decl)
			}
			if strings.Join(got, ",") != strings.Join(tc.uses, ",") {
				t.Errorf("uses in %v, want %v", got, tc.uses)
			}
			if n := len(factTableUpdateLiterals(file)); n != tc.updates {
				t.Errorf("%d update literal(s), want %d", n, tc.updates)
			}
		})
	}
	if !factRekeyCallers["internal/facts/entstore"]["Store.rekey"] || factRekeyCallers["internal/facts/entstore"]["Store.Record"] {
		t.Fatal("the allowlist does not read the way the rule assumes")
	}
}

// factRekeyUse is one place a file names a fact re-key write.
type factRekeyUse struct {
	pos   token.Pos
	entry string
	// decl is the top-level declaration holding it (funcDeclName's form, or
	// a package-level name).
	decl string
}

// factRekeyUses returns every selector in file naming one of
// factRekeyWrites, by the declaration holding it. It reads names, not types:
// nothing else in the module has a method of these names.
func factRekeyUses(file *ast.File) []factRekeyUse {
	var uses []factRekeyUse
	collect := func(node ast.Node, decl string) {
		ast.Inspect(node, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && factRekeyWrites[sel.Sel.Name] {
				uses = append(uses, factRekeyUse{pos: sel.Pos(), entry: sel.Sel.Name, decl: decl})
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
				switch s := spec.(type) {
				case *ast.ValueSpec:
					if len(s.Names) > 0 {
						collect(s, s.Names[0].Name)
					}
				case *ast.TypeSpec:
					collect(s, s.Name.Name)
				}
			}
		}
	}
	return uses
}

// factTableUpdateLiterals returns the position of every string literal in
// file that holds SQL updating a fact table (factTableUpdates), whatever its
// case or spacing.
func factTableUpdateLiterals(file *ast.File) []token.Pos {
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
		for _, stmt := range factTableUpdates {
			// A table name is followed by a space or the end; "update facts"
			// must not match a table merely starting with it.
			if strings.Contains(folded+" ", stmt+" ") {
				out = append(out, lit.Pos())
				break
			}
		}
		return true
	})
	return out
}

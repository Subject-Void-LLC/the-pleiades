// This file holds the engine's two lists of its own actions to each other:
// engineActionStatKeys (journal_entry.go), which IsBuiltinAction and so
// validation read, and the switch in builtinActionExecutor.Execute
// (action.go), which runs them. Validation once kept a third list that
// drifted and refused the dotted spellings the executor ran; this test is
// what keeps the remaining two from drifting the same way.
package engine

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// noInProcessImplementation is the text Execute's default case returns,
// the mark of a name the switch does not handle.
const noInProcessImplementation = "has no in-process implementation"

// TestEveryBuiltinInTheTableRuns proves the forward direction: every
// non-transport name validation accepts as an engine action reaches a real
// case of the executor's switch, never its default.
func TestEveryBuiltinInTheTableRuns(t *testing.T) {
	exec := NewBuiltinActionExecutor()
	for name, statKeys := range engineActionStatKeys {
		// A transport action declares stat keys and runs on a transport
		// executor, not this switch.
		if statKeys != nil {
			continue
		}
		// A pause with no params waits for nothing, so the call returns at once.
		_, err := exec.Execute(context.Background(), &Task{FQCN: name}, nil)
		if err != nil && strings.Contains(err.Error(), noInProcessImplementation) {
			t.Errorf("%q is an engine action in engineActionStatKeys, but the executor's switch has no case for it", name)
		}
	}
}

// TestEverySwitchCaseIsInTheTable proves the reverse direction by reading
// action.go's source: every name the executor's switch handles is in the
// table, so validation never refuses a name the engine would run.
func TestEverySwitchCaseIsInTheTable(t *testing.T) {
	consts := packageStringConsts(t)
	names := executeSwitchNames(t, consts)
	// A control: the switch has a dozen cases, so an empty or tiny result
	// means the reader went blind rather than that the lists agree.
	if len(names) < 10 {
		t.Fatalf("read only %d case names from Execute's switch (%v); the source reader is broken", len(names), names)
	}
	for _, name := range names {
		if !IsBuiltinAction(name) {
			t.Errorf("the executor's switch runs %q, but engineActionStatKeys lacks it, so validation refuses it", name)
		}
	}
}

// packageStringConsts parses this package's non-test files and returns
// every constant declared as a plain string literal, keyed by name.
func packageStringConsts(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]string{}
	for _, file := range files {
		// Test files declare no constant the switch uses.
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file) // #nosec G304 -- a file of this package, from a Glob in its own directory
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				// Only name = "literal" pairs matter; iota blocks and typed
				// expressions are skipped.
				for i, ident := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					out[ident.Name] = value
				}
			}
		}
	}
	return out
}

// executeSwitchNames returns every name a case of
// builtinActionExecutor.Execute's switch names, resolving a constant through
// consts and failing on any case expression it cannot resolve.
func executeSwitchNames(t *testing.T, consts map[string]string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "action.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		// Only the Execute method of builtinActionExecutor holds the switch.
		if !ok || fn.Name.Name != "Execute" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		recv, ok := fn.Recv.List[0].Type.(*ast.Ident)
		if !ok || recv.Name != "builtinActionExecutor" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				switch e := expr.(type) {
				case *ast.BasicLit:
					value, err := strconv.Unquote(e.Value)
					if err != nil {
						t.Fatal(err)
					}
					names = append(names, value)
				case *ast.Ident:
					value, ok := consts[e.Name]
					if !ok {
						t.Fatalf("case %s in Execute is not a string constant this test can resolve", e.Name)
					}
					names = append(names, value)
				default:
					t.Fatalf("case expression %T in Execute is neither a literal nor a constant", expr)
				}
			}
			return true
		})
	}
	return names
}

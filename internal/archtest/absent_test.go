// Architecture rule for absence (Phase 131, R4): a built-in read-only
// lookup answers a thing that does not exist with exists false, in a run and
// in a check, never with an error, so an agent can branch on it.
//
// The convention it holds: a read-only method that can be asked about
// something absent declares an exists return (bool, returned always), says
// in its Description that an absent one is exists false, supports check, and
// has a test in internal/catalog named for the absent case (Absent or
// Missing in its name) that names the method and runs it in both modes with
// `for _, check := range []bool{false, true}`. The test is what catches the
// failure this rule was written after: virt.cml.lab.info's Doc promised
// exists false and its check answered "cannot be checked" (lab runs 4 and 6).
package archtest

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// existsReturn is the Returns entry an absence-answering lookup declares.
const existsReturn = "exists"

// saysExistsFalse matches a Description that says an absent thing is
// reported as exists false ("exists is false", "reported as exists false").
var saysExistsFalse = regexp.MustCompile(`(?i)\bexists (is )?false\b`)

// absentTestName matches a test named for the absent case.
var absentTestName = regexp.MustCompile(`^Test\w*(Absent|Missing)`)

// TestALookupAnswersAbsenceAsData keeps R4 over every implemented,
// read-only built-in method: one that declares an exists return holds the
// whole convention above, and one whose Description promises exists false
// declares the return it promises.
func TestALookupAnswersAbsenceAsData(t *testing.T) {
	tested := absentTests(t, filepath.Join(repoRoot(t), "internal", "catalog"))
	lookups := 0
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Manifest.Status != collection.StatusImplemented || !desc.Manifest.Reversibility.ReadOnly {
			continue
		}
		if _, ok := returnNamed(desc.Manifest.Doc, existsReturn); ok {
			lookups++
		}
		for _, problem := range absenceProblems(desc, tested) {
			t.Errorf("%s %s (Phase 131, R4: an absent thing is exists false in a run and in a check)", desc.Name, problem)
		}
	}
	if lookups == 0 {
		t.Fatal("no read-only method declares exists, so this test proved nothing")
	}
	t.Logf("%d read-only lookups declare exists and test it absent in both modes", lookups)
}

// absenceProblems returns what desc, a read-only method, lacks of the
// convention, given the methods with an absent-case test in both modes.
func absenceProblems(desc collection.Descriptor, tested map[string]bool) []string {
	doc := desc.Manifest.Doc
	field, declared := returnNamed(doc, existsReturn)
	says := saysExistsFalse.MatchString(doc.Description)
	if !declared {
		if says {
			return []string{"says exists is false in its Description but declares no exists return"}
		}
		return nil
	}
	var problems []string
	if field.Type != "bool" || field.Returned != "always" {
		problems = append(problems, "declares exists as "+field.Type+" returned "+strconv.Quote(field.Returned)+"; declare it a bool returned always")
	}
	if !desc.Manifest.SupportsCheck || desc.Check == nil {
		problems = append(problems, "declares exists and has no check; a check of a lookup is the same read")
	}
	if !says {
		problems = append(problems, "declares exists and its Description does not say an absent one is exists false")
	}
	if !tested[desc.Name] {
		problems = append(problems, "has no test named for the absent case (Absent or Missing) that names it and runs it in both modes with []bool{false, true}")
	}
	return problems
}

// returnNamed is doc's Returns entry called name.
func returnNamed(doc collection.Doc, name string) (collection.ReturnField, bool) {
	for _, r := range doc.Returns {
		if r.Name == name {
			return r, true
		}
	}
	return collection.ReturnField{}, false
}

// absentTests returns every method named by an absent-case test in both
// modes under dir.
func absentTests(t *testing.T, dir string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	walkGoFiles(t, dir, func(path string, file *ast.File, _ *token.FileSet) {
		if !strings.HasSuffix(path, "_test.go") {
			return
		}
		for _, name := range absentTestsIn(file) {
			found[name] = true
		}
	})
	return found
}

// absentTestsIn returns the methods file's absent-case tests name: every
// string literal shaped like a method name inside a Test function named for
// the absent case whose body loops over []bool{false, true}.
func absentTestsIn(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !absentTestName.MatchString(fn.Name.Name) {
			continue
		}
		var literals []string
		bothModes := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil && methodShaped.MatchString(s) {
					literals = append(literals, s)
				}
			case *ast.CompositeLit:
				bothModes = bothModes || isBothModes(n)
			}
			return true
		})
		if bothModes {
			names = append(names, literals...)
		}
	}
	sort.Strings(names)
	return names
}

// methodShaped matches a method name: two or more lower-case dotted parts.
var methodShaped = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// isBothModes reports whether lit is []bool{false, true}.
func isBothModes(lit *ast.CompositeLit) bool {
	arr, ok := lit.Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	if elem, ok := arr.Elt.(*ast.Ident); !ok || elem.Name != "bool" || len(lit.Elts) != 2 {
		return false
	}
	first, ok1 := lit.Elts[0].(*ast.Ident)
	second, ok2 := lit.Elts[1].(*ast.Ident)
	return ok1 && ok2 && first.Name == "false" && second.Name == "true"
}

// TestAbsenceRule_Control proves the rule above sees what it looks for: a
// lookup missing each part of the convention is named, a whole one is not,
// and only a both-modes absent test counts as one.
func TestAbsenceRule_Control(t *testing.T) {
	read := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	whole := collection.Descriptor{
		Name: "x.thing.info",
		Manifest: collection.Manifest{SupportsCheck: true, Doc: collection.Doc{
			Description: "A thing that does not exist is not a failure: exists is false.",
			Returns:     []collection.ReturnField{{Name: "exists", Type: "bool", Returned: "always"}},
		}},
		Invoke: read,
		Check:  read,
	}
	tested := map[string]bool{"x.thing.info": true}
	if got := absenceProblems(whole, tested); len(got) != 0 {
		t.Errorf("a whole lookup was named: %v", got)
	}
	for name, change := range map[string]func(*collection.Descriptor) map[string]bool{
		"exists returned sometimes": func(d *collection.Descriptor) map[string]bool {
			d.Manifest.Doc.Returns = []collection.ReturnField{{Name: "exists", Type: "bool", Returned: "when found"}}
			return tested
		},
		"no check": func(d *collection.Descriptor) map[string]bool {
			d.Manifest.SupportsCheck, d.Check = false, nil
			return tested
		},
		"no word of absence": func(d *collection.Descriptor) map[string]bool {
			d.Manifest.Doc.Description = "Reads a thing."
			return tested
		},
		"no absent test":     func(d *collection.Descriptor) map[string]bool { return map[string]bool{} },
		"promise, no return": func(d *collection.Descriptor) map[string]bool { d.Manifest.Doc.Returns = nil; return tested },
	} {
		d := whole
		d.Manifest.Doc.Returns = append([]collection.ReturnField(nil), whole.Manifest.Doc.Returns...)
		if got := absenceProblems(d, change(&d)); len(got) == 0 {
			t.Errorf("%s: not named", name)
		}
	}

	src := `package p
import "testing"
func TestInfo_AbsentInBothModes(t *testing.T) {
	for _, check := range []bool{false, true} { call(t, "x.thing.info", check) }
}
func TestInfo_MissingInARunOnly(t *testing.T) { call(t, "x.other.info", false) }
func TestInfo_Present(t *testing.T) {
	for _, check := range []bool{false, true} { call(t, "x.third.info", check) }
}`
	file, err := parser.ParseFile(token.NewFileSet(), "control_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := absentTestsIn(file); len(got) != 1 || got[0] != "x.thing.info" {
		t.Errorf("absent tests found = %v, want only x.thing.info", got)
	}
}

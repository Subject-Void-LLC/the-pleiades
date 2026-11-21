// Phase 41b: reading each package's facttest.Main call from its source, so
// the archtest can hold what the call's Options say to what is true of the
// module (which package holds methods, which package drives another's).
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

// Import paths of the two helpers an audit's Options.Unavailable may call,
// each the reason the tests it guards skip.
const (
	needImport        = modulePath + "/internal/testsupport/need"
	testsupportImport = modulePath + "/internal/testsupport"
)

// factAudit is one facttest.Main call a package's TestMain makes, as its
// Options literal reads.
type factAudit struct {
	// pkg is the import path of the package whose test files make the call,
	// and file the file that makes it, by its path in the module.
	pkg, file string
	// of is Options.Of, "" when absent.
	of string
	// unavailable is the helper Options.Unavailable calls
	// ("need.POSIXShellMissing"), "" when absent.
	unavailable string
	// exempt is Options.Exempt, each method with its reason ("" when the
	// reason is not a constant string), and drivenBy Options.DrivenBy.
	exempt, drivenBy map[string]string
	// problems is each way the call could not be read: Options not a
	// literal, Of not a string, Unavailable not one of the two helpers.
	problems []string
}

// audited is the import path of the package whose methods the audit holds:
// Options.Of, or the package making the call.
func (a factAudit) audited() string {
	if a.of != "" {
		return a.of
	}
	return a.pkg
}

// factAudits returns every facttest.Main call a TestMain makes in the module,
// by the import path of the package making it. A directory named testdata,
// or starting with a dot or an underscore, is no package the go command
// builds, and is skipped.
func factAudits(t *testing.T) map[string]factAudit {
	t.Helper()
	root := repoRoot(t)
	out := map[string]factAudit{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		head, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		if importName(head, facttestImport, "facttest") == "" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		pkg := modulePath + "/" + filepath.ToSlash(filepath.Dir(rel))
		for _, a := range readAudits(f, pkg, filepath.ToSlash(rel)) {
			if prior, ok := out[pkg]; ok {
				a.problems = append(a.problems, "a second facttest.Main call beside the one in "+prior.file)
			}
			out[pkg] = a
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// readAudits reads each facttest.Main call in f's TestMain.
func readAudits(f *ast.File, pkg, file string) []factAudit {
	local := importName(f, facttestImport, "facttest")
	var out []factAudit
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil || fn.Name.Name != "TestMain" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelector(call.Fun, local, "Main") {
				return true
			}
			a := factAudit{pkg: pkg, file: file, exempt: map[string]string{}, drivenBy: map[string]string{}}
			if len(call.Args) != 2 {
				a.problems = append(a.problems, "facttest.Main takes the tests and an Options literal")
			} else {
				readOptions(f, call.Args[1], local, &a)
			}
			out = append(out, a)
			return false
		})
	}
	return out
}

// readOptions reads an Options literal into a.
func readOptions(f *ast.File, expr ast.Expr, local string, a *factAudit) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || !isSelector(lit.Type, local, "Options") {
		a.problems = append(a.problems, "its Options is not a facttest.Options literal, so this test cannot read what it says")
		return
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			a.problems = append(a.problems, "its Options literal names no field")
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			a.problems = append(a.problems, "its Options literal names no field")
			continue
		}
		switch key.Name {
		case "Of":
			s, ok := constantString(kv.Value)
			if !ok || s == "" {
				a.problems = append(a.problems, "Options.Of is not a constant import path")
			}
			a.of = s
		case "Unavailable":
			a.unavailable = unavailableHelper(f, kv.Value)
			if a.unavailable == "" {
				a.problems = append(a.problems, "Options.Unavailable is neither need.POSIXShellMissing() nor testsupport.DockerMissing(), the reasons the tests they guard skip")
			}
		case "Exempt", "DrivenBy":
			entries, ok := stringMap(kv.Value)
			if !ok {
				a.problems = append(a.problems, "Options."+key.Name+" is not a map literal with constant method names")
			}
			if key.Name == "Exempt" {
				a.exempt = entries
			} else {
				a.drivenBy = entries
			}
		}
	}
}

// unavailableHelper names the helper expr calls with no argument,
// "need.POSIXShellMissing" or "testsupport.DockerMissing", or "" when it is
// neither.
func unavailableHelper(f *ast.File, expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return ""
	}
	for _, h := range []struct{ path, fallback, name string }{
		{needImport, "need", "POSIXShellMissing"},
		{testsupportImport, "testsupport", "DockerMissing"},
	} {
		if local := importName(f, h.path, h.fallback); local != "" && isSelector(call.Fun, local, h.name) {
			return h.fallback + "." + h.name
		}
	}
	return ""
}

// stringMap reads a map literal whose keys are constant strings, each with
// its value when that is a constant string too ("" when not), and whether
// every key was one.
func stringMap(expr ast.Expr) (map[string]string, bool) {
	out := map[string]string{}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return out, false
	}
	all := true
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			all = false
			continue
		}
		key, ok := constantString(kv.Key)
		if !ok {
			all = false
			continue
		}
		out[key], _ = constantString(kv.Value)
	}
	return out, all
}

// constantString is the value of a string literal, or of literals joined by
// +, and whether expr is one.
func constantString(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := constantString(e.X)
		if !ok {
			return "", false
		}
		y, ok := constantString(e.Y)
		return x + y, ok
	case *ast.ParenExpr:
		return constantString(e.X)
	}
	return "", false
}

// isSelector reports whether expr is pkg.name.
func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

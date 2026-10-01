// The view packages on disk, read from source so the conformance suite's
// reachability check covers every one without a hand-kept list.
package resources_test

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

// viewPackageNames returns the registration key (its Name constant) of
// every view package under this directory. A hand-kept list here is the
// FAILURE_PATTERNS.md #52 gap moved one step: a new view nobody adds to it
// is as unchecked as one nobody adds to registrars.go.
func viewPackageNames(t *testing.T) []string {
	t.Helper()
	dirs, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, dir := range dirs {
		if !dir.IsDir() || strings.HasPrefix(dir.Name(), ".") || dir.Name() == "testdata" {
			continue
		}
		name, ok := viewName(t, dir.Name())
		if !ok {
			t.Errorf("view package %s declares no string constant Name, which every view's registration key is", dir.Name())
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("found no view packages, so the check below would pass over nothing")
	}
	return names
}

// viewName reads the top-level string constant Name from dir's non-test
// Go files.
func viewName(t *testing.T, dir string) (string, bool) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, ident := range value.Names {
					if ident.Name != "Name" || i >= len(value.Values) {
						continue
					}
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("%s: Name %s: %v", path, lit.Value, err)
					}
					return name, true
				}
			}
		}
	}
	return "", false
}

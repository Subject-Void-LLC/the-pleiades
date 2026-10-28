// The static scan behind TestEveryRecordedKeyIsDeclared: which stat and
// fact names the code of one Collection package records.
//
// A key is found where a reader of the source would find it, without type
// checking (which would mean loading the whole module's types in one test):
//
//   - the first argument of an rc.SetStat or rc.EmitFact call, when it is a
//     string literal or a constant (the package's own, or one of another
//     package in this module named through its import);
//   - the keys of a map literal, or the elements of a slice literal, that a
//     for loop ranges over and hands its key to SetStat or EmitFact;
//   - the keys of a map built in a function and then ranged into SetStat,
//     or handed to a package function that does (a recordStats helper):
//     its literal's keys and every constant index assigned into it.
//
// Everything else (a key read from a device, such as an HTTP response's
// header names, or computed at run time) is counted as dynamic and left to
// the method's own tests, since no static rule can name it.
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

// recordCalls are the sdk.RunbookContext methods that put a value where a
// register reads it.
var recordCalls = map[string]bool{"SetStat": true, "EmitFact": true}

// statScan is what the scan found in one package directory.
type statScan struct {
	// keys maps each statically named key to the base names of the files
	// that record it.
	keys map[string]map[string]bool
	// dynamicAt is where each record call whose key the scan could not
	// name sits, for the log.
	dynamicAt []string
}

// constScope resolves the string constants a package's files can name.
type constScope struct {
	root    string
	module  string
	cache   map[string]map[string]string
	t       *testing.T
	fileset *token.FileSet
}

// newConstScope is a resolver for the module rooted at root.
func newConstScope(t *testing.T, root, module string) *constScope {
	return &constScope{root: root, module: module, cache: map[string]map[string]string{}, t: t, fileset: token.NewFileSet()}
}

// parse returns dir's non-test Go files, or nothing for a directory that is
// not there.
func (s *constScope) parse(dir string) []*ast.File {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(s.fileset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			s.t.Fatalf("parsing %s: %v", e.Name(), err)
		}
		files = append(files, f)
	}
	return files
}

// consts is every string constant declared in dir, package level and
// function local alike (a local constant shadowing another name is rare
// enough here that one table serves).
func (s *constScope) consts(dir string) map[string]string {
	if got, ok := s.cache[dir]; ok {
		return got
	}
	out := map[string]string{}
	for _, f := range s.parse(dir) {
		ast.Inspect(f, func(n ast.Node) bool {
			gen, ok := n.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				return true
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if v, ok := stringLit(vs.Values[i]); ok {
							out[name.Name] = v
						}
					}
				}
			}
			return true
		})
	}
	s.cache[dir] = out
	return out
}

// stringLit is expr's value when it is a string literal.
func stringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// fileImports maps each import name a file uses to the directory of the
// module package it names; imports from outside the module are left out.
func (s *constScope) fileImports(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !strings.HasPrefix(path, s.module+"/") {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = filepath.Join(s.root, strings.TrimPrefix(path, s.module+"/"))
	}
	return out
}

// resolve is expr's string value when it is a literal or a constant this
// file can name.
func (s *constScope) resolve(expr ast.Expr, local map[string]string, imports map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return stringLit(e)
	case *ast.Ident:
		v, ok := local[e.Name]
		return v, ok
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		dir, ok := imports[pkg.Name]
		if !ok {
			return "", false
		}
		v, ok := s.consts(dir)[e.Sel.Name]
		return v, ok
	}
	return "", false
}

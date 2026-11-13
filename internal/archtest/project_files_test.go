// This file holds two declarations to the code they describe, so neither
// can fall behind it.
//
// A method that reads or writes a file in the project (sdk.ProjectFiles,
// sdk.ProjectFileWriter) must say which parameter names it, with
// collection.ParamFormatProjectPath (or ParamFormatTemplateFile, which the
// engine reads for it): that format is what refuses such a task on a
// Walk-tier Runner before any task runs (internal/validate's project file
// rule), and what holds an expression in the path to vars. Before Phase
// 127b only two methods declared it, and six more read project files
// unmarked, each refused only once the run reached it.
//
// And internal/forge/catalogdata, the source a method is scaffolded from,
// carries each method's alternatives as the method registered them, so a
// method regenerated from scratch is born admitting the devices its code
// chooses between.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // triggers every Collection package's init()

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// projectFileCalls are the sdk methods through which a method reaches a
// project file.
var projectFileCalls = map[string]bool{"ReadProjectFile": true, "WriteProjectFile": true}

// declaresProjectFile reports whether any parameter of m, or any entry
// field at any depth, names a project file by its format.
func declaresProjectFile(params []collection.Param) bool {
	for _, p := range params {
		if p.Format == collection.ParamFormatProjectPath || p.Format == collection.ParamFormatTemplateFile || declaresProjectFile(p.Entries) {
			return true
		}
	}
	return false
}

// packageOf is the import path of the package fn is defined in, read from
// its symbol name (a/b/c.Func, a/b/c.(*T).M, a/b/c.f.func1).
func packageOf(fn any) string {
	if fn == nil || reflect.ValueOf(fn).IsNil() {
		return ""
	}
	name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	slash := strings.LastIndex(name, "/")
	dot := strings.Index(name[slash+1:], ".")
	if dot < 0 {
		return name
	}
	return name[:slash+1+dot]
}

// projectFileProblems is the rule: every package in readers (packages that
// call a project file method) registers at least one method declaring a
// project file parameter, and every method declaring one is registered by
// a package in readers, or names a template file, which the engine reads.
// declared maps each method's package to whether it declares one.
func projectFileProblems(readers map[string]bool, declared map[string][]bool, templateOnly map[string]bool) []string {
	var problems []string
	for pkg := range readers {
		if !slices.Contains(declared[pkg], true) {
			problems = append(problems, pkg+" reads or writes a project file, and no method it registers declares the parameter naming it with collection.ParamFormatProjectPath")
		}
	}
	for pkg, flags := range declared {
		if slices.Contains(flags, true) && !readers[pkg] && !templateOnly[pkg] {
			problems = append(problems, pkg+" declares a project file parameter and never reads or writes a project file")
		}
	}
	sort.Strings(problems)
	return problems
}

// TestEveryProjectFileReaderDeclaresItsParameter scans internal/catalog for
// calls to ReadProjectFile and WriteProjectFile, and holds each package
// making one to registering a method that declares the project file
// parameter, and the reverse. The negative controls run the rule on a
// reader that declares nothing and a declaration that reads nothing.
func TestEveryProjectFileReaderDeclaresItsParameter(t *testing.T) {
	root := repoRoot(t)
	readers := map[string]bool{}
	walkGoFiles(t, filepath.Join(root, "internal", "catalog"), func(path string, file *ast.File, _ *token.FileSet) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && projectFileCalls[sel.Sel.Name] {
					rel, _ := filepath.Rel(root, filepath.Dir(path))
					readers[modulePath+"/"+filepath.ToSlash(rel)] = true
				}
			}
			return true
		})
	})
	if len(readers) < 8 {
		t.Fatalf("found %d packages reading project files, want at least the eight known: the scan is not reading the tree", len(readers))
	}

	declared := map[string][]bool{}
	templateOnly := map[string]bool{}
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Provider != nil || desc.Invoke == nil {
			continue
		}
		pkg := packageOf(desc.Invoke)
		declared[pkg] = append(declared[pkg], declaresProjectFile(desc.Manifest.Doc.Params))
		for _, p := range desc.Manifest.Doc.Params {
			if p.Format == collection.ParamFormatTemplateFile {
				templateOnly[pkg] = true
			}
		}
	}
	for _, problem := range projectFileProblems(readers, declared, templateOnly) {
		t.Error(problem)
	}

	// The controls: a reader with no declaration, and a declaration with
	// no reader, are each named.
	if got := projectFileProblems(map[string]bool{"x/reader": true}, map[string][]bool{"x/reader": {false}}, nil); len(got) != 1 || !strings.Contains(got[0], "x/reader reads") {
		t.Errorf("a reader declaring nothing: %q", got)
	}
	if got := projectFileProblems(map[string]bool{}, map[string][]bool{"x/quiet": {true}}, nil); len(got) != 1 || !strings.Contains(got[0], "never reads") {
		t.Errorf("a declaration reading nothing: %q", got)
	}
}

// TestCatalogDataAlternativesMatchTheRegistry: each catalogdata entry's
// Alternatives are the ones its method registered, so a scaffold
// regenerated from that data declares the same kinds of device. The
// control is wait.routes, which declares two.
func TestCatalogDataAlternativesMatchTheRegistry(t *testing.T) {
	withAlternatives := 0
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		if len(desc.Manifest.Alternatives) > 0 {
			withAlternatives++
		}
		if !reflect.DeepEqual(cfg.Alternatives, desc.Manifest.Alternatives) {
			t.Errorf("%s registers alternatives %+v, and catalogdata says %+v", cfg.Name, desc.Manifest.Alternatives, cfg.Alternatives)
		}
	}
	if withAlternatives == 0 {
		t.Fatal("no registered method declares alternatives, so the comparison compared nothing")
	}
}

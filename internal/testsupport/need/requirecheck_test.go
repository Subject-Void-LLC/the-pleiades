// checkRequireCalls, the check behind TestEveryRequireCallNamesAKnownNeed:
// every call of need.Require or testsupport.Require in a module, resolved
// through the type checker to the need it names.
//
// A need written as a literal is read as written. One written as a name is
// resolved: a constant (in the package or imported, need.Docker,
// refusing.Need) by its constant value; an unexported package-level
// variable initialized from a constant and never assigned again by that
// value. A parameter of the function the call is in makes that function a
// forwarder, and then each call of it in its package is resolved the same
// way. Anything else (a loop variable, a field, a function's result, a
// closure's parameter, an exported forwarder, whose callers in other
// packages are not seen) is refused unless the enclosing function is
// listed with a reason, so a name nothing can check is never passed over
// as the literal-only check used to pass it. A testsupport.Pin literal's
// What, which testsupport.Download hands to Require, is held to the same
// rule, and so is Require used as a value rather than called.
//
// Each package that calls Require is type-checked from source with
// everything it imports reduced to declarations: a module package's
// function bodies are dropped, and a package outside the module is empty.
// Errors those leave are ignored; only the values of the need arguments
// are read, and those are constants or local declarations.
package need

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// requireCheck is what checkRequireCalls found.
type requireCheck struct {
	// problems are each a position and what is wrong there.
	problems []string
	// seen is every need a call resolved to, known or not.
	seen map[string]bool
}

// requireChecker holds one module's check.
type requireChecker struct {
	root, module string
	known        []string
	allow        map[string]string
	allowUsed    map[string]bool
	fset         *token.FileSet
	cache        map[string]*types.Package
	loading      map[string]bool
	out          requireCheck
}

// checkRequireCalls checks every Require call and Pin literal in the
// module at root against known. allow lists, by package path and function
// name ("path.Func", or "path.Type.Method"), the functions whose need
// arguments cannot be resolved here, each with the reason they are safe;
// an entry nothing needed is itself a problem.
func checkRequireCalls(root string, known []string, allow map[string]string) (requireCheck, error) {
	module, err := modulePath(root)
	if err != nil {
		return requireCheck{}, err
	}
	c := &requireChecker{
		root: root, module: module, known: known, allow: allow, allowUsed: map[string]bool{},
		fset: token.NewFileSet(), cache: map[string]*types.Package{}, loading: map[string]bool{},
		out: requireCheck{seen: map[string]bool{}},
	}
	groups, err := c.candidates()
	if err != nil {
		return requireCheck{}, err
	}
	for _, g := range groups {
		c.checkPackage(g)
	}
	for key := range allow {
		if !c.allowUsed[key] {
			c.problem(token.NoPos, "allow lists %s, which no call needed; remove it", key)
		}
	}
	sort.Strings(c.out.problems)
	return c.out, nil
}

// modulePath reads the module's path from root's go.mod.
func modulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod")) // #nosec G304 -- the module's own go.mod
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(scan.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", errors.New("go.mod names no module")
}

// entryPaths are the packages whose Require is checked.
func (c *requireChecker) entryPaths() []string {
	return []string{c.module + "/internal/testsupport", c.module + "/internal/testsupport/need"}
}

// pkgGroup is one package's directory and name.
type pkgGroup struct{ dir, name string }

// candidates walks the module, as the old literal-only check did (hidden
// directories and node_modules left out), and returns each package one of
// whose files might call Require or build a Pin: a call of anything named
// Require, any mention of Require or Pin through an import of an entry
// package, or a file in an entry package. The type checker decides which
// of those are really the entry points.
func (c *requireChecker) candidates() ([]pkgGroup, error) {
	seen := map[pkgGroup]bool{}
	var out []pkgGroup
	err := filepath.WalkDir(c.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != c.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		file, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil // a file that does not parse is the compiler's to report
		}
		g := pkgGroup{dir: filepath.Dir(p), name: file.Name.Name}
		if !seen[g] && c.mightRequire(g.dir, file) {
			seen[g] = true
			out = append(out, g)
		}
		return nil
	})
	return out, err
}

// mightRequire is candidates' test of one file.
func (c *requireChecker) mightRequire(dir string, file *ast.File) bool {
	if slices.Contains(c.entryPaths(), c.importPath(dir)) {
		return true
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			found = found || n.Name == "Require" || n.Name == "Pin"
		}
		return !found
	})
	return found
}

// importPath is the import path of the package in dir.
func (c *requireChecker) importPath(dir string) string {
	rel, err := filepath.Rel(c.root, dir)
	if err != nil || rel == "." {
		return c.module
	}
	return c.module + "/" + filepath.ToSlash(rel)
}

// Import implements types.Importer: a module package reduced to its
// declarations, and anything else as an empty package.
func (c *requireChecker) Import(p string) (*types.Package, error) {
	if pkg, ok := c.cache[p]; ok {
		return pkg, nil
	}
	empty := func() *types.Package {
		pkg := types.NewPackage(p, path.Base(p))
		pkg.MarkComplete()
		c.cache[p] = pkg
		return pkg
	}
	if p != c.module && !strings.HasPrefix(p, c.module+"/") || c.loading[p] {
		return empty(), nil
	}
	dir := filepath.Join(c.root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, c.module), "/")))
	files := c.parseDir(dir, "")
	if len(files) == 0 {
		return empty(), nil
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				fn.Body = nil // only declarations are read
			}
		}
	}
	c.loading[p] = true
	conf := types.Config{Importer: c, Error: func(error) {}, FakeImportC: true}
	pkg, _ := conf.Check(p, c.fset, files, nil)
	delete(c.loading, p)
	c.cache[p] = pkg
	return pkg, nil
}

// parseDir parses dir's Go files of package name, or, when name is "",
// of the package an import of dir names: the name most of its files that
// are not tests have, its internal tests included.
func (c *requireChecker) parseDir(dir, name string) []*ast.File {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []*ast.File
	count := map[string]int{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(c.fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		files = append(files, f)
		if !strings.HasSuffix(e.Name(), "_test.go") {
			count[f.Name.Name]++
		}
	}
	if name == "" {
		for n, k := range count {
			if k > count[name] || k == count[name] && n < name {
				name = n
			}
		}
	}
	kept := files[:0]
	for _, f := range files {
		if f.Name.Name == name {
			kept = append(kept, f)
		}
	}
	return kept
}

// problem records one problem at pos.
func (c *requireChecker) problem(pos token.Pos, format string, args ...any) {
	where := "allow"
	if pos.IsValid() {
		p := c.fset.Position(pos)
		rel, err := filepath.Rel(c.root, p.Filename)
		if err != nil {
			rel = p.Filename
		}
		where = fmt.Sprintf("%s:%d", filepath.ToSlash(rel), p.Line)
	}
	c.out.problems = append(c.out.problems, where+": "+fmt.Sprintf(format, args...))
}

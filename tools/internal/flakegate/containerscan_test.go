// Which packages' tests start a container, read from the module's own
// source with no build, no type checking and no Docker: the reading behind
// TestEveryContainerPackageIsListed.
package flakegate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// containerVerbs are the docker and podman subcommands that create a
// container, an image or a network, as against ones that only read the
// daemon's state (info, version, inspect, volume ls).
var containerVerbs = map[string]bool{"run": true, "create": true, "start": true, "build": true, "buildx": true, "compose": true}

// testcontainersPath is the prefix of every testcontainers-go package.
const testcontainersPath = "github.com/testcontainers/testcontainers-go"

// majorVersion matches an import path's trailing major version element.
var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// scanRef is one name a function body mentions that may lead to a
// container: a function of one of pkgs, or, when method is set, a method
// of that name declared in any of pkgs, since without types a method
// call's receiver is unknown. Reading every such method as the one called
// over-approximates, which errs toward listing a package.
type scanRef struct {
	pkgs   []string
	name   string
	method bool
}

// scanFacts is what one function body, or one whole test file, says about
// containers: the call in it that starts one directly, if any, whether
// that call is testcontainers-go's (a fixture it starts, waits on and
// keeps) rather than a one-shot docker or podman command, and every name
// it mentions.
type scanFacts struct {
	where   string
	seed    string
	fixture bool
	refs    []scanRef
}

// containerScan is a module read once: each function's facts by key, the
// methods of each package by name, and each package's test files' facts.
type containerScan struct {
	module  string
	funcs   map[string]*scanFacts
	methods map[string]map[string][]string
	tests   map[string][]*scanFacts
	direct  map[string]string
}

// scanForContainers reads every Go file under root, a module whose path is
// module, skipping hidden directories, testdata and node_modules. Each file
// is parsed, reduced to its facts and dropped, so the whole module is never
// held as syntax at once.
func scanForContainers(root, module string) (*containerScan, error) {
	s := &containerScan{module: module, funcs: map[string]*scanFacts{}, methods: map[string]map[string][]string{}, tests: map[string][]*scanFacts{}, direct: map[string]string{}}
	var files []string
	names := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		files = append(files, path)
		if !strings.HasSuffix(path, "_test.go") {
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
			if err != nil {
				return err
			}
			names[s.importPath(root, path)] = f.Name.Name
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		if err := s.readFile(root, path, names); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// importPath is the import path of the package in path's directory.
func (s *containerScan) importPath(root, path string) string {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || rel == "." {
		return s.module
	}
	return s.module + "/" + filepath.ToSlash(rel)
}

// readFile records one file's facts: each function's, for a non-test
// file, or the whole file's, for a test file.
func (s *containerScan) readFile(root, path string, names map[string]string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	pkg := s.importPath(root, path)
	imports := map[string]string{}
	local := []string{pkg}
	for _, imp := range f.Imports {
		ip, _ := strconv.Unquote(imp.Path.Value)
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else if n, ok := names[ip]; ok {
			name = n
		} else {
			name = guessPackageName(ip)
		}
		if name != "_" && name != "." {
			imports[name] = ip
		}
		if ip == s.module || strings.HasPrefix(ip, s.module+"/") {
			local = append(local, ip)
		}
		if strings.HasSuffix(path, "_test.go") && strings.HasPrefix(ip, testcontainersPath) && s.direct[pkg] == "" {
			s.direct[pkg] = fmt.Sprintf("%s imports %s", path, ip)
		}
	}
	if strings.HasSuffix(path, "_test.go") {
		facts := &scanFacts{where: path}
		for _, decl := range f.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
				continue
			}
			s.collect(decl, facts, fset, pkg, imports, local)
		}
		s.tests[pkg] = append(s.tests[pkg], facts)
		return nil
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		key := pkg + "." + fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			key = pkg + ".(" + receiverName(fn.Recv.List[0].Type) + ")." + fn.Name.Name
			if s.methods[pkg] == nil {
				s.methods[pkg] = map[string][]string{}
			}
			s.methods[pkg][fn.Name.Name] = append(s.methods[pkg][fn.Name.Name], key)
		}
		facts := &scanFacts{where: fset.Position(fn.Pos()).String()}
		s.collect(fn.Body, facts, fset, pkg, imports, local)
		s.funcs[key] = facts
	}
	return nil
}

// collect adds what node mentions to facts: a call starting a container
// as its seed, and every function or method name as a reference.
func (s *containerScan) collect(node ast.Node, facts *scanFacts, fset *token.FileSet, pkg string, imports map[string]string, local []string) {
	seen := map[string]bool{}
	add := func(r scanRef) {
		k := fmt.Sprint(r.pkgs, r.name, r.method)
		if !seen[k] {
			seen[k] = true
			facts.refs = append(facts.refs, r)
		}
	}
	var visit func(ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if why, fixture := startsContainer(n, imports); why != "" && (facts.seed == "" || fixture && !facts.fixture) {
				facts.seed, facts.fixture = fmt.Sprintf("%s calls %s", fset.Position(n.Pos()), why), fixture
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				if ip, isImport := imports[id.Name]; isImport {
					if ip == s.module || strings.HasPrefix(ip, s.module+"/") {
						add(scanRef{pkgs: []string{ip}, name: n.Sel.Name})
					}
					return false
				}
			}
			add(scanRef{pkgs: local, name: n.Sel.Name, method: true})
			ast.Inspect(n.X, visit)
			return false
		case *ast.Ident:
			add(scanRef{pkgs: []string{pkg}, name: n.Name})
		}
		return true
	}
	ast.Inspect(node, visit)
}

// startsContainer says what call starts a container, or "" when it starts
// none, and whether it is a testcontainers-go fixture: a testcontainers-go
// function creating a container or a network is, and the docker or podman
// command run with a verb that creates one is not.
func startsContainer(call *ast.CallExpr, imports map[string]string) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	ip := imports[id.Name]
	name := sel.Sel.Name
	if strings.HasPrefix(ip, testcontainersPath) {
		if name == "GenericContainer" || name == "GenericNetwork" || name == "Run" || (name == "New" && strings.HasSuffix(ip, "/network")) {
			return ip + "." + name, true
		}
		return "", false
	}
	if ip != "os/exec" || (name != "Command" && name != "CommandContext") {
		return "", false
	}
	args := call.Args
	if name == "CommandContext" && len(args) > 0 {
		args = args[1:]
	}
	if len(args) < 2 {
		return "", false
	}
	program := stringLiteral(args[0])
	if program != "docker" && program != "podman" {
		return "", false
	}
	for _, a := range args[1:] {
		v := stringLiteral(a)
		if v == "" || strings.HasPrefix(v, "-") {
			continue
		}
		if containerVerbs[v] {
			return program + " " + v, false
		}
		return "", false
	}
	return "", false
}

// stringLiteral is e's value when it is a string literal, else "".
func stringLiteral(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return v
}

// receiverName is a method receiver's type name, without a pointer or
// type parameters.
func receiverName(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return "?"
		}
	}
}

// guessPackageName is the name an import outside the module is known by
// when the import does not name it: its last path element, skipping a
// major version and dropping a "go-" prefix or "-go" suffix, so
// testcontainers-go is testcontainers.
func guessPackageName(ip string) string {
	parts := strings.Split(ip, "/")
	last := parts[len(parts)-1]
	if majorVersion.MatchString(last) && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	last = strings.TrimSuffix(strings.TrimPrefix(last, "go-"), "-go")
	return strings.ReplaceAll(last, "-", "_")
}

// starters is every function or method that starts a container, directly
// or through another, each with the step that makes it one: its own call,
// or the key of the one it reaches. With fixtures set, only a
// testcontainers-go start counts.
func (s *containerScan) starters(fixtures bool) map[string]string {
	why := map[string]string{}
	for key, f := range s.funcs {
		if f.seed != "" && (f.fixture || !fixtures) {
			why[key] = f.seed
		}
	}
	for changed := true; changed; {
		changed = false
		for key, f := range s.funcs {
			if why[key] != "" {
				continue
			}
			if via := s.reaches(f, why); via != "" {
				why[key] = "calls " + via
				changed = true
			}
		}
	}
	return why
}

// reaches is the first starter f mentions, or "".
func (s *containerScan) reaches(f *scanFacts, why map[string]string) string {
	for _, r := range f.refs {
		for _, pkg := range r.pkgs {
			if !r.method {
				if key := pkg + "." + r.name; why[key] != "" {
					return key
				}
				continue
			}
			for _, key := range s.methods[pkg][r.name] {
				if why[key] != "" {
					return key
				}
			}
		}
	}
	return ""
}

// containerPackages is every package whose tests start a container, each
// with the chain of calls that shows it. With fixtures set, only a
// testcontainers-go start counts, and importing testcontainers-go does.
func (s *containerScan) containerPackages(fixtures bool) map[string]string {
	why := s.starters(fixtures)
	out := map[string]string{}
	for pkg, reason := range s.direct {
		out[pkg] = reason
	}
	for pkg, files := range s.tests {
		if out[pkg] != "" {
			continue
		}
		for _, f := range files {
			if f.seed != "" && (f.fixture || !fixtures) {
				out[pkg] = f.seed
				break
			}
			if via := s.reaches(f, why); via != "" {
				out[pkg] = fmt.Sprintf("%s reaches %s", f.where, chain(via, why))
				break
			}
		}
	}
	return out
}

// chain spells out how key starts a container: each function it calls on
// the way, ending at the call that starts one.
func chain(key string, why map[string]string) string {
	steps := []string{key}
	for range 8 {
		next, ok := strings.CutPrefix(why[key], "calls ")
		if !ok {
			return strings.Join(steps, ", which calls ") + ", where " + why[key]
		}
		steps, key = append(steps, next), next
	}
	return strings.Join(steps, ", which calls ")
}

// TestContainerScan_FindsEveryWayATestStartsAContainer reads a fixture
// module whose packages start a container every way the scan follows (a
// direct import, a helper through another helper, a method, the podman
// command, a same-package helper, an aliased import building an image)
// and two that only look (a helper asking the daemon its version, a wait
// strategy, a method that starts nothing), and requires exactly the first
// kind.
func TestContainerScan_FindsEveryWayATestStartsAContainer(t *testing.T) {
	s, err := scanForContainers(filepath.Join("testdata", "containerscan"), "example.com/scan")
	if err != nil {
		t.Fatal(err)
	}
	got := s.containerPackages(false)
	names := func(found map[string]string) string {
		var out []string
		for pkg := range found {
			out = append(out, strings.TrimPrefix(pkg, "example.com/scan/"))
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	if want := "cli direct external local method transitive"; names(got) != want {
		t.Errorf("packages starting a container = %v, want %v", names(got), want)
	}
	if want := "direct local method transitive"; names(s.containerPackages(true)) != want {
		t.Errorf("packages starting a testcontainers-go fixture = %v, want %v", names(s.containerPackages(true)), want)
	}
	for pkg, reason := range map[string]string{
		"transitive": "example.com/scan/helper.Indirect, which calls example.com/scan/helper.Start, where ",
		"cli":        "calls podman run",
		"external":   "calls docker build",
		"direct":     "imports github.com/testcontainers/testcontainers-go",
		"local":      "calls github.com/testcontainers/testcontainers-go/network.New",
	} {
		if !strings.Contains(got["example.com/scan/"+pkg], reason) {
			t.Errorf("%s's reason = %q, want it to say %q", pkg, got["example.com/scan/"+pkg], reason)
		}
	}
	if _, err := scanForContainers(filepath.Join("testdata", "no-such-module"), "example.com/none"); !os.IsNotExist(err) {
		t.Errorf("scanning a missing directory: %v, want it not found", err)
	}
}

// TestGuessPackageName_ReadsTheNameAnImportIsKnownBy covers the import
// paths whose package name is not simply their last element.
func TestGuessPackageName_ReadsTheNameAnImportIsKnownBy(t *testing.T) {
	for path, want := range map[string]string{
		"github.com/testcontainers/testcontainers-go":         "testcontainers",
		"github.com/testcontainers/testcontainers-go/network": "network",
		"go.yaml.in/yaml/v3":                                  "yaml",
		"github.com/mattn/go-sqlite3":                         "sqlite3",
		"os/exec":                                             "exec",
		"example.com/two-words":                               "two_words",
	} {
		if got := guessPackageName(path); got != want {
			t.Errorf("guessPackageName(%q) = %q, want %q", path, got, want)
		}
	}
}

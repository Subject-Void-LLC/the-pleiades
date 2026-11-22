// Package archtest: the engine reaches devices' facts through its own port
// (Phase 41).
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// factsPkg is the import path of the shared fact rules and the Crawl store.
const factsPkg = modulePath + "/internal/facts"

// TestEngineReachesFactsOnlyThroughItsPort: internal/engine may use
// internal/facts (the shared rules, which import no store), but never the
// Controller's fact store, internal/facts/entstore, by any path; a store is
// wired in a cmd/ composition root behind engine.FactRecorder, as the journal
// is behind engine.Journal. (The engine reaches the ent layer through other
// packages, internal/inventory among them, which this does not judge; that it
// imports none directly is TestEngineNeverImportsPersistence's rule.)
func TestEngineReachesFactsOnlyThroughItsPort(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/internal/engine")
	if len(pkgs) == 0 {
		t.Fatalf("go list matched no packages for %s/internal/engine, so this rule examined nothing", modulePath)
	}
	sawFacts := false
	for _, pkg := range pkgs {
		for _, dep := range append(append([]string{}, pkg.Imports...), pkg.Deps...) {
			switch {
			case dep == factsPkg:
				sawFacts = true
			case strings.HasPrefix(dep, factsPkg+"/"):
				t.Errorf("%s reaches %s: the engine stores facts only through engine.FactRecorder", pkg.ImportPath, dep)
			}
		}
	}
	if !sawFacts {
		t.Error("internal/engine no longer reaches internal/facts, so this rule is about nothing: update or remove it")
	}
}

// factsHandedIn are names factsBeyondThePort refuses that the engine may still
// name, each because a composition root builds the value and hands it in. Its
// constructors stay refused: the engine uses one, never makes one.
var factsHandedIn = map[string]string{
	"Keyer": "WithFactDigestKeyer takes the digest keyer a composition root built from the project's or the Controller's key",
}

// TestEngineNamesNoFactStoreOrKey: the import rule above lets the engine
// import internal/facts, and that package holds the Crawl CLI's store and the
// keys that seal it beside the shared rules, so an import rule alone would let
// the engine open .pleiades/facts past FactReader and FactRecorder. No
// production file of internal/engine may name a concrete store, an opener or
// constructor of one, or key material (factsBeyondThePort, derived from the
// package itself), except a value factsHandedIn lists. Everything else stays
// allowed: the Store interface (a port, not a store), entries, observations,
// provenance, guards and specs, the digest field names and ScrubDigests, the
// sentinel errors (ErrNoStore, ErrNoKeys, ErrSealed, ErrUnsealed, which name an
// outcome and open nothing), and functions such as VerifyChain that take keys
// the engine cannot name or make. What another package the engine imports
// does with the store is not judged here.
func TestEngineNamesNoFactStoreOrKey(t *testing.T) {
	root := repoRoot(t)
	refused := factsBeyondThePort(t, filepath.Join(root, "internal", "facts"))
	for _, want := range []string{"FileStore", "OpenFileStore", "NewFileStore", "Keys", "NewKeys", "Keyer", "NewKeyer"} {
		if refused[want] == "" {
			t.Fatalf("factsBeyondThePort no longer finds %s, so it has gone blind or the package changed: update the rule", want)
		}
	}
	engineDir := filepath.Join(root, "internal", "engine")
	seen := map[string]bool{}
	walkGoFiles(t, engineDir, func(path string, file *ast.File, fset *token.FileSet) {
		if filepath.Dir(path) != engineDir || strings.HasSuffix(path, "_test.go") {
			return
		}
		rel, _ := filepath.Rel(root, path)
		for _, use := range factsNamesUsed(file) {
			why := refused[use.name]
			switch {
			case use.name == ".":
				t.Errorf("%s:%d dot-imports internal/facts, which hides every name this rule reads", rel, fset.Position(use.pos).Line)
			case why == "":
			case factsHandedIn[use.name] != "":
				seen[use.name] = true
			default:
				t.Errorf("%s:%d names facts.%s, %s: the engine reads facts through FactReader and stores them through "+
					"FactRecorder, which a cmd/ composition root wires", rel, fset.Position(use.pos).Line, use.name, why)
			}
		}
	})
	for name := range factsHandedIn {
		if refused[name] == "" {
			t.Errorf("factsHandedIn lists %s, which the rule does not refuse: take it off the list", name)
		} else if !seen[name] {
			t.Errorf("factsHandedIn lists %s, which internal/engine no longer names: take it off the list", name)
		}
	}
}

// TestFactsBeyondThePort_Control proves the derivation and the scan see what
// they look for, so an empty result above means something.
func TestFactsBeyondThePort_Control(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(`package facts
type Store interface { Current(); History() }
type Entry struct{}
type DiskStore struct{}
func (s *DiskStore) Current() {}
func (s *DiskStore) History() {}
type half struct{}
func (half) Current() {}
type Seal struct{}
func NewSeal(secret []byte) *Seal { return nil }
func NewLazySeal(resolve func() ([]byte, error)) (*Seal, error) { return nil, nil }
func OpenDisk(dir string) (*DiskStore, error) { return nil, nil }
func Prepare(raw []byte) ([]Entry, error) { return nil, nil }
func Check(s *Seal) bool { return false }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := factsBeyondThePort(t, dir)
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	if want := "DiskStore,NewLazySeal,NewSeal,OpenDisk,Seal"; strings.Join(names, ",") != want {
		t.Fatalf("refused %v, want %s (an interface, a plain type, a function taking raw bytes and one taking a key are not)", names, want)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "e.go", `package engine
import f "`+factsPkg+`"
import other "example.com/other"
var _ = f.OpenFileStore
func g(k *f.Keyer) { _ = other.OpenFileStore }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	var used []string
	for _, use := range factsNamesUsed(file) {
		used = append(used, use.name)
	}
	if strings.Join(used, ",") != "OpenFileStore,Keyer" {
		t.Fatalf("names used %v, want OpenFileStore and Keyer through the alias, and nothing of another package", used)
	}
}

// factsName is one name a file uses from internal/facts.
type factsName struct {
	pos  token.Pos
	name string
}

// factsNamesUsed is every facts.<Name> selector in file, through whatever
// name it imports internal/facts by; a dot import is reported as ".".
func factsNamesUsed(file *ast.File) []factsName {
	name := importName(file, factsPkg, "facts")
	if name == "" {
		return nil
	}
	if name == "." {
		return []factsName{{pos: file.Package, name: "."}}
	}
	var out []factsName
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, isIdent := sel.X.(*ast.Ident); isIdent && x.Name == name {
				out = append(out, factsName{pos: sel.Pos(), name: sel.Sel.Name})
			}
		}
		return true
	})
	return out
}

// factsBeyondThePort reads the production files of the package in dir (not
// its subpackages) and returns, by name, why each of these exported names
// is beyond the engine's port:
//
//   - a concrete store: an exported non-interface type whose methods include
//     every method of the package's Store interface;
//   - key material: an exported type a New function returns when it takes a
//     secret, as []byte or as a func() ([]byte, error) resolving one;
//   - an opener or constructor: an exported function returning either.
func factsBeyondThePort(t *testing.T, dir string) map[string]string {
	t.Helper()
	var files []*ast.File
	walkGoFiles(t, dir, func(path string, file *ast.File, _ *token.FileSet) {
		if filepath.Dir(path) == dir && !strings.HasSuffix(path, "_test.go") {
			files = append(files, file)
		}
	})
	storeMethods := map[string]bool{}
	methods := map[string]map[string]bool{}
	var funcs []*ast.FuncDecl
	for _, file := range files {
		for _, d := range file.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Recv == nil {
					funcs = append(funcs, decl)
					continue
				}
				if recv := strings.TrimSuffix(funcDeclName(decl), "."+decl.Name.Name); recv != decl.Name.Name {
					if methods[recv] == nil {
						methods[recv] = map[string]bool{}
					}
					methods[recv][decl.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == "Store" {
						if it, isIface := ts.Type.(*ast.InterfaceType); isIface {
							for _, m := range it.Methods.List {
								for _, n := range m.Names {
									storeMethods[n.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	if len(storeMethods) == 0 {
		t.Fatalf("%s declares no Store interface with methods, so no concrete store can be told from one", dir)
	}
	refused := map[string]string{}
	for typ, ms := range methods {
		all := ast.IsExported(typ)
		for m := range storeMethods {
			all = all && ms[m]
		}
		if all {
			refused[typ] = "a concrete store"
		}
	}
	for _, fn := range funcs {
		if strings.HasPrefix(fn.Name.Name, "New") && takesSecret(fn.Type) {
			for _, typ := range resultTypes(fn.Type) {
				if ast.IsExported(typ) {
					refused[typ] = "key material"
				}
			}
		}
	}
	for _, fn := range funcs {
		if !ast.IsExported(fn.Name.Name) {
			continue
		}
		for _, typ := range resultTypes(fn.Type) {
			if why := refused[typ]; why == "a concrete store" || why == "key material" {
				refused[fn.Name.Name] = "an opener or constructor of " + typ
			}
		}
	}
	return refused
}

// takesSecret reports whether a function takes a []byte or a func() ([]byte,
// error): a secret, or a way to resolve one.
func takesSecret(ft *ast.FuncType) bool {
	isBytes := func(e ast.Expr) bool {
		arr, ok := e.(*ast.ArrayType)
		if !ok || arr.Len != nil {
			return false
		}
		id, ok := arr.Elt.(*ast.Ident)
		return ok && id.Name == "byte"
	}
	for _, p := range ft.Params.List {
		if fn, ok := p.Type.(*ast.FuncType); isBytes(p.Type) || ok && fn.Results != nil && len(fn.Results.List) == 2 && isBytes(fn.Results.List[0].Type) {
			return true
		}
	}
	return false
}

// resultTypes names each result a function returns as a package-local type,
// T or *T.
func resultTypes(ft *ast.FuncType) []string {
	if ft.Results == nil {
		return nil
	}
	var out []string
	for _, r := range ft.Results.List {
		typ := r.Type
		if star, ok := typ.(*ast.StarExpr); ok {
			typ = star.X
		}
		if id, ok := typ.(*ast.Ident); ok {
			out = append(out, id.Name)
		}
	}
	return out
}

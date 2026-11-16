// Architecture rules for the TLS key log (pleiades run --tls-keylog): it is
// the command line's alone. A Runner and the Controller can never write one,
// and a dispatch can never carry one, because nothing they run can reach the
// two calls that make and carry a key log, nothing outside pkg/devicetls sets
// a tls.Config's KeyLogWriter, and the messages between the tiers have no
// field to hold one.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// devicetlsPkg is the import path of the one place device TLS is built.
const devicetlsPkg = modulePath + "/pkg/devicetls"

// keyLogEntries are pkg/devicetls' names that make a key log or put one on
// a run's context.
var keyLogEntries = map[string]bool{
	"CreateKeyLog": true,
	"WithKeyLog":   true,
}

// keyLogCallers are the command line's declarations that make the run's key
// log (startTLSKeyLog) and carry it on the run's context (carryOut), by
// package directory: pleiades run and adhoc --tls-keylog, and nothing else.
var keyLogCallers = map[string]map[string]bool{
	"cmd/pleiades": {"startTLSKeyLog": true, "carryOut": true},
}

// TestOnlyTheCommandLineMakesATLSKeyLog keeps the key log the command line's:
// outside pkg/devicetls, no production file may name CreateKeyLog or
// WithKeyLog except in a declaration keyLogCallers lists. That is what makes
// the Walk tier unable to write one: cmd/runner, cmd/controller and
// everything they link have no way to create a key log or hand one to a
// client, whatever a dispatch says.
func TestOnlyTheCommandLineMakesATLSKeyLog(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]bool{}
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			pkgDir := filepath.ToSlash(filepath.Dir(rel))
			if strings.HasSuffix(rel, "_test.go") || pkgDir == "pkg/devicetls" {
				return
			}
			for _, use := range keyLogUses(file) {
				if keyLogCallers[pkgDir][use.decl] {
					seen[pkgDir+"."+use.decl] = true
					continue
				}
				t.Errorf("%s:%d names devicetls.%s in %s. A TLS key log decrypts every session it covers, credentials "+
					"included, so only pleiades run and adhoc --tls-keylog make one; a Runner or the Controller never may. "+
					"A new command taking the flag is listed in keyLogCallers with its reason", rel, fset.Position(use.pos).Line, use.entry, use.decl)
			}
		})
	}
	// Every listed declaration still makes or carries the key log, so the
	// list cannot outlive the code it allows.
	for dir, decls := range keyLogCallers {
		for decl := range decls {
			if !seen[dir+"."+decl] {
				t.Errorf("%s.%s is listed as making or carrying the TLS key log but names neither entry; take it off the list", dir, decl)
			}
		}
	}
}

// TestOnlyDevicetlsSetsAKeyLogWriter keeps every TLS key log behind the one
// opt-in: outside pkg/devicetls, no production file sets a KeyLogWriter, as
// a field (cfg.KeyLogWriter = w) or in a literal (tls.Config{KeyLogWriter:
// w}). A second way to write one would be a second way past the flag.
func TestOnlyDevicetlsSetsAKeyLogWriter(t *testing.T) {
	root := repoRoot(t)
	owner := 0
	for _, dir := range seamScanRoots {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if strings.HasSuffix(rel, "_test.go") {
				return
			}
			found := keyLogWriterSets(file)
			if filepath.ToSlash(filepath.Dir(rel)) == "pkg/devicetls" {
				owner += len(found)
				return
			}
			for _, pos := range found {
				t.Errorf("%s:%d sets a KeyLogWriter. TLS session secrets are written only through pkg/devicetls' "+
					"ConfigContext, under a run's --tls-keylog: build the configuration there instead", rel, fset.Position(pos).Line)
			}
		})
	}
	// The positive control: the owner still sets it, so the matcher matches.
	if owner == 0 {
		t.Fatal("pkg/devicetls sets no KeyLogWriter, so the matcher is matching nothing")
	}
}

// TestNoDispatchCarriesATLSKeyLog holds the messages between the tiers, a
// dispatch's payload and the Runner's per-task child request and response,
// to having no field that names a key log, by Go name or JSON name, at any
// depth: a field of a nested struct, or of the element of a pointer, slice,
// array or map. So a Controller cannot ask a Runner for one and a child
// cannot hand one back. A field typed any is not looked inside, since its
// shape is not known until it is decoded.
func TestNoDispatchCarriesATLSKeyLog(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[wire.DispatchPayload](),
		reflect.TypeFor[wire.ChildRequest](),
		reflect.TypeFor[wire.ChildResponse](),
	} {
		found, walked := keyLogFields(typ)
		if walked == 0 {
			t.Fatalf("%s has no fields, so this rule checks nothing", typ)
		}
		for _, f := range found {
			t.Errorf("%s names a TLS key log: a key log is the command line's alone and never crosses to a Runner", f)
		}
	}
}

// keyLogFields returns every field of typ, at any depth, whose Go or JSON
// name speaks of a key log, each as its dotted path from typ, and how many
// fields it walked. Each struct type is walked once, so a type that holds
// itself ends the walk.
func keyLogFields(typ reflect.Type) ([]string, int) {
	var found []string
	walked := 0
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type, at string)
	walk = func(t reflect.Type, at string) {
		for {
			switch t.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Array:
				t = t.Elem()
				continue
			case reflect.Map:
				walk(t.Key(), at+"[key]")
				t = t.Elem()
				continue
			}
			break
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		for i := range t.NumField() {
			f := t.Field(i)
			walked++
			path := at + "." + f.Name
			if mentionsKeyLog(f.Name) || mentionsKeyLog(f.Tag.Get("json")) {
				found = append(found, fmt.Sprintf("%s (json %q)", path, f.Tag.Get("json")))
			}
			walk(f.Type, path)
		}
	}
	walk(typ, typ.String())
	return found, walked
}

// keyLogControlInner and keyLogControlPayload are a payload holding a key log
// field only at depth, for TestKeyLogMatchers_Control.
type keyLogControlInner struct {
	Path string `json:"tls_key_log_path"`
}

// keyLogControlPayload hides its key log fields in a nested struct and in
// the element of a slice, beside a map of the same element and itself.
type keyLogControlPayload struct {
	Name  string `json:"name"`
	Inner struct {
		KeyLog string
	}
	List   []keyLogControlInner
	ByName map[string]*keyLogControlInner
	Self   *keyLogControlPayload
}

// mentionsKeyLog reports whether name speaks of a key log: keylog, key_log,
// KeyLog or SSLKEYLOGFILE, in any case.
func mentionsKeyLog(name string) bool {
	folded := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(name), "_", ""), "-", "")
	return strings.Contains(folded, "keylog")
}

// TestKeyLogMatchers_Control proves each scan sees what it looks for, so an
// empty result above means something.
func TestKeyLogMatchers_Control(t *testing.T) {
	cases := []struct {
		name string
		src  string
		uses []string
		sets int
	}{
		{"a call to CreateKeyLog", `package p
import "` + devicetlsPkg + `"
func start() { _, _ = devicetls.CreateKeyLog("x") }`, []string{"start"}, 0},
		{"an aliased import is still seen", `package p
import d "` + devicetlsPkg + `"
func (r *runner) carry(ctx context.Context) { _ = d.WithKeyLog(ctx, nil) }`, []string{"runner.carry"}, 0},
		{"another package's WithKeyLog is not this one", `package p
import "example.com/other"
func work() { _ = other.WithKeyLog() }`, nil, 0},
		{"ConfigContext is not an entry", `package p
import "` + devicetlsPkg + `"
func work(s devicetls.Settings) { _, _ = s.ConfigContext(nil, nil) }`, nil, 0},
		{"a field set", `package p
func work(cfg *tls.Config, w io.Writer) { cfg.KeyLogWriter = w }`, nil, 1},
		{"a literal", `package p
var cfg = &tls.Config{MinVersion: tls.VersionTLS12, KeyLogWriter: os.Stderr}`, nil, 1},
		{"a dot import names it bare", `package p
import . "` + devicetlsPkg + `"
func start() { _, _ = CreateKeyLog("x") }
func carry(ctx context.Context) { _ = WithKeyLog(ctx, nil) }`, []string{"start", "carry"}, 0},
		{"a dot import's other package's selector is not this one", `package p
import . "` + devicetlsPkg + `"
import "example.com/other"
func work() { _ = other.CreateKeyLog; _ = Settings{} }`, nil, 0},
		{"a bare name without the dot import is not this one", `package p
import "` + devicetlsPkg + `"
func work() { _ = CreateKeyLog; _ = devicetls.Settings{} }`, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the control source: %v", err)
			}
			var got []string
			for _, use := range keyLogUses(file) {
				got = append(got, use.decl)
			}
			if strings.Join(got, ",") != strings.Join(tc.uses, ",") {
				t.Errorf("uses in %v, want %v", got, tc.uses)
			}
			if sets := len(keyLogWriterSets(file)); sets != tc.sets {
				t.Errorf("%d KeyLogWriter sets, want %d", sets, tc.sets)
			}
		})
	}
	for name, want := range map[string]bool{"TLSKeyLog": true, "tls_key_log": true, "SSLKEYLOGFILE": true, "tls-keylog": true, "Secrets": false, "Key": false} {
		if mentionsKeyLog(name) != want {
			t.Errorf("mentionsKeyLog(%q) = %t, want %t", name, !want, want)
		}
	}
	// The field walk finds a key log field in a nested struct and in a
	// slice's element (the map's element is the same type, walked once), and
	// ends at the type that holds itself.
	found, walked := keyLogFields(reflect.TypeFor[keyLogControlPayload]())
	want := []string{
		`archtest.keyLogControlPayload.Inner.KeyLog (json "")`,
		`archtest.keyLogControlPayload.List.Path (json "tls_key_log_path")`,
	}
	if strings.Join(found, "|") != strings.Join(want, "|") || walked < 7 {
		t.Errorf("keyLogFields found %q after %d fields, want %q", found, walked, want)
	}
}

// keyLogUse is one place a file names a key log entry.
type keyLogUse struct {
	pos   token.Pos
	entry string
	// decl is the top-level declaration holding it, as funcDeclName names it.
	decl string
}

// keyLogUses returns every place file names one of keyLogEntries through its
// import of pkg/devicetls: as name.Entry, or as a bare Entry where the file
// dot-imports the package (a selector's own name, other.Entry, is not one).
func keyLogUses(file *ast.File) []keyLogUse {
	name := importName(file, devicetlsPkg, "devicetls")
	if name == "" {
		return nil
	}
	var uses []keyLogUse
	collect := func(node ast.Node, decl string) {
		// The names a selector picks, which are never a dot import's bare use.
		picked := map[*ast.Ident]bool{}
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				picked[n.Sel] = true
				if x, isIdent := n.X.(*ast.Ident); isIdent && x.Name == name && keyLogEntries[n.Sel.Name] {
					uses = append(uses, keyLogUse{pos: n.Pos(), entry: n.Sel.Name, decl: decl})
				}
			case *ast.Ident:
				if name == "." && !picked[n] && keyLogEntries[n.Name] {
					uses = append(uses, keyLogUse{pos: n.Pos(), entry: n.Name, decl: decl})
				}
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

// keyLogWriterSets returns every place file sets a KeyLogWriter: a selector
// assigned to, or a key in a composite literal.
func keyLogWriterSets(file *ast.File) []token.Pos {
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "KeyLogWriter" {
					found = append(found, sel.Pos())
				}
			}
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && key.Name == "KeyLogWriter" {
				found = append(found, key.Pos())
			}
		}
		return true
	})
	return found
}

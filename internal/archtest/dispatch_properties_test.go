// The rule that a device type's dispatch allowlist is exactly what its
// code reads, and holds no secret.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// propertyKey is the shape every inventory property key has.
var propertyKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// keySources are the packages outside a device package whose property-key
// constants a device package may name (pkg/devicetls's TLS settings,
// pkg/httpapi's plaintext flag, linux's own when generic_ssh embeds it).
var keySources = []string{"pkg/devicetls", "pkg/httpapi", "internal/inventory/devices/linux"}

// propertyConstants returns, for the Go files in dir, every package-level
// string constant that names a property key: by this module's convention
// its name ends in Property or starts with prop.
func propertyConstants(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, file := range parseDir(t, dir) {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i >= len(vs.Values) || !(strings.HasSuffix(name.Name, "Property") || strings.HasPrefix(name.Name, "prop")) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil && propertyKey.MatchString(v) {
							out[name.Name] = v
						}
					}
				}
			}
		}
	}
	return out
}

// parseDir parses dir's non-test Go files.
func parseDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

// keysRead returns the property keys the code in dir reads: string
// literals passed to Properties' String, Int and Bool or to pathProperty;
// every reference to a property-key constant, its own or a key source's;
// and pkg/devicetls's keys when it calls devicetls.Parse.
func keysRead(t *testing.T, root, dir string) map[string]bool {
	t.Helper()
	own := propertyConstants(t, dir)
	foreign := map[string]map[string]string{}
	for _, src := range keySources {
		foreign[filepath.Base(src)] = propertyConstants(t, filepath.Join(root, src))
	}
	read := map[string]bool{}
	for _, file := range parseDir(t, dir) {
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch fun := x.Fun.(type) {
				case *ast.SelectorExpr:
					name = fun.Sel.Name
					if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "devicetls" && (name == "Parse" || name == "Properties") {
						for _, v := range foreign["devicetls"] {
							read[v] = true
						}
					}
					// A type that embeds another device type for its
					// accessors (generic_ssh, linux.Server) reads what that
					// package reads, through its AccessorProperties.
					if pkg, ok := fun.X.(*ast.Ident); ok && name == "AccessorProperties" {
						for k := range keysRead(t, root, filepath.Join(root, "internal/inventory/devices", pkg.Name)) {
							read[k] = true
						}
					}
				case *ast.Ident:
					name = fun.Name
				}
				if (name == "String" || name == "Int" || name == "Bool" || name == "pathProperty") && len(x.Args) >= 1 {
					if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil && propertyKey.MatchString(v) {
							read[v] = true
						}
					}
				}
			case *ast.Ident:
				if v, ok := own[x.Name]; ok {
					read[v] = true
				}
			case *ast.SelectorExpr:
				if pkg, ok := x.X.(*ast.Ident); ok {
					if v, ok := foreign[pkg.Name][x.Sel.Name]; ok {
						read[v] = true
					}
				}
			}
			return true
		})
	}
	return read
}

// typesByPackage groups the registered device types by the directory
// (relative to the module root) of the package that registered them.
func typesByPackage(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for typ, ctor := range record.AllTypes() {
		fn := runtime.FuncForPC(reflect.ValueOf(ctor).Pointer()).Name()
		pkgPath := fn[:strings.LastIndex(fn, ".")]
		rel := strings.TrimPrefix(pkgPath, modulePath+"/")
		out[rel] = append(out[rel], typ)
	}
	return out
}

// TestDispatchPropertiesAreWhatTheCodeReads asserts every registered
// device type declares its dispatch properties, and that each package's
// declared keys are exactly the keys its code reads: a key read and not
// declared would leave an accessor empty on a Runner, and one declared and
// not read would put a property on the message stream for nothing. No
// declared key may name a secret.
func TestDispatchPropertiesAreWhatTheCodeReads(t *testing.T) {
	root := moduleRoot(t)
	packages := typesByPackage(t)
	if len(packages) == 0 {
		t.Fatal("no device types are registered")
	}
	for dir, types := range packages {
		slices.Sort(types)
		declared := map[string]bool{}
		for _, typ := range types {
			keys, ok := record.DispatchProperties(typ)
			if !ok {
				t.Errorf("device type %s declares no dispatch properties: call record.RegisterDispatchProperties beside RegisterType", typ)
				continue
			}
			for _, k := range keys {
				declared[k] = true
				if redact.SecretName(k) && !notSecret(t, typ, k) {
					t.Errorf("device type %s declares %q, which names a secret, as a dispatch property: it would ride the message stream", typ, k)
				}
			}
		}
		read := keysRead(t, root, filepath.Join(root, dir))
		if len(read) == 0 && len(declared) > 0 {
			t.Errorf("%s: the matcher found no property read, so it is matching nothing here", dir)
		}
		for k := range read {
			if !declared[k] {
				t.Errorf("%s (%s) reads property %q and no type there declares it: a Runner would rebuild the device without it", dir, strings.Join(types, ", "), k)
			}
		}
		for k := range declared {
			if !read[k] {
				t.Errorf("%s (%s) declares %q and its code does not read it: it would ride the message stream for nothing", dir, strings.Join(types, ", "), k)
			}
		}
	}
}

// secretNamedButValidated lists dispatch keys whose NAME the secret-name
// heuristic flags but whose value the device type validates into a shape
// that cannot hold a secret. Each entry is exact and proven by notSecret,
// which builds the type with a secret-looking value there and requires a
// refusal, so an entry stops excusing anything the moment its validation
// is loosened.
var secretNamedButValidated = map[string]struct {
	reason   string
	baseline map[string]any
}{
	"generic_http/http_auth": {
		reason:   "the credential MODE, validated to one of none, basic or bearer; the credential itself is in the store",
		baseline: map[string]any{"base_url": "https://api.example.com"},
	},
	"generic_http/http_allow_plaintext_credentials": {
		reason:   "a flag validated as a boolean",
		baseline: map[string]any{"base_url": "http://api.example.com", "http_auth": "basic"},
	},
}

// notSecret reports whether typ/key is on secretNamedButValidated and its
// type really refuses a secret-looking value there.
func notSecret(t *testing.T, typ, key string) bool {
	t.Helper()
	entry, ok := secretNamedButValidated[typ+"/"+key]
	if !ok {
		return false
	}
	ctor, _ := record.LookupType(typ)
	props := map[string]any{}
	for k, v := range entry.baseline {
		props[k] = v
	}
	props[key] = "hunter2-looks-like-a-secret"
	if _, err := ctor(record.Record{Name: "probe", Type: typ, Properties: props}); err == nil {
		t.Errorf("%s/%s is excused as %q, and %s accepted a secret-looking value there", typ, key, entry.reason, typ)
		return true
	}
	return true
}

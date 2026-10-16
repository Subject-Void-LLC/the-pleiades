// Package archtest: where the PostgreSQL driver may be imported under
// pkg/, and that the one package that does never lets it read the
// machine it runs on.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// postgresPort is the one pkg/ package allowed to import the driver.
const postgresPort = modulePath + "/pkg/pgclient"

// TestOnlyThePostgresPortImportsTheDriverUnderPkg holds pkg/pgclient's
// confinement: anything a Collection can import reaches a database
// through it, so the choices that keep a run's database from the
// credentials of whoever runs pleiades are made in one place. (Under
// internal/, TestOnlyDesignatedAdaptersImportConcreteDrivers already
// holds the driver to its adapters.) The control: the port itself must be
// found importing the driver.
func TestOnlyThePostgresPortImportsTheDriverUnderPkg(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/pkg/...")
	if len(pkgs) == 0 {
		t.Fatal("go list matched no package under pkg/, so this rule examined nothing")
	}
	portImports := false
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if imp != "github.com/lib/pq" && !strings.HasPrefix(imp, "github.com/lib/pq/") {
				continue
			}
			if pkg.ImportPath == postgresPort {
				portImports = true
				continue
			}
			t.Errorf("%s imports %s; under pkg/ only %s may, so a database is reached through it", pkg.ImportPath, imp, postgresPort)
		}
	}
	if !portImports {
		t.Error("pkg/pgclient imports no github.com/lib/pq package, so this rule's query is misaimed")
	}
}

// The import paths of the driver and of database/sql.
const (
	postgresDriverPath = "github.com/lib/pq"
	databaseSQLPath    = "database/sql"
)

// ambientPostgresEntryPoints are the driver's ways to configuration
// nobody passed, by import path and name: a connection string is read
// together with every PG* environment variable (NewConfig, and through it
// NewConnector, Open, DialOpen, the three listener constructors, the
// Driver type's Open, and sql.Open, which calls that Open), and the
// driver's own TLS reads root.crt and a client certificate from the home
// directory (RegisterTLSConfig).
var ambientPostgresEntryPoints = map[string]bool{
	postgresDriverPath + ".NewConfig":         true,
	postgresDriverPath + ".NewConnector":      true,
	postgresDriverPath + ".Open":              true,
	postgresDriverPath + ".DialOpen":          true,
	postgresDriverPath + ".NewListener":       true,
	postgresDriverPath + ".NewDialListener":   true,
	postgresDriverPath + ".NewListenerConn":   true,
	postgresDriverPath + ".Driver":            true,
	postgresDriverPath + ".RegisterTLSConfig": true,
	databaseSQLPath + ".Open":                 true,
}

// TestThePostgresPortReadsNothingAmbient reads pkg/pgclient's non-test
// files for a use of one of ambientPostgresEntryPoints, and for a TLS
// mode other than disable handed to the driver: TLS is negotiated by the
// port's own dialer, so the driver's never runs. The controls: the port's
// files must be found and parsed, and TestAmbientPostgresUses_Detector
// holds the detector to snippets it must and must not flag.
func TestThePostgresPortReadsNothingAmbient(t *testing.T) {
	pkgs := goList(t, false, postgresPort)
	parsed := 0
	fset := token.NewFileSet()
	for _, pkg := range pkgs {
		for _, name := range pkg.GoFiles {
			path := filepath.Join(pkg.Dir, name)
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			parsed++
			for _, use := range ambientPostgresUses(fset, f) {
				t.Error(use)
			}
		}
	}
	if parsed == 0 {
		t.Error("no file of pkg/pgclient was parsed, so this rule examined nothing")
	}
}

// ambientPostgresUses is every place f reaches configuration nobody
// passed, as messages. A package is known by its import path, whatever
// name the file gives it, so an alias does not hide a use and another
// package that happens to be called pq or sql is not mistaken for one; a
// dot import, whose names cannot be told from the file's own, is refused.
func ambientPostgresUses(fset *token.FileSet, f *ast.File) []string {
	var found []string
	report := func(n ast.Node, format string, args ...any) {
		found = append(found, fset.Position(n.Pos()).String()+": "+fmt.Sprintf(format, args...))
	}
	// names maps each name the file calls a watched package by to its path.
	names := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || (path != postgresDriverPath && path != databaseSQLPath) {
			continue
		}
		name := pathpkg.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case "_":
			// A blank import registers the driver and names nothing; sql.Open
			// is what would then reach it, and is caught there.
		case ".":
			report(imp, "dot-imports %s, so which of its names are used cannot be checked; import it by name", path)
		default:
			names[name] = path
		}
	}
	// driverName reports whether expr is the driver's own name, by
	// whatever name the file imported it under.
	driverName := func(expr ast.Expr, name string) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && names[id.Name] == postgresDriverPath
	}
	// checkSSLMode reports a value given the driver's SSLMode that is not
	// its disable constant. A constant of the driver's own is left to the
	// selector rule below, so it is reported once.
	checkSSLMode := func(value ast.Expr) {
		if sel, ok := value.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "SSLMode") && driverName(sel, sel.Sel.Name) {
			return
		}
		report(value, "hands the driver an SSLMode other than its SSLModeDisable; TLS is negotiated by this package's dialer, never by the driver")
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if key, ok := n.Key.(*ast.Ident); ok && key.Name == "SSLMode" {
				checkSSLMode(n.Value)
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "SSLMode" && i < len(n.Rhs) {
					checkSSLMode(n.Rhs[i])
				}
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && names[id.Name] != "" {
				qualified := names[id.Name] + "." + n.Sel.Name
				if ambientPostgresEntryPoints[qualified] {
					report(n, "uses %s, which reads configuration nobody passed; build the driver's Config and hand it to NewConnectorConfig", qualified)
				}
				if names[id.Name] == postgresDriverPath && strings.HasPrefix(n.Sel.Name, "SSLMode") && n.Sel.Name != "SSLModeDisable" {
					report(n, "hands the driver %s; TLS is negotiated by this package's dialer, never by the driver", qualified)
				}
				return true
			}
			// A method named Driver hands out the driver itself (a
			// connector's, or a sql.DB's), whose Open reads a connection
			// string with every PG* variable.
			if n.Sel.Name == "Driver" {
				report(n, "asks for the driver itself, whose Open reads configuration nobody passed")
			}
		}
		return true
	})
	return found
}

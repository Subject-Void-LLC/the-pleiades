// Package archtest: where the PostgreSQL driver may be imported under
// pkg/, and that the one package that does never lets it read the
// machine it runs on.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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

// ambientPostgresEntryPoints are the driver's ways to configuration
// nobody passed: a connection string is read together with every PG*
// environment variable (NewConfig, and NewConnector and sql.Open through
// it), and the driver's own TLS reads root.crt and a client certificate
// from the home directory.
var ambientPostgresEntryPoints = map[string]bool{
	"pq.NewConfig":         true,
	"pq.NewConnector":      true,
	"pq.RegisterTLSConfig": true,
	"sql.Open":             true,
}

// TestThePostgresPortReadsNothingAmbient reads pkg/pgclient's non-test
// files for a call to one of ambientPostgresEntryPoints, and for a TLS
// mode other than disable handed to the driver: TLS is negotiated by the
// port's own dialer, so the driver's never runs. The control: the port's
// files must be found and parsed.
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
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgName, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				qualified := pkgName.Name + "." + sel.Sel.Name
				if ambientPostgresEntryPoints[qualified] {
					t.Errorf("%s uses %s, which reads configuration nobody passed; build the driver's Config and hand it to NewConnectorConfig", fset.Position(n.Pos()), qualified)
				}
				if pkgName.Name == "pq" && strings.HasPrefix(sel.Sel.Name, "SSLMode") && sel.Sel.Name != "SSLModeDisable" {
					t.Errorf("%s hands the driver %s; TLS is negotiated by this package's dialer, never by the driver", fset.Position(n.Pos()), qualified)
				}
				return true
			})
		}
	}
	if parsed == 0 {
		t.Error("no file of pkg/pgclient was parsed, so this rule examined nothing")
	}
}

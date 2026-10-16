// Package archtest: the PostgreSQL port's ambient-configuration detector,
// on snippets it must and must not flag, so a detector that matched
// nothing could not pass.
package archtest

import (
	"go/parser"
	"go/token"
	"testing"
)

// TestAmbientPostgresUses_Detector: every driver entry point that reads a
// connection string with the PG* environment, or hands the driver a TLS
// mode, is flagged however its package is imported, and nothing is
// flagged for a package that merely shares the driver's name.
func TestAmbientPostgresUses_Detector(t *testing.T) {
	const pq = "import \"github.com/lib/pq\"\n"
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"NewConfig":                      {pq + `func f() { _, _ = pq.NewConfig("") }`, 1},
		"NewConnector":                   {pq + `func f() { _, _ = pq.NewConnector("") }`, 1},
		"Open":                           {pq + `func f() { _, _ = pq.Open("") }`, 1},
		"DialOpen":                       {pq + `func f(d pq.Dialer) { _, _ = pq.DialOpen(d, "") }`, 1},
		"NewListener":                    {pq + `func f() { _ = pq.NewListener("", 0, 0, nil) }`, 1},
		"NewDialListener":                {pq + `func f(d pq.Dialer) { _ = pq.NewDialListener(d, "", 0, 0, nil) }`, 1},
		"NewListenerConn":                {pq + `func f() { _, _ = pq.NewListenerConn("", nil) }`, 1},
		"RegisterTLSConfig":              {pq + `func f() { _ = pq.RegisterTLSConfig("x", nil) }`, 1},
		"the Driver type":                {pq + `func f() { _, _ = pq.Driver{}.Open("") }`, 1},
		"a connector's Driver":           {pq + `func f(c *pq.Connector) { _, _ = c.Driver().Open("") }`, 1},
		"sql.Open":                       {"import \"database/sql\"\n" + `func f() { _, _ = sql.Open("postgres", "") }`, 1},
		"the driver under an alias":      {"import p \"github.com/lib/pq\"\n" + `func f() { _, _ = p.NewConnector("") }`, 1},
		"database/sql under an alias":    {"import dbsql \"database/sql\"\n" + `func f() { _, _ = dbsql.Open("postgres", "") }`, 1},
		"a dot import of the driver":     {"import . \"github.com/lib/pq\"\n" + `func f() { _, _ = NewConfig("") }`, 1},
		"a TLS mode under an alias":      {"import p \"github.com/lib/pq\"\n" + `var c = p.Config{SSLMode: p.SSLModeVerifyFull}`, 1},
		"a TLS mode spelled as a string": {pq + `var c = pq.Config{SSLMode: "require"}`, 1},
		"a TLS mode assigned":            {pq + `func f(c *pq.Config) { c.SSLMode = "verify-full" }`, 1},
		"what the port does":             {pq + "import \"database/sql\"\n" + `func f(c pq.Config) { c.SSLMode = pq.SSLModeDisable; n, _ := pq.NewConnectorConfig(pq.Config{SSLMode: pq.SSLModeDisable}); _ = sql.OpenDB(n) }`, 0},
		"another package named pq":       {"import pq \"example.com/notpq\"\n" + `func f() { pq.Open(); pq.NewConfig() }`, 0},
		"another package named sql":      {"import sql \"example.com/notsql\"\n" + `func f() { sql.Open() }`, 0},
	} {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "snippet.go", "package p\n"+tc.src+"\n", 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := ambientPostgresUses(fset, f); len(got) != tc.want {
				t.Errorf("flagged %d, want %d: %q", len(got), tc.want, got)
			}
		})
	}
}

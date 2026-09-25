// The rule that pkg/filters, the functions a runbook's expressions call,
// reach no network, open no file for writing and start no process.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// filtersForbiddenImports are packages whose only purpose is I/O a filter
// has no business doing.
var filtersForbiddenImports = map[string]bool{
	"os": true, "os/exec": true, "net/http": true, "io/ioutil": true, "syscall": true, "plugin": true,
}

// filtersNetIO are the net functions that touch the network, rather than
// parse an address, which is all a filter may use the package for.
var filtersNetIO = []string{"Dial", "Listen", "Lookup", "Resolve", "FileConn", "FileListener", "FilePacketConn"}

// filtersIOExceptions are the two reads a filter does make, named so that
// a new one has to be argued for here: time.LoadLocation reads the zone
// database (ShiftTimezone, whose name is bounded first and which the
// standard library refuses when it contains ".." or starts with "/"), and
// crypto/rand reads the operating system's entropy (GenerateRandomPassword
// and SecureCompare).
var filtersIOExceptions = map[string]bool{"time.LoadLocation": true, "rand.Reader": true, "rand.Int": true, "rand.Read": true}

// filtersIOSites returns every forbidden import and every network call in
// file, and the exception sites it uses.
func filtersIOSites(file *ast.File) (forbidden []string, exceptions []string) {
	names := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if filtersForbiddenImports[path] {
			forbidden = append(forbidden, "import "+path)
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = path
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path := names[pkg.Name]
		call := filepath.Base(path) + "." + sel.Sel.Name
		switch {
		case path == "net":
			for _, prefix := range filtersNetIO {
				if strings.HasPrefix(sel.Sel.Name, prefix) {
					forbidden = append(forbidden, call)
				}
			}
		case path == "time" && sel.Sel.Name == "LoadLocation", path == "crypto/rand":
			if filtersIOExceptions[call] {
				exceptions = append(exceptions, call)
			} else {
				forbidden = append(forbidden, call)
			}
		}
		return true
	})
	return forbidden, exceptions
}

// TestFiltersDoNoNetworkFileOrProcessIO asserts pkg/filters imports no
// I/O package and calls no network function, its only reads being the
// two named exceptions. Several phases' hardening items (53, 54, 55, 57)
// rest on "no function in this phase performs I/O"; an import audit by
// eye is what they cited, and it missed that ShiftTimezone reads the zone
// database. This makes the claim exact and keeps it true.
func TestFiltersDoNoNetworkFileOrProcessIO(t *testing.T) {
	dir := filepath.Join(moduleRoot(t), "pkg", "filters")
	files := parseDir(t, dir)
	if len(files) == 0 {
		t.Fatal("no pkg/filters files were scanned")
	}
	used := map[string]bool{}
	for _, file := range files {
		forbidden, exceptions := filtersIOSites(file)
		for _, f := range forbidden {
			t.Errorf("pkg/filters (%s) uses %s: a filter reaches no network, writes no file and starts no process", file.Name.Name, f)
		}
		for _, e := range exceptions {
			used[e] = true
		}
	}
	// The positive control: the named reads are still there, so the
	// matcher still finds what it looks for.
	if !used["time.LoadLocation"] {
		t.Error("pkg/filters no longer calls time.LoadLocation: remove it from filtersIOExceptions and the comment, or the matcher is blind")
	}
}

// TestFiltersIOSitesDetects is the negative control.
func TestFiltersIOSitesDetects(t *testing.T) {
	const src = `package probe

import (
	stdnet "net"
	"os"
	"time"
	"crypto/rand"
)

var (
	a, _ = stdnet.Dial("tcp", "x")
	b, _ = stdnet.LookupHost("x")
	c    = stdnet.ParseIP("10.0.0.1")
	d, _ = os.Open("x")
	e, _ = time.LoadLocation("UTC")
	f    = rand.Reader
)
`
	file, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	forbidden, exceptions := filtersIOSites(file)
	if len(forbidden) != 3 || len(exceptions) != 2 {
		t.Fatalf("forbidden %v, exceptions %v; want the import of os, Dial and LookupHost, and the two exceptions", forbidden, exceptions)
	}
}

// The rule that only pkg/devicetls may lower TLS: name a version below
// TLS 1.2, or a cipher suite Go does not offer by default.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// downgradeOwner is the one package allowed to lower TLS, behind a device
// record's explicit, per-device flags.
const downgradeOwner = "pkg/devicetls"

// weakTLSSelectors returns every place file names crypto/tls's weak
// versions or legacy cipher suites: VersionTLS10, VersionTLS11,
// VersionSSL30, InsecureCipherSuites, and any suite constant with RSA key
// exchange, RC4 or 3DES in its name.
func weakTLSSelectors(file *ast.File) []token.Pos {
	tlsName := ""
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "crypto/tls" {
			tlsName = "tls"
			if imp.Name != nil {
				tlsName = imp.Name.Name
			}
		}
	}
	if tlsName == "" {
		return nil
	}
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != tlsName {
			return true
		}
		name := sel.Sel.Name
		switch {
		case name == "VersionTLS10", name == "VersionTLS11", name == "VersionSSL30", name == "InsecureCipherSuites",
			strings.HasPrefix(name, "TLS_RSA_WITH_"), strings.Contains(name, "_RC4_"), strings.Contains(name, "3DES"):
			found = append(found, sel.Pos())
		}
		return true
	})
	return found
}

// TestOnlyDevicetlsLowersTLS asserts no shipping code outside
// pkg/devicetls names a TLS version below 1.2 or a legacy cipher suite.
// A device may be reached that way only through its own record's flags,
// which pkg/devicetls reads and warns about; anywhere else, a weak
// setting would be a downgrade no flag asked for.
func TestOnlyDevicetlsLowersTLS(t *testing.T) {
	root := moduleRoot(t)
	var checked, owned int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		for _, pos := range weakTLSSelectors(file) {
			if filepath.ToSlash(filepath.Dir(rel)) == downgradeOwner {
				owned++
				continue
			}
			t.Errorf("%s:%d names a TLS version below 1.2 or a legacy cipher suite. Only %s may, behind a device record's explicit flags, so a weakening is always asked for and always warned about",
				rel, fset.Position(pos).Line, downgradeOwner)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go files were scanned")
	}
	// The positive control: the owner still does it, so the matcher is
	// matching something.
	if owned == 0 {
		t.Fatalf("%s names no weak version or suite, so this matcher matches nothing; if the package no longer lowers TLS, delete this rule", downgradeOwner)
	}
}

// TestWeakTLSSelectorsDetects is the negative control: each weak name is
// found, under an aliased import too, and a strong one is not.
func TestWeakTLSSelectorsDetects(t *testing.T) {
	const src = `package probe

import ctls "crypto/tls"

var (
	a = ctls.VersionTLS10
	b = ctls.VersionTLS11
	c = ctls.InsecureCipherSuites
	d = ctls.TLS_RSA_WITH_AES_128_GCM_SHA256
	e = ctls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA
	f = ctls.TLS_ECDHE_RSA_WITH_RC4_128_SHA
	strong = ctls.VersionTLS12
	suite  = ctls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
)
`
	file, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(weakTLSSelectors(file)); got != 6 {
		t.Fatalf("found %d weak names, want 6", got)
	}
}

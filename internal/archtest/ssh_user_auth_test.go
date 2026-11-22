// The rule that a login's authentication reaches golang.org/x/crypto/ssh in one
// place: pkg/remoteexec's Auth.clientConfig, which wraps a stored key so its
// signature is held to SHA-2 unless the device's record allows a legacy one
// (pkg/remoteexec's userkey.go). A key handed to the library anywhere else
// would sign with ssh-rsa (SHA-1) whenever a server did not advertise SHA-2,
// with no record asked and no warning given.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// userAuthOwner is the one file that may hand the library a login's methods.
const userAuthOwner = "pkg/remoteexec/auth.go"

// userAuthExempt is internal/testsupport, which logs in to test containers
// and which TestTestsupportNeverImportedByProductionCode keeps out of every
// shipped binary.
var userAuthExempt = []string{"internal/testsupport"}

// userAuthUses returns every place file hands the library a login's
// authentication: a call to PublicKeys or PublicKeysCallback, an
// ssh.ClientConfig literal that sets Auth or AuthCallback, or, in a file
// importing the library, an assignment to a field named Auth or AuthCallback.
func userAuthUses(file *ast.File) []token.Pos {
	sshName := ""
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "golang.org/x/crypto/ssh" {
			sshName = "ssh"
			if imp.Name != nil {
				sshName = imp.Name.Name
			}
		}
	}
	if sshName == "" {
		return nil
	}
	isAuthField := func(name string) bool { return name == "Auth" || name == "AuthCallback" }
	isSSH := func(e ast.Expr, name string) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == sshName && sel.Sel.Name == name
	}
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isSSH(x.Fun, "PublicKeys") || isSSH(x.Fun, "PublicKeysCallback") {
				found = append(found, x.Pos())
			}
		case *ast.CompositeLit:
			if !isSSH(x.Type, "ClientConfig") {
				return true
			}
			for _, elt := range x.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && isAuthField(key.Name) {
						found = append(found, kv.Pos())
					}
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && isAuthField(sel.Sel.Name) {
					found = append(found, sel.Pos())
				}
			}
		}
		return true
	})
	return found
}

// TestOnlyRemoteexecHandsTheLibraryALogin asserts no shipping Go file but
// pkg/remoteexec/auth.go hands golang.org/x/crypto/ssh a login's
// authentication, so every stored key signs through the wrapper that holds it
// to the device's record.
func TestOnlyRemoteexecHandsTheLibraryALogin(t *testing.T) {
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
		rel = filepath.ToSlash(rel)
		exempt := slices.ContainsFunc(userAuthExempt, func(dir string) bool { return strings.HasPrefix(rel, dir+"/") })
		for _, pos := range userAuthUses(file) {
			switch {
			case rel == userAuthOwner:
				owned++
			case exempt:
			default:
				t.Errorf("%s:%d hands golang.org/x/crypto/ssh a login's authentication. Only %s may, through Auth.clientConfig, which holds a stored key's signature to the device's record",
					rel, fset.Position(pos).Line, userAuthOwner)
			}
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
		t.Fatalf("%s hands the library no login, so this matcher matches nothing; if logins moved, move this rule with them", userAuthOwner)
	}
}

// TestUserAuthUsesDetects is the negative control: each form is found under
// an aliased import, and a host key scan's ClientConfig with no Auth, and a
// file that does not import the library, are not.
func TestUserAuthUsesDetects(t *testing.T) {
	const src = `package probe

import xssh "golang.org/x/crypto/ssh"

func f(s xssh.Signer, cfg *xssh.ClientConfig) {
	_ = xssh.PublicKeys(s)
	_ = xssh.PublicKeysCallback(nil)
	_ = &xssh.ClientConfig{User: "u", Auth: nil}
	_ = xssh.ClientConfig{AuthCallback: nil}
	cfg.Auth = nil
	_ = &xssh.ClientConfig{User: "scan", HostKeyAlgorithms: nil}
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(userAuthUses(file)); got != 5 {
		t.Fatalf("found %d logins, want 5", got)
	}
	const other = `package probe

type config struct{ Auth string }

func g(c *config) { c.Auth = "x" }
`
	file, err = parser.ParseFile(token.NewFileSet(), "other.go", other, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := userAuthUses(file); got != nil {
		t.Fatalf("a file not importing the library: found %d logins", len(got))
	}
}

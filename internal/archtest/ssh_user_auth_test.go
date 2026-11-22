// The rule that a login's authentication reaches an SSH library only from the
// files that hold a stored key's signature to SHA-2 first: pkg/remoteexec's
// Auth.clientConfig, which wraps a key so it signs ssh-rsa (SHA-1) only where
// the device's record allows it, and internal/project's git transport, which
// replaces go-git's parsed signer with remoteexec.HoldToSHA2. A key handed to
// golang.org/x/crypto/ssh, or to go-git's SSH transport, from any other file
// would sign with ssh-rsa whenever a server did not advertise SHA-2, with no
// record asked and no warning given.
//
// What it checks is where a login is handed over, read from the source: a
// call or a literal of the forms below. It cannot follow a key through a
// variable, so a file it names is one whose key handling a reviewer reads,
// and each must still make the call that holds the key (userAuthOwners).
// Out of its reach entirely: a key handed to a program that does its own SSH,
// ansible-playbook or an external Collection that does not use sdk.Connect.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// userAuthOwners are the files that may hand an SSH library a login, each
// with the call it must make to hold a stored key's signature first.
var userAuthOwners = map[string]string{
	"pkg/remoteexec/auth.go":  "newUserKeySigner",
	"internal/project/git.go": "HoldToSHA2",
}

// userAuthExempt is internal/testsupport, which logs in to test containers
// and which TestTestsupportNeverImportedByProductionCode keeps out of every
// shipped binary.
var userAuthExempt = []string{"internal/testsupport"}

// The SSH libraries a login can be handed to.
const (
	cryptoSSHPath = "golang.org/x/crypto/ssh"
	goGitSSHPath  = "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// fromPackage reports whether e names one of names from the package file
// imports as pkg: a selector pkg.Name, or a bare Name under a dot import.
func fromPackage(e ast.Expr, pkg string, names ...string) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		return ok && pkg != "" && pkg != "." && id.Name == pkg && slices.Contains(names, x.Sel.Name)
	case *ast.Ident:
		return pkg == "." && slices.Contains(names, x.Name)
	}
	return false
}

// userAuthUses returns every place file hands an SSH library a login's
// authentication. From golang.org/x/crypto/ssh: a call to PublicKeys or
// PublicKeysCallback, a ClientConfig literal setting Auth or AuthCallback, or
// an assignment to a field named Auth or AuthCallback. From go-git's SSH
// transport: a call to NewPublicKeys, NewPublicKeysFromFile or
// NewSSHAgentAuth, a PublicKeys or PublicKeysCallback literal, or an
// assignment to a field named Signer. Either library may be imported under
// any name, or with a dot (importName, winrm_wait_test.go, returns ".").
func userAuthUses(file *ast.File) []token.Pos {
	cryptoName := importName(file, cryptoSSHPath, "ssh")
	gitName := importName(file, goGitSSHPath, "ssh")
	if cryptoName == "" && gitName == "" {
		return nil
	}
	isAuthField := func(name string) bool { return name == "Auth" || name == "AuthCallback" }
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if fromPackage(x.Fun, cryptoName, "PublicKeys", "PublicKeysCallback") || fromPackage(x.Fun, gitName, "NewPublicKeys", "NewPublicKeysFromFile", "NewSSHAgentAuth") {
				found = append(found, x.Pos())
			}
		case *ast.CompositeLit:
			if fromPackage(x.Type, gitName, "PublicKeys", "PublicKeysCallback") {
				found = append(found, x.Pos())
				return true
			}
			if !fromPackage(x.Type, cryptoName, "ClientConfig") {
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
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if (cryptoName != "" && isAuthField(sel.Sel.Name)) || (gitName != "" && sel.Sel.Name == "Signer") {
					found = append(found, sel.Pos())
				}
			}
		}
		return true
	})
	return found
}

// calls reports whether file calls a function or method named name.
func calls(file *ast.File, name string) bool {
	called := false
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				called = called || fun.Name == name
			case *ast.SelectorExpr:
				called = called || fun.Sel.Name == name
			}
		}
		return !called
	})
	return called
}

// TestOnlyTheHoldingFilesHandAnSSHLibraryALogin asserts no shipping Go file
// but the userAuthOwners hands golang.org/x/crypto/ssh or go-git's SSH
// transport a login's authentication, and that each owner still makes the
// call that holds a stored key's signature to SHA-2. It checks where a login
// is handed over, not every path a key could take inside those files.
func TestOnlyTheHoldingFilesHandAnSSHLibraryALogin(t *testing.T) {
	root := moduleRoot(t)
	var checked int
	owned := map[string]int{}
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
		uses := userAuthUses(file)
		for _, pos := range uses {
			switch _, owner := userAuthOwners[rel]; {
			case owner:
				owned[rel]++
			case exempt:
			default:
				t.Errorf("%s:%d hands an SSH library a login's authentication. Only %v may, each holding a stored key's signature to SHA-2 first",
					rel, fset.Position(pos).Line, slices.Sorted(maps.Keys(userAuthOwners)))
			}
		}
		if hold, owner := userAuthOwners[rel]; owner && len(uses) > 0 && !calls(file, hold) {
			t.Errorf("%s hands an SSH library a login and no longer calls %s, which holds the key's signature to SHA-2", rel, hold)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go files were scanned")
	}
	// The positive control: each owner still does it, so the matcher is
	// matching something there.
	for owner := range userAuthOwners {
		if owned[owner] == 0 {
			t.Errorf("%s hands an SSH library no login, so this matcher matches nothing there; if logins moved, move this rule with them", owner)
		}
	}
}

// TestUserAuthUsesDetects is the negative control: each form is found under
// an aliased import and under a dot import, for both libraries, and a host
// key scan's ClientConfig with no Auth, and a file that imports neither
// library, are not.
func TestUserAuthUsesDetects(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want int
	}{
		"x/crypto under an alias": {`package probe

import xssh "golang.org/x/crypto/ssh"

func f(s xssh.Signer, cfg *xssh.ClientConfig) {
	_ = xssh.PublicKeys(s)
	_ = xssh.PublicKeysCallback(nil)
	_ = &xssh.ClientConfig{User: "u", Auth: nil}
	_ = xssh.ClientConfig{AuthCallback: nil}
	cfg.Auth = nil
	_ = &xssh.ClientConfig{User: "scan", HostKeyAlgorithms: nil}
}
`, 5},
		"x/crypto under a dot import": {`package probe

import . "golang.org/x/crypto/ssh"

func f(s Signer) {
	_ = PublicKeys(s)
	_ = &ClientConfig{Auth: nil}
	_ = &ClientConfig{User: "scan"}
}
`, 2},
		"go-git under an alias": {`package probe

import (
	xssh "golang.org/x/crypto/ssh"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

func f(s xssh.Signer, k *gitssh.PublicKeys) {
	_, _ = gitssh.NewPublicKeys("git", nil, "")
	_, _ = gitssh.NewPublicKeysFromFile("git", "key", "")
	_, _ = gitssh.NewSSHAgentAuth("git")
	_ = &gitssh.PublicKeys{User: "git", Signer: s}
	_ = &gitssh.PublicKeysCallback{User: "git"}
	k.Signer = s
	_ = &gitssh.Password{User: "git"}
}
`, 6},
		"go-git under a dot import": {`package probe

import . "github.com/go-git/go-git/v5/plumbing/transport/ssh"

func f() {
	_, _ = NewPublicKeys("git", nil, "")
	_ = &PublicKeys{User: "git"}
}
`, 2},
		"neither library": {`package probe

type config struct{ Auth, Signer string }

func g(c *config) { c.Auth, c.Signer = "x", "y" }
`, 0},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "probe.go", tc.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := len(userAuthUses(file)); got != tc.want {
			t.Errorf("%s: found %d logins, want %d", name, got, tc.want)
		}
	}

	// An owner that hands go-git a key without holding it is told so: calls
	// finds the hold through a selector, and misses it when it is gone.
	const held = `package probe

func f() { k.Signer, _ = remoteexec.HoldToSHA2(k.Signer) }
`
	const bare = `package probe

func f() { k.Signer = s }
`
	for src, want := range map[string]bool{held: true, bare: false} {
		file, err := parser.ParseFile(token.NewFileSet(), "owner.go", src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		if got := calls(file, userAuthOwners["internal/project/git.go"]); got != want {
			t.Errorf("calls(%q) = %v, want %v", src, got, want)
		}
	}
}

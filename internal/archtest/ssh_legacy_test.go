// The rule that only pkg/remoteexec may weaken SSH: name an algorithm
// golang.org/x/crypto/ssh calls insecure (SHA-1 key exchange, ssh-rsa and DSA
// host keys, CBC, 3DES and RC4 ciphers, hmac-sha1-96), by its constant or by
// its name in a string.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// sshDowngradeOwners are the packages allowed to name a weak SSH algorithm:
// pkg/remoteexec, which offers one only behind a device record's explicit,
// per-device flag and warns about it on every connection, and its test
// server, which imitates an old device so that refusal is tested over a real
// handshake. TestOnlyRemoteexecWeakensSSH also holds that no shipping file
// imports the test server.
var sshDowngradeOwners = []string{"pkg/remoteexec", "pkg/remoteexec/remoteexectest"}

// weakSSHNames returns every algorithm name the library calls insecure, and
// the SHA-1 RSA and DSA host key names it lists among them.
func weakSSHNames() []string {
	insecure := ssh.InsecureAlgorithms()
	return slices.Concat(insecure.KeyExchanges, insecure.Ciphers, insecure.MACs, insecure.HostKeys)
}

// weakSSHUses returns every place file names a weak SSH algorithm: a selector
// on golang.org/x/crypto/ssh naming InsecureAlgorithms, an Insecure*
// algorithm constant (InsecureIgnoreHostKey is host key verification, held
// by its own rules, not an algorithm), KeyAlgoRSA, KeyAlgoDSA or a SHA-1 RSA
// or DSA certificate algorithm; or a string literal that is a weak
// algorithm's name.
func weakSSHUses(file *ast.File) []token.Pos {
	sshName := ""
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "golang.org/x/crypto/ssh" {
			sshName = "ssh"
			if imp.Name != nil {
				sshName = imp.Name.Name
			}
		}
	}
	weak := weakSSHNames()
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			pkg, ok := x.X.(*ast.Ident)
			if !ok || sshName == "" || pkg.Name != sshName {
				return true
			}
			name := x.Sel.Name
			switch {
			case name == "InsecureIgnoreHostKey":
			case name == "InsecureAlgorithms", strings.HasPrefix(name, "Insecure"),
				name == "KeyAlgoRSA", name == "KeyAlgoDSA", name == "CertAlgoRSAv01", name == "CertAlgoDSAv01":
				found = append(found, x.Pos())
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(x.Value); err == nil && slices.Contains(weak, v) {
				found = append(found, x.Pos())
			}
		}
		return true
	})
	return found
}

// TestOnlyRemoteexecWeakensSSH asserts no shipping code outside
// pkg/remoteexec names a weak SSH algorithm. A device may be reached that way
// only through its own record's flags, which pkg/remoteexec reads and warns
// about; anywhere else, a weak algorithm would be a downgrade no flag asked
// for, as the library's own default once was.
func TestOnlyRemoteexecWeakensSSH(t *testing.T) {
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
		// The test server may name weak algorithms only while nothing that
		// ships links it.
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) == modulePath+"/"+sshDowngradeOwners[1] {
				t.Errorf("%s imports %s, the test server that imitates an old SSH device: only tests may", rel, sshDowngradeOwners[1])
			}
		}
		for _, pos := range weakSSHUses(file) {
			if slices.Contains(sshDowngradeOwners, filepath.ToSlash(filepath.Dir(rel))) {
				owned++
				continue
			}
			t.Errorf("%s:%d names a weak SSH algorithm. Only %s may, behind a device record's explicit flags, so a weakening is always asked for and always warned about",
				rel, fset.Position(pos).Line, sshDowngradeOwners[0])
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
		t.Fatalf("%s names no weak SSH algorithm, so this matcher matches nothing; if the package no longer allows one, delete this rule", sshDowngradeOwners[0])
	}
}

// TestWeakSSHUsesDetects is the negative control: each weak form is found,
// under an aliased import too, and a strong one and host key verification's
// own opt-out are not.
func TestWeakSSHUsesDetects(t *testing.T) {
	const src = `package probe

import xssh "golang.org/x/crypto/ssh"

var (
	a = xssh.InsecureAlgorithms()
	b = xssh.InsecureKeyExchangeDH14SHA1
	c = xssh.KeyAlgoRSA
	d = xssh.InsecureCipherAES128CBC
	e = []string{"diffie-hellman-group1-sha1", "ssh-dss", "hmac-sha1-96"}
	strong = xssh.KeyAlgoRSASHA512
	kex    = "curve25519-sha256"
	skip   = xssh.InsecureIgnoreHostKey()
)
`
	file, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(weakSSHUses(file)); got != 7 {
		t.Fatalf("found %d weak uses, want 7", got)
	}
}

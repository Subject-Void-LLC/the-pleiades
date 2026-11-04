// Package archtest: only pkg/remoteexec opens an SSH session, so every command
// a device receives goes through the connection that hands it to sh.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// sessionOpens returns where file opens an SSH session: a zero-argument
// NewSession() call, or an OpenChannel("session", ...) that would carry an
// exec request of its own.
func sessionOpens(file *ast.File) []token.Pos {
	var at []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch {
		case sel.Sel.Name == "NewSession" && len(call.Args) == 0:
			at = append(at, call.Pos())
		case sel.Sel.Name == "OpenChannel" && len(call.Args) > 0:
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && v == "session" {
					at = append(at, call.Pos())
				}
			}
		}
		return true
	})
	return at
}

// TestOnlyRemoteexecOpensAnSSHSession keeps every command a device's exec
// channel receives going through pkg/remoteexec's Conn, which hands it to sh
// whatever the login shell is (pkg/remoteexec/shline) unless the device is not
// a POSIX shell. A session opened anywhere else would send its command to the
// login shell as written, and a csh or fish login would read it differently,
// which is how a quarter of the release gates failed under fish before
// (FAILURE_PATTERNS 845). Tests may open their own, being the independent
// path a gate reads a device through.
func TestOnlyRemoteexecOpensAnSSHSession(t *testing.T) {
	root := repoRoot(t)
	allowed := filepath.Join("pkg", "remoteexec") + string(filepath.Separator)
	for _, dir := range []string{"cmd", "internal", "pkg", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, _ := filepath.Rel(root, path)
			if strings.HasSuffix(path, "_test.go") || strings.HasPrefix(rel, allowed) {
				return
			}
			for _, pos := range sessionOpens(file) {
				t.Errorf("%s: opens an SSH session outside pkg/remoteexec; send the command through a remoteexec.Conn, which hands it to sh", fset.Position(pos))
			}
		})
	}
}

// TestSessionOpensFindsBothWays is the check's negative control: each way of
// opening a session is found, and a channel of another kind is not.
func TestSessionOpensFindsBothWays(t *testing.T) {
	src := `package x
func f(c client) {
	c.NewSession()
	c.OpenChannel("session", nil)
	c.OpenChannel("direct-tcpip", nil)
	c.NewSession(1)
}`
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sessionOpens(file)); got != 2 {
		t.Fatalf("found %d session opens, want 2 (NewSession() and OpenChannel(\"session\"))", got)
	}
}

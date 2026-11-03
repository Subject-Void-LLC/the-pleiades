// Package archtest: only pkg/remoteexec opens an SSH session, so every command
// a device receives goes through the connection that hands it to sh.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

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
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewSession" && len(call.Args) == 0 {
					t.Errorf("%s: opens an SSH session outside pkg/remoteexec; send the command through a remoteexec.Conn, which hands it to sh", fset.Position(call.Pos()))
				}
				return true
			})
		})
	}
}

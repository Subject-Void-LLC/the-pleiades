// Package archtest: that no Go source reads a listener's address after
// closing it, the construction FAILURE_PATTERNS 123, 177, 183 and 478 each
// fixed where it was found and each saw written again.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listenFuncs are the net functions that bind a port.
var listenFuncs = map[string]bool{"Listen": true, "ListenTCP": true, "ListenUDP": true, "ListenPacket": true}

// allowedReleasedPorts are places that read a closed listener's address on
// purpose, each with why that is not the hazard. Keyed by "path:function",
// the path slash-separated. An entry nothing matches any more fails the
// test, so an exemption cannot outlive the code it was written for.
var allowedReleasedPorts = map[string]string{
	"cmd/controller/leader_election_release_gate_test.go:freeTCPPort": "the port is handed to a controller subprocess " +
		"as its LISTEN_ADDR, which must bind it itself: a port held here would refuse that bind. If another process " +
		"takes the port first, the controller fails to start and the test fails loudly at startup; it never runs " +
		"against the wrong server.",
	"tests/e2e/harness_test.go:freeTCPPort": "the port is handed to a subprocess of the real binaries, which must bind " +
		"it itself: a port held here would refuse that bind. If another process takes it first, the subprocess " +
		"fails to start and the test fails loudly at startup; it never runs against the wrong server.",
}

// TestNoReleasedPortIsUsedAsAnAddress refuses, in every Go file of the
// module, a listener whose address is used after the listener is closed:
// either read and used after an explicit Close, or returned from a function
// that closes it. A released port belongs to whichever process binds it
// next, so an address built that way may answer when a test needs nothing
// there, or be taken when a test needs it free (FAILURE_PATTERNS 123). Use
// an address held for the test instead: refusing.Addr (or refusing.Port,
// internal/testsupport/refusing), whose port is bound and never listened on, so it refuses
// at once and nothing else can take it; port 0 ("127.0.0.1:0") where any
// refusal will do; or keep the listener open while its address is used.
func TestNoReleasedPortIsUsedAsAnAddress(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	var found []string
	matched := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 -- a file of this repository, found by walking it
		if err != nil {
			return err
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for _, f := range releasedPorts(filepath.ToSlash(rel), src) {
			if _, ok := allowedReleasedPorts[f.key]; ok {
				matched[f.key] = true
				continue
			}
			found = append(found, f.message)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %s, so the walk is misaimed", scanned, root)
	}
	for _, f := range found {
		t.Error(f)
	}
	for key := range allowedReleasedPorts {
		if !matched[key] {
			t.Errorf("allowedReleasedPorts[%q] matches no finding any more; delete the entry", key)
		}
	}
}

// releasedPort is one finding: its allowlist key and what to print.
type releasedPort struct{ key, message string }

// releasedPorts finds, in one file's source, every listener whose address
// outlives it. A file that does not parse yields nothing here; the build
// reports it.
func releasedPorts(rel string, src []byte) []releasedPort {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out []releasedPort
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		name := "func literal"
		switch fn := n.(type) {
		case *ast.FuncDecl:
			body, name = fn.Body, fn.Name.Name
		case *ast.FuncLit:
			body = fn.Body
		}
		if body != nil {
			for _, msg := range scanScope(fset, body) {
				out = append(out, releasedPort{key: rel + ":" + name, message: fmt.Sprintf("%s (in %s): %s", rel, name, msg)})
			}
		}
		return true
	})
	return out
}

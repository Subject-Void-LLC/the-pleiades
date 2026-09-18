// Structural proof that the setup command takes its key material from the key
// resolver and cannot hand a secret to another process or to the environment.
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

// The setup command writes the master encryption key, the JWT secret and a
// database password to disk. These tests are its Adversarial Pattern
// Justification made executable: they prove the generation path is consumed
// from the key resolver rather than reimplemented beside it, and that no
// secret it holds can reach a process argument or an environment variable.
//
// The third leak path, a log line or an error message, is carried by
// behavior rather than structure: internal/setup's FuzzParseEnvFile proves
// no refusal depends on a secret's value, its tests assert a run without a
// terminal never prints the key, and cmd/controller's setup tests run the
// built binary and search everything it printed.

// setupSourceFiles returns the production Go files that make up the setup
// command: every non-test file in internal/setup, and every non-test
// cmd/controller file whose name starts with "setup".
func setupSourceFiles(t *testing.T) []string {
	t.Helper()
	root := moduleRoot(t)
	var files []string
	for _, pattern := range []string{
		filepath.Join(root, "internal", "setup", "*.go"),
		filepath.Join(root, "cmd", "controller", "setup*.go"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("globbing %s: %v", pattern, err)
		}
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				files = append(files, m)
			}
		}
	}
	return files
}

// TestSetupConsumesTheResolversGenerator fails if the setup command makes
// key material any way but through internal/crypto.GenerateKey, or can hand
// a secret to another process or to its own environment.
//
// crypto/rand and math/rand are forbidden outright rather than inspected
// call by call: a package with no random source cannot generate a key
// beside the resolver's, whatever its code says. os/exec is forbidden
// because a child process's argument list is visible to every user on the
// machine for as long as it runs. Setting an environment variable is
// forbidden because the environment is inherited by every child and is
// readable from /proc.
func TestSetupConsumesTheResolversGenerator(t *testing.T) {
	files := setupSourceFiles(t)

	// A control: the glob has to find the package, or every check below
	// passes vacuously.
	if len(files) < 5 {
		t.Fatalf("found %d setup source files, want the whole of internal/setup; the glob is misaimed", len(files))
	}

	forbiddenImports := map[string]string{
		"crypto/rand":  "generates random bytes outside internal/crypto.GenerateKey",
		"math/rand":    "is not a source of key material at all",
		"math/rand/v2": "is not a source of key material at all",
		"os/exec":      "would put arguments where every user on the machine can read them",
	}
	forbiddenCalls := map[string]string{
		"os.Setenv":      "would put a value in the environment every child process inherits",
		"os.Unsetenv":    "changes the environment the controller's own loader reads",
		"syscall.Setenv": "would put a value in the environment every child process inherits",
	}

	fset := token.NewFileSet()
	generatorCalls := 0
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if reason, bad := forbiddenImports[name]; bad {
				t.Errorf("%s imports %s, which %s", rel(t, path), name, reason)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			qualified := pkg.Name + "." + sel.Sel.Name
			if reason, bad := forbiddenCalls[qualified]; bad {
				t.Errorf("%s calls %s, which %s", rel(t, path), qualified, reason)
			}
			if qualified == "crypto.GenerateKey" {
				generatorCalls++
			}
			return true
		})
	}
	if generatorCalls == 0 {
		t.Fatal("no setup source file calls crypto.GenerateKey, so the secrets it writes come from somewhere else")
	}
}

// TestTheResolverHasOneGenerator fails if internal/crypto's key resolver
// reads random bytes anywhere but GenerateKey, which is the half of the
// consumption proof that lives on the resolver's side: the setup command
// and ResolveKey both reach key material through that one function.
func TestTheResolverHasOneGenerator(t *testing.T) {
	path := filepath.Join(moduleRoot(t), "internal", "crypto", "key_resolve.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "rand" && sel.Sel.Name == "Read" {
				if fn.Name.Name != "GenerateKey" {
					t.Errorf("%s reads random bytes in %s; only GenerateKey may, so that every key has one source", rel(t, path), fn.Name.Name)
				}
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("key_resolve.go reads no random bytes at all; this test is looking in the wrong place")
	}
}

// rel shortens path to be relative to the module root for a message.
func rel(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.Rel(moduleRoot(t), path)
	if err != nil {
		return path
	}
	return r
}

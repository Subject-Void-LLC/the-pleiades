// Tests that every container this repository starts is terminated when its
// start fails, run without Docker by reading the source.
//
// testcontainers-go hands back the container alongside the error when a
// start fails partway, most often because a wait strategy gave up or the
// context ended. Until the caller terminates it, that container keeps
// running and its connection to the reaper keeps a goroutine alive. A test
// that stopped on the error first leaked both, and one such goroutine failed
// every later test's leak check in tests/e2e; in the Ansible orchestrator
// it left ansible-playbook running after its job's timeout
// (FAILURE_PATTERNS 423). The library's own documentation asks for the
// cleanup before the error check, so this holds every start to it.
package testsupport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// testcontainersRoot is the import path whose GenericContainer starts a
// container; modulesPrefix is where each module's Run lives.
const (
	testcontainersRoot = "github.com/testcontainers/testcontainers-go"
	modulesPrefix      = testcontainersRoot + "/modules/"
)

// unterminatedStarts returns a description of each container start in file
// whose failure path does not terminate the container: neither a
// statement between the start and its error check, nor the error check's
// own branch, terminates or schedules the termination of the variable the
// container was assigned to. A start whose error is not checked by an
// `if err != nil` before anything else uses the container is reported too,
// since nothing then stands between a failed start and the next use.
func unterminatedStarts(fset *token.FileSet, file *ast.File) (starts int, problems []string) {
	root, modules := "", map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch {
		case path == testcontainersRoot:
			if name == "" {
				name = "testcontainers"
			}
			root = name
		case strings.HasPrefix(path, modulesPrefix):
			if name == "" {
				name = path[strings.LastIndex(path, "/")+1:]
			}
			modules[name] = true
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i, stmt := range block.List {
			name, ok := containerStart(stmt, root, modules)
			if !ok {
				continue
			}
			starts++
			if !terminatedOnFailure(block.List[i+1:], name) {
				problems = append(problems, fset.Position(stmt.Pos()).String()+": "+name+
					" is not terminated when its start fails; call testcontainers.TerminateContainer("+name+
					") in the error branch, or testcontainers.CleanupContainer before the error check")
			}
		}
		return true
	})
	return starts, problems
}

// containerStart reports whether stmt is `name, err := <start>(...)` (or
// `=`), where the start is root.GenericContainer or a module's Run, and
// returns the container's variable name.
func containerStart(stmt ast.Stmt, root string, modules map[string]bool) (string, bool) {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return "", false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	isStart := (root != "" && pkg.Name == root && sel.Sel.Name == "GenericContainer") ||
		(modules[pkg.Name] && sel.Sel.Name == "Run")
	if !isStart {
		return "", false
	}
	name, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || name.Name == "_" {
		return "", false
	}
	return name.Name, true
}

// terminatedOnFailure reports whether, in the statements after a start,
// the container is terminated (or its termination scheduled) before or
// inside the first `if err != nil` check.
func terminatedOnFailure(after []ast.Stmt, name string) bool {
	for _, stmt := range after {
		if check, ok := stmt.(*ast.IfStmt); ok && isErrCheck(check.Cond) {
			return terminates(check.Body, name)
		}
		if terminates(stmt, name) {
			return true
		}
	}
	return false
}

// isErrCheck reports whether cond is `err != nil`.
func isErrCheck(cond ast.Expr) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	x, xok := bin.X.(*ast.Ident)
	y, yok := bin.Y.(*ast.Ident)
	return xok && yok && x.Name == "err" && y.Name == "nil"
}

// terminates reports whether n contains a call that terminates name:
// TerminateContainer(name), CleanupContainer(tb, name) or name.Terminate().
func terminates(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "TerminateContainer", "CleanupContainer":
			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == name {
					found = true
				}
			}
		case "Terminate":
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestEveryContainerIsTerminatedWhenItsStartFails holds every container
// start in this repository, tests and production alike, to terminating the
// container on its failure path.
func TestEveryContainerIsTerminatedWhenItsStartFails(t *testing.T) {
	root := RepoRoot(t)
	fset := token.NewFileSet()
	total := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".claude", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 -- walking this repository's own source.
		if err != nil {
			return err
		}
		if !strings.Contains(string(src), testcontainersRoot) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		starts, problems := unterminatedStarts(fset, file)
		total += starts
		for _, p := range problems {
			t.Error(p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	// There are 49 today. A count near zero means the detector stopped
	// recognizing a start, and would then pass by checking nothing.
	if total < 40 {
		t.Fatalf("found only %d container starts, so this guard is no longer recognizing them", total)
	}
}

// TestUnterminatedStartsDetects is the guard's positive control: each shape
// a start takes in this repository, terminated and not.
func TestUnterminatedStartsDetects(t *testing.T) {
	const header = `package p
import (
	"context"
	"testing"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
)
`
	tests := []struct {
		name string
		body string
		want int
	}{
		{"generic, error stops the test first", `func f(t *testing.T) {
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
}`, 1},
		{"module Run under an alias, error returned first", `func f() error {
	pg, err := testpg.Run(context.Background(), "postgres")
	if err != nil {
		return err
	}
	defer pg.Terminate(context.Background())
	return nil
}`, 1},
		{"terminated in the error branch", `func f(t *testing.T) {
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatal(err)
	}
}`, 0},
		{"cleanup scheduled before the error check", `func f(t *testing.T) {
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{})
	if c != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		t.Fatal(err)
	}
}`, 0},
		{"CleanupContainer first, the library's own idiom", `func f(t *testing.T) {
	pg, err := testpg.Run(context.Background(), "postgres")
	testcontainers.CleanupContainer(t, pg)
	if err != nil {
		t.Fatal(err)
	}
}`, 0},
		{"the method form in the error branch", `func f() error {
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		return err
	}
	return nil
}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "p.go", header+tt.body, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			starts, problems := unterminatedStarts(fset, file)
			if starts != 1 {
				t.Fatalf("found %d starts, want 1", starts)
			}
			if len(problems) != tt.want {
				t.Errorf("reported %d problem(s), want %d: %v", len(problems), tt.want, problems)
			}
		})
	}
}

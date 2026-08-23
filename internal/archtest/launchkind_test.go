// This file enforces PLAN.md Section 28's own requirement, and Phase 21's
// adversarial gate: no consumer of a launch Kind may type-switch on it.
//
// The requirement is not stylistic. A closed set of kinds forces a case per
// kind at every consumer, and the consumers named in the plan are schedules
// (Phase 23), workflow nodes (Phase 24), notification policies (Phase 28),
// approvals (Phase 26), the runner's adapter selection and the launch API.
// With a switch in each, adding a kind means editing six packages and
// finding the one that was missed by running it. With a registry, a kind is
// one file plus a line in builtins.go.
//
// The rule is enforced two ways here, because either alone has a gap. The
// source scan catches a comparison written today; the behavioural test
// catches a consumer that dispatches on kind by some other mechanism, by
// driving it with a kind this repository has never heard of.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// builtInKindNames are the registered kinds' own keys. A comparison against
// one of these literals, outside the packages that legitimately own them,
// is the shape this file forbids.
var builtInKindNames = []string{"runbook", "playbook"}

// kindOwningDirs may name a kind literally: a kind's own package declares
// its key, and the composition roots map an adapter name onto a concrete
// adapter, which is the one place a name has to become a thing.
var kindOwningDirs = []string{
	filepath.Join("internal", "launch", "kinds"),
	filepath.Join("cmd", "runner"),
	filepath.Join("cmd", "controller"),
}

// TestNoConsumerSwitchesOnLaunchKind scans for a comparison or a switch
// whose subject is a launch kind and whose operand is a kind literal.
//
// It scans source rather than asking the type system, because what is being
// forbidden is not a type at all: Kind is a string, so a type switch on it
// is impossible and what a consumer would actually write is `if kind ==
// "playbook"`. That is invisible to every compiler check and to every
// linter, and it is exactly what the registry exists to make unnecessary.
//
// The subject has to be checked, not just the literal. "runbook" is an
// ordinary domain noun here that predates launch kinds by many phases: it
// is a view field name, a route segment, a column label and a scope. A scan
// that flagged every occurrence reported six of those on its first run and
// nothing that was actually a branch on kind, which would have made this
// test a thing people silence rather than a thing they fix.
func TestNoConsumerSwitchesOnLaunchKind(t *testing.T) {
	root := repoRoot(t)

	for _, dir := range []string{"internal", "cmd", "pkg"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if ownsKindNames(rel) || strings.HasSuffix(rel, "_test.go") {
				return
			}

			report := func(pos token.Pos, kind, shape string) {
				t.Errorf("%s:%d branches on the launch kind %q (%s). "+
					"A consumer that branches on kind grows a case per kind, which is the closed-enum cost "+
					"the open registry exists to avoid: read what you need off the kind's Descriptor instead",
					rel, fset.Position(pos).Line, kind, shape)
			}

			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.BinaryExpr:
					if node.Op != token.EQL && node.Op != token.NEQ {
						return true
					}
					// One side names a kind, the other is a kind literal.
					for _, pair := range [][2]ast.Expr{{node.X, node.Y}, {node.Y, node.X}} {
						if !mentionsKind(pair[0]) {
							continue
						}
						if kind, ok := kindLiteral(pair[1]); ok {
							report(node.Pos(), kind, "a comparison")
						}
					}

				case *ast.SwitchStmt:
					if node.Tag == nil || !mentionsKind(node.Tag) {
						return true
					}
					for _, stmt := range node.Body.List {
						clause, ok := stmt.(*ast.CaseClause)
						if !ok {
							continue
						}
						for _, expr := range clause.List {
							if kind, ok := kindLiteral(expr); ok {
								report(expr.Pos(), kind, "a switch case")
							}
						}
					}
				}
				return true
			})
		})
	}
}

// mentionsKind reports whether an expression names something called a
// kind. It is a name check rather than a type check because a consumer
// holding a kind holds a string, and every string looks alike to go/types.
func mentionsKind(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && strings.Contains(strings.ToLower(ident.Name), "kind") {
			found = true
		}
		return !found
	})
	return found
}

// kindLiteral reports whether an expression is a string literal naming a
// registered kind, allowing for a conversion such as launch.Kind("runbook").
func kindLiteral(expr ast.Expr) (string, bool) {
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
		expr = call.Args[0]
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	for _, kind := range builtInKindNames {
		if value == kind {
			return kind, true
		}
	}
	return "", false
}

// ownsKindNames reports whether a file is allowed to name a kind literally.
func ownsKindNames(rel string) bool {
	for _, dir := range kindOwningDirs {
		if strings.HasPrefix(rel, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// walkGoFiles parses every non-generated Go file under dir.
func walkGoFiles(t *testing.T, dir string, visit func(path string, file *ast.File, fset *token.FileSet)) {
	t.Helper()

	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is a sibling test's
			// scaffolding being cleaned up, not an architecture failure:
			// FAILURE_PATTERNS.md #88 is this exact race, and aborting the
			// whole scan for it fails an architecture test for a reason
			// unrelated to architecture.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			// Generated ent code is a hundred thousand lines nothing here
			// has an opinion about.
			if entry.Name() == "ent" && strings.Contains(path, filepath.Join("internal", "ent")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		visit(path, parsed, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
}

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolving the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// TestCompositionRootsImportTheLaunchKinds is the reachability guard.
//
// internal/launch's registry is populated only by the init() functions of
// the kind packages, which run only if something imports them. A
// composition root that forgot the blank import would compile, pass every
// unit test, start cleanly, and then route nothing: the Runner's Router
// would have an empty table and refuse every dispatch as an unknown kind,
// and the Controller would refuse every template as one.
//
// That is FAILURE_PATTERNS.md #52 exactly, and it has happened three times
// in this repository. The dependency graph is the only place it is visible
// before it happens.
func TestCompositionRootsImportTheLaunchKinds(t *testing.T) {
	const kindsPath = modulePath + "/internal/launch/kinds"

	// The two binaries that resolve a kind at run time. cmd/pleiades is
	// deliberately absent: the Crawl-tier CLI has no templates and no
	// dispatch, so importing the registry there would be weight with no
	// consumer.
	needs := map[string]bool{
		modulePath + "/cmd/runner":     false,
		modulePath + "/cmd/controller": false,
	}

	for _, pkg := range goList(t, true, modulePath+"/...") {
		if _, wanted := needs[pkg.ImportPath]; !wanted {
			continue
		}
		for _, dep := range pkg.Deps {
			if dep == kindsPath {
				needs[pkg.ImportPath] = true
			}
		}
	}

	for path, imports := range needs {
		if !imports {
			t.Errorf("%s does not depend on %s, so internal/launch's registry is empty in that binary: "+
				"every dispatch would be an unknown kind and every template would be refused, "+
				"with nothing failing at build time to say why", path, kindsPath)
		}
	}
}

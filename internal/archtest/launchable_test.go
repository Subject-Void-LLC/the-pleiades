// This file enforces the same rule launchkind_test.go does, one axis over:
// no consumer of a LAUNCHABLE may branch on what sort of launchable it is.
//
// The two axes are different questions and both have to stay switch-free. A
// launch kind says which engine runs a definition; a launchable type says what
// sort of object is being run, and its consumers are a schedule today and a
// workflow node and a notification policy later. A switch in each would mean
// that adding a type (an inventory source, a workflow) is an edit to every
// consumer, with the one that was missed found by running it at three in the
// morning. That is precisely the cost internal/launchable's registry exists to
// avoid, and Phase 21's adversarial gate asks for proof rather than intent.
//
// Three assertions here, because the first alone has two gaps: a consumer can
// branch by a mechanism no source scan sees, and a registry nothing imports is
// empty in a way no scan notices either.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// builtInLaunchableTypes are the registered launchable types' own keys.
//
// "project" is an ordinary domain noun in this repository, so this test is
// careful in the same way its sibling is about "runbook": the SUBJECT of a
// comparison has to name a launchable before the literal matters, and the
// words that qualify are narrow on purpose.
var builtInLaunchableTypes = []string{"job_template", "project"}

// launchableOwningDirs may name a launchable type literally: each type's own
// package declares its key, internal/launchable holds the key constants
// themselves, and cmd/controller maps a key onto the concrete launcher for it,
// which is the one place a key has to become a thing.
var launchableOwningDirs = []string{
	filepath.Join("internal", "launchable"),
	filepath.Join("cmd", "controller"),
}

// launchableSubjectWords are the identifier fragments that make an expression
// a launchable rather than something else called a type.
//
// "type" alone is far too common a word to key on: it names a credential type,
// a question type, a device type, an SCM type and a dozen others. So the
// subject has to mention a launchable, a target or a unified job, which is the
// vocabulary internal/launchable actually uses.
var launchableSubjectWords = []string{"launchable", "target", "unifiedjob"}

// TestNoConsumerSwitchesOnLaunchableType scans for a comparison or a switch
// whose subject is a launchable and whose operand is a type literal.
func TestNoConsumerSwitchesOnLaunchableType(t *testing.T) {
	root := repoRoot(t)

	for _, dir := range []string{"internal", "cmd", "pkg"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			if ownsLaunchableTypeNames(rel) || strings.HasSuffix(rel, "_test.go") {
				return
			}

			report := func(pos token.Pos, targetType, shape string) {
				t.Errorf("%s:%d branches on the launchable type %q (%s). "+
					"A consumer that branches on what sort of launchable it holds grows a case per type, "+
					"which is what the registry exists to avoid: read what you need off the type's "+
					"launchable.Descriptor, or route through launchable.Router",
					rel, fset.Position(pos).Line, targetType, shape)
			}

			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.BinaryExpr:
					if node.Op != token.EQL && node.Op != token.NEQ {
						return true
					}
					for _, pair := range [][2]ast.Expr{{node.X, node.Y}, {node.Y, node.X}} {
						if !mentionsLaunchable(pair[0]) {
							continue
						}
						if targetType, ok := launchableTypeLiteral(pair[1]); ok {
							report(node.Pos(), targetType, "a comparison")
						}
					}

				case *ast.SwitchStmt:
					if node.Tag == nil || !mentionsLaunchable(node.Tag) {
						return true
					}
					for _, stmt := range node.Body.List {
						clause, ok := stmt.(*ast.CaseClause)
						if !ok {
							continue
						}
						for _, expr := range clause.List {
							if targetType, ok := launchableTypeLiteral(expr); ok {
								report(expr.Pos(), targetType, "a switch case")
							}
						}
					}
				}
				return true
			})
		})
	}
}

// TestCompositionRootImportsTheLaunchableTypes is the reachability half.
//
// An init() only runs if something imports the package it lives in, so a type
// nobody imports is registered nowhere and invisible to every picker and
// validator. Here it is worse than invisible in one direction and fatal in the
// other: launchable.NewRouter refuses to build when a registered type has no
// launcher, so the Controller either cannot schedule anything (nothing
// imported) or will not start (a launcher composed for a type that is not
// registered). This is FAILURE_PATTERNS.md #52's shape, which this repository
// has shipped three times.
func TestCompositionRootImportsTheLaunchableTypes(t *testing.T) {
	const typesPkg = modulePath + "/internal/launchable/types"

	for _, pkg := range goList(t, false, modulePath+"/cmd/controller") {
		found := false
		for _, dep := range pkg.Deps {
			if dep == typesPkg {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s does not depend on %s, so no launchable type is registered in it: "+
				"the registry would be empty, and a schedule could point at nothing",
				pkg.ImportPath, typesPkg)
		}
	}
}

// TestSchedulePackageDoesNotReachTheLaunchers keeps the seam a seam.
//
// internal/schedule declares narrow interfaces (Launcher, Admitter) rather than
// importing what satisfies them, and that is what makes it a consumer of the
// abstraction rather than of the Dispatcher and the project runner. If it ever
// imports either, the abstraction has stopped paying for itself: a schedule
// would know what sort of thing it launches again, and the packages it now
// depends on would be free to depend back.
//
// internal/launch is on the list too, for the same reason and one more: a
// schedule names a launchable, never a template, and reaching the template
// domain from here would be the old coupling growing back under a new name.
func TestSchedulePackageDoesNotReachTheLaunchers(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/api",
		modulePath + "/internal/project",
		modulePath + "/internal/launch",
	}

	for _, pkg := range goList(t, false, modulePath+"/internal/schedule/...") {
		for _, dep := range pkg.Deps {
			for _, bad := range forbidden {
				if dep != bad {
					continue
				}
				t.Errorf("%s depends on %s. internal/schedule must reach a launcher only through "+
					"the narrow interfaces it declares, so that adding a launchable type is not an "+
					"edit here and the launchers stay free to depend on this package",
					pkg.ImportPath, dep)
			}
		}
	}
}

// mentionsLaunchable reports whether an expression names something that holds
// a launchable's type. A name check rather than a type check, because a
// launchable type is a string and every string looks alike to go/types.
func mentionsLaunchable(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return !found
		}
		lowered := strings.ToLower(ident.Name)
		for _, word := range launchableSubjectWords {
			if strings.Contains(lowered, word) {
				found = true
			}
		}
		return !found
	})
	return found
}

// launchableTypeLiteral reports whether an expression is a string literal
// naming a registered launchable type, allowing for a conversion.
func launchableTypeLiteral(expr ast.Expr) (string, bool) {
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
	for _, targetType := range builtInLaunchableTypes {
		if value == targetType {
			return targetType, true
		}
	}
	return "", false
}

// ownsLaunchableTypeNames reports whether a file may name a launchable type
// literally.
func ownsLaunchableTypeNames(rel string) bool {
	for _, dir := range launchableOwningDirs {
		if strings.HasPrefix(rel, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

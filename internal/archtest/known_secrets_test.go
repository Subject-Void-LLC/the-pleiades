// This file holds sdk.KnownSecrets to the one method it exists for.
//
// The interface hands a method every value of every named secret in the
// project's credential file, where sdk.NamedSecrets hands out one field of
// one secret a task's own reference names. A built-in method can already ask
// for any secret by name, so this widens nothing a reviewer could not see;
// what it adds is the list, and a list of every secret is the thing a
// careless caller would log. So it is reached from the packages named here
// and no other: the one that declares it, the command line's context that
// implements it, and net.yang.schema, which masks the sources it writes into
// the project by it, where no output boundary of the run reaches.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// knownSecretsNames are the identifiers that reach the run's known secrets.
var knownSecretsNames = map[string]bool{"KnownSecrets": true, "KnownSecretValues": true}

// knownSecretsAllowed are the packages, by their path in the module, that may
// name them, each with why.
var knownSecretsAllowed = map[string]string{
	"pkg/sdk":                   "declares the interface and its accessor",
	"internal/engine":           "the command line's context implements it (WithKnownSecretValues)",
	"internal/catalog/net/yang": "net.yang.schema masks the sources it writes into the project by the run's known secrets",
}

// TestOnlyDesignatedConsumersReachTheKnownSecrets fails when a package not in
// knownSecretsAllowed names sdk.KnownSecrets or KnownSecretValues anywhere
// outside its tests: a call, a type assertion, an embedding, or a method of
// that name, which is how a context would come to implement it. It also
// fails when an allowed package no longer names it, so the list cannot hold a
// standing permission nothing uses.
func TestOnlyDesignatedConsumersReachTheKnownSecrets(t *testing.T) {
	root := repoRoot(t)
	found := map[string][]string{}
	for _, dir := range []string{"cmd", "internal", "pkg", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			pkg := filepath.ToSlash(rel)
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && knownSecretsNames[id.Name] {
					found[pkg] = append(found[pkg], fset.Position(id.Pos()).String())
				}
				return true
			})
		})
	}
	var offenders []string
	for pkg, at := range found {
		if _, ok := knownSecretsAllowed[pkg]; !ok {
			offenders = append(offenders, pkg+" ("+strings.Join(at, ", ")+")")
		}
	}
	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("%s names sdk.KnownSecrets, which hands out every named secret of the project; "+
			"a method needing one asks for it by name (sdk.ResolveSecretRefs), and one masking text the run's own boundary "+
			"masks needs nothing: name the package in knownSecretsAllowed only with why no other way does", o)
	}
	for pkg := range knownSecretsAllowed {
		if len(found[pkg]) == 0 {
			t.Errorf("knownSecretsAllowed names %s, which no longer names sdk.KnownSecrets: remove it, "+
				"or the scan is not reading the tree", pkg)
		}
	}
}

// TestRunnerAndProgramContextsHoldNoKnownSecrets: a Runner's per-task child
// and an external Collection program both hand a method pkg/external's
// RunbookContext, which must not implement sdk.KnownSecrets, so neither can
// list the project's secrets (a Runner holds none, and a program is a third
// party). The control is the command line's own context, which does.
func TestRunnerAndProgramContextsHoldNoKnownSecrets(t *testing.T) {
	var child sdk.RunbookContext = external.NewRunbookContext(nil)
	if _, ok := child.(sdk.KnownSecrets); ok {
		t.Error("pkg/external's RunbookContext implements sdk.KnownSecrets, so a Runner's child or an external program could list the project's named secrets")
	}
	if _, ok := engine.NewRunbookContext(nil).(sdk.KnownSecrets); !ok {
		t.Error("the command line's context does not implement sdk.KnownSecrets, so this assertion cannot tell an implementation from none")
	}
}

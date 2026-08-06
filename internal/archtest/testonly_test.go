// This file enforces one narrow rule Phase 12 needed and no existing
// archtest file covers: internal/auth/authtest, a real token issuer built
// so a test can obtain a token AuthMiddleware actually validates instead
// of fabricating an *auth.Identity directly into a context (see that
// package's own doc comment), must never reach a production build.
// Without this, nothing stops a future caller from importing it as a
// convenience and quietly reintroducing the exact bypass Phase 12 removed
// api.IdentityKeyForTest to close, just under a different name.
//
// A Go build tag cannot express this: authtest is deliberately not a
// _test.go file, because a _test.go file cannot be imported across
// package boundaries at all, which is the tests/e2e problem authtest
// exists to solve. The only enforcement left is a dependency-graph check,
// the same shape layering_test.go already uses for pkg/'s isolation from
// internal/.
package archtest

import "testing"

// authtestImportPath is the one package this file forbids any production
// package from depending on, directly or transitively.
const authtestImportPath = modulePath + "/internal/auth/authtest"

// TestAuthtestNeverImportedByProductionCode fails if any package's own
// build (never its _test.go files: go list's default Deps excludes them)
// depends on authtest.
func TestAuthtestNeverImportedByProductionCode(t *testing.T) {
	for _, pkg := range goList(t, true, modulePath+"/...") {
		if pkg.ImportPath == authtestImportPath {
			continue
		}
		for _, dep := range pkg.Deps {
			if dep == authtestImportPath {
				t.Errorf("%s depends on %s outside of _test.go files; a token issuer must only ever be reachable from tests",
					pkg.ImportPath, authtestImportPath)
			}
		}
	}
}

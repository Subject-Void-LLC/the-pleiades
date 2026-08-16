// This file enforces the rule no Go build tag can express: a package that
// exists to serve tests must never end up in a production build.
//
// Two packages are covered, for two different reasons that happen to have
// the same fix.
//
// internal/auth/authtest is a real token issuer, built so a test can obtain
// a token AuthMiddleware actually validates instead of fabricating an
// *auth.Identity directly into a context (see that package's own doc
// comment). Without this check, nothing stops a future caller from
// importing it as a convenience and quietly reintroducing the exact bypass
// Phase 12 removed api.IdentityKeyForTest to close, just under a different
// name.
//
// internal/testsupport imports testing, so a production import links the
// testing package into the shipped binary: test flags registered on the
// default flag set, and a larger image, in exchange for nothing. That is
// not hypothetical. It is exactly what happened, and it is what Phase 20's
// self-provisioning work had to undo: the only implementation of "generate
// a serving certificate" lived in internal/testsupport, and the controller
// needed it. The fix was to move the logic to internal/tlscert, an ordinary
// production package, and to leave internal/testsupport calling it. This
// test is what stops the next person from doing the undone thing again,
// because nothing else would have caught it: the import compiles, the tests
// pass, and the cost is invisible until somebody looks at `go list -deps`
// on a shipped binary.
//
// What this file does NOT do, stated here because the claim was made and was
// wrong: it does not keep the testing package out of every shipped binary.
// `go list -deps ./cmd/runner | grep -x testing` finds it today, pulled in
// through internal/adapters/legacy and the testcontainers dependency below
// it, and no rule here forbids that. These two checks forbid two named
// import paths. That is a real property and it is worth having; it is just a
// smaller one than "no binary links testing", and writing the larger claim
// down is how a gap stops being looked for.
//
// A Go build tag cannot express either rule. authtest is deliberately not
// a _test.go file, because a _test.go file cannot be imported across
// package boundaries at all, which is the tests/e2e problem authtest exists
// to solve; internal/testsupport has the same constraint for the same
// reason. The only enforcement left is a dependency-graph check, the same
// shape layering_test.go already uses for pkg/'s isolation from internal/.
package archtest

import "testing"

// authtestImportPath and testsupportImportPath are the packages this file
// forbids any production package from depending on, directly or
// transitively.
const (
	authtestImportPath    = modulePath + "/internal/auth/authtest"
	testsupportImportPath = modulePath + "/internal/testsupport"
)

// assertNotImportedByProductionCode fails if any package's own build (never
// its _test.go files: go list's default Deps excludes them) depends on
// forbidden.
//
// reason is appended to the failure, because the two callers below forbid
// their package for genuinely different reasons and a bare "must only be
// reachable from tests" would send the next reader to the wrong place.
func assertNotImportedByProductionCode(t *testing.T, forbidden, reason string) {
	t.Helper()
	for _, pkg := range goList(t, true, modulePath+"/...") {
		if pkg.ImportPath == forbidden {
			continue
		}
		for _, dep := range pkg.Deps {
			if dep == forbidden {
				t.Errorf("%s depends on %s outside of _test.go files; %s", pkg.ImportPath, forbidden, reason)
			}
		}
	}
}

// TestAuthtestNeverImportedByProductionCode keeps the test-double token
// issuer out of every shipped binary.
func TestAuthtestNeverImportedByProductionCode(t *testing.T) {
	assertNotImportedByProductionCode(t, authtestImportPath,
		"a token issuer must only ever be reachable from tests")
}

// TestTestsupportNeverImportedByProductionCode keeps one package that
// imports testing, internal/testsupport, out of every production build.
//
// The narrower claim is deliberate, and it replaces a wider one this comment
// used to make: that the rule "keeps the testing package out of every
// shipped binary". It does not, and cannot. `go list -deps ./cmd/runner`
// reports the testing package today, reached through
// internal/adapters/legacy and the testcontainers dependency underneath it.
// A reader who took the wider claim at face value would conclude the
// property held everywhere and stop checking, which is worse than having no
// comment: this test's guarantee is about ONE import path, not about the
// testing package in general. Closing the cmd/runner case is a separate
// piece of work with a different fix, and pretending it is already done was
// the only thing standing in the way of somebody noticing it.
//
// The fix when this fails is almost never to add an exception. It is to
// move the logic that was wanted into an ordinary package and let
// internal/testsupport call it, which is what internal/tlscert is: the
// serving certificate generator, extracted for exactly this reason, with
// internal/testsupport keeping only its testing.TB convenience wrapper.
func TestTestsupportNeverImportedByProductionCode(t *testing.T) {
	assertNotImportedByProductionCode(t, testsupportImportPath,
		"internal/testsupport imports testing, so a production import links the testing package into a shipped binary; move the logic into an ordinary package (internal/tlscert is the precedent) and have internal/testsupport call it instead")
}

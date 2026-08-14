package archtest

import (
	"sort"
	"strings"
	"testing"
)

// The structural enforcement of PLAN.md Section 29.3's "secret fields read
// back as a redaction marker" and Section 17.6's "there is no plaintext
// read API".
//
// internal/credstore returns a projection with no field a plaintext secret
// could occupy, and internal/credstore/resolve returns the real values.
// Splitting them into two packages is what turns the requirement from a
// convention into a property, and this file is the half that makes the
// split load bearing: without it, the two packages are merely tidy, and any
// handler can import whichever it likes.

// resolverPackage is the one package that can produce a plaintext
// credential value.
const resolverPackage = modulePath + "/internal/credstore/resolve"

// resolverConsumerAllowlist is every package permitted to import it.
//
// internal/dispatch is the real consumer: it resolves credentials at
// fan-out, immediately before injection, which is the just-in-time point
// PLAN.md Section 17.4 requires. A job waiting behind a capacity limit
// therefore holds no secret, and a relaunch a week later picks up a
// credential that has since been rotated.
//
// cmd/ composition roots are exempt below rather than listed here, because
// constructing a concrete implementation and handing it to whoever needs it
// is what a composition root is for.
//
// Adding an entry here is a real decision. The question to answer first is
// not "does this package need credential values" but "can it do its job
// with the redacted projection instead", which for everything that renders
// a page or answers a request is yes.
var resolverConsumerAllowlist = map[string]bool{
	modulePath + "/internal/dispatch": true,
}

// TestAPINeverImportsTheCredentialResolver is the assertion the whole
// package split exists to make possible.
//
// If internal/api could import the resolver, "no plaintext read API" would
// be back to a promise somebody has to keep remembering, and the failure
// mode of forgetting is a secret in an HTTP response, which is a secret in
// a log, a proxy and a browser history.
func TestAPINeverImportsTheCredentialResolver(t *testing.T) {
	// Checked over Deps rather than Imports, so a handler cannot reach the
	// resolver through an intermediary package either.
	for _, pkg := range goList(t, true, modulePath+"/internal/api/...") {
		for _, dep := range pkg.Deps {
			if dep == resolverPackage {
				t.Errorf(
					"%s depends on %s, which returns plaintext credential values.\n"+
						"The API layer holds credstore.Store, whose read projection replaces every secret input with a "+
						"redaction marker and has nowhere to put a real one. That is what makes PLAN.md Section 17.6's "+
						"no-plaintext-read-API rule a property of the build rather than something a reviewer has to "+
						"notice. If a handler genuinely needs a value here, it does not: it needs the marker.",
					pkg.ImportPath, dep)
			}
		}
	}
}

// TestOnlyDesignatedConsumersImportTheCredentialResolver widens the same
// check to the whole module.
//
// The API layer is the sharpest case and not the only one. A UI view, a
// docs generator or a CLI command reaching the resolver would each be a
// plaintext secret somewhere it has no business being, and each would look
// entirely reasonable in isolation.
func TestOnlyDesignatedConsumersImportTheCredentialResolver(t *testing.T) {
	offenders := make([]string, 0)

	for _, pkg := range goList(t, false, modulePath+"/...") {
		switch {
		case pkg.ImportPath == resolverPackage:
			continue
		case resolverConsumerAllowlist[pkg.ImportPath]:
			continue
		case strings.HasPrefix(pkg.ImportPath, modulePath+"/cmd/"):
			// A composition root wires concrete implementations into
			// interfaces. That is its whole purpose, and the same reason
			// the concrete-driver allowlist exempts cmd/ too.
			continue
		}

		for _, imp := range pkg.Imports {
			if imp == resolverPackage {
				offenders = append(offenders, pkg.ImportPath)
			}
		}
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(
			"these packages import the credential resolver, which returns plaintext secret values: %v\n"+
				"Ask whether the redacted credstore.Store projection would do instead. For anything that renders a "+
				"page, answers a request, or writes a document, it will.",
			offenders)
	}
}

// TestResolverConsumerAllowlistHasNoStaleEntries keeps the allowlist a true
// record, mirroring the adapter and renderer allowlists.
//
// A stale entry here is worse than a stale one elsewhere: it is a standing,
// unexamined permission to read secrets, and the next package to occupy
// that import path inherits it silently.
func TestResolverConsumerAllowlistHasNoStaleEntries(t *testing.T) {
	seen := make(map[string]bool, len(resolverConsumerAllowlist))
	for _, pkg := range goList(t, false, modulePath+"/...") {
		for _, imp := range pkg.Imports {
			if imp == resolverPackage {
				seen[pkg.ImportPath] = true
			}
		}
	}

	stale := make([]string, 0)
	for pkg := range resolverConsumerAllowlist {
		if !seen[pkg] {
			stale = append(stale, pkg)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Logf(
			"resolverConsumerAllowlist entries that do not yet import the resolver: %v\n"+
				"This is a log rather than a failure because internal/dispatch is listed ahead of Stage 22b wiring it. "+
				"Once that lands, promote this to an error so the allowlist cannot rot.",
			stale)
	}
}

// TestCredentialTypesNeverImportPersistence guards the direction that keeps
// the two packages separable at all.
//
// internal/ent imports internal/credtype for its typed field.JSON columns.
// If credtype imported internal/ent or internal/credstore back, the two
// would form a cycle, and the usual way that gets resolved is by collapsing
// the domain types into the persistence package, which would put the
// plaintext type and the storage layer back in one place.
func TestCredentialTypesNeverImportPersistence(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/ent",
		modulePath + "/internal/credstore",
	}

	for _, pkg := range goList(t, true, modulePath+"/internal/credtype/...") {
		for _, dep := range pkg.Deps {
			if hasPrefix(dep, forbidden) {
				t.Errorf(
					"%s depends on %s. internal/ent imports internal/credtype for its typed JSON columns, so this "+
						"direction is an import cycle, and the usual fix for a cycle is to merge the two packages, "+
						"which would put the plaintext credential type back inside the storage layer.",
					pkg.ImportPath, dep)
			}
		}
	}
}

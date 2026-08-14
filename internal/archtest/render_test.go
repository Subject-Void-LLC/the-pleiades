package archtest

import (
	"sort"
	"strings"
	"testing"
)

// The structural half of PLAN.md Section 25's "Template renderer" rule:
// the contract has exactly one implementation, and a second one is a
// defect rather than a variation.
//
// internal/render's own shared_test.go holds the ledger, a table with one
// row per declared call site. A ledger is a promise. This file is the
// enforcement: it fails the build when any package in this module reaches
// for a second template engine, which is the only way a second
// implementation can actually appear.
//
// Phase 22's checklist calls for exactly this under "Adversarial Pattern
// Justification: prove the renderer is shared, not duplicated." An
// argument is not a proof.

// templateEnginePrefixes are the import paths this test treats as a
// template engine.
//
// The standard library's two are here because they are the ones somebody
// reaches for by reflex: text/template is a perfectly good renderer and
// the wrong one for this job, since its syntax is not Jinja2's and an AWX
// credential type's injectors are written in Jinja2. A second engine
// silently accepting the same string with different semantics is worse
// than one refusing it outright.
//
// The third-party entries are the real Jinja2-compatible Go libraries. If
// a later phase decides the strict subset in internal/render is not enough
// (Phase 28's notification templates are the likely trigger), the right
// move is to swap internal/render's one implementation behind its existing
// port and leave this list alone, not to add an exemption here.
var templateEnginePrefixes = []string{
	"text/template",
	"html/template",
	"github.com/flosch/pongo2",
	"github.com/nikolalohinski/gonja",
	"github.com/noirbizarre/gonja",
	"github.com/hashicorp/hil",
	"github.com/valyala/fasttemplate",
}

// templateEngineAllowlist is every package permitted to import a template
// engine directly.
//
// Every entry is a CODE GENERATOR. That is the whole justification and the
// test below enforces the shape of it: these packages render Go source
// files at authoring time, from templates committed beside them, and never
// touch a value that came from a user, a database row, or a network
// request. They are not rendering anything at run time, so they are not
// consumers of the Section 25 primitive at all.
//
// internal/render itself is deliberately NOT here. It implements the
// grammar by hand rather than delegating to text/template, because
// strict-undefined semantics (a missing variable is an error, never the
// empty string) cannot be expressed through text/template's own execution
// model without fighting it.
var templateEngineAllowlist = map[string]bool{
	modulePath + "/internal/forge/collectionscaffold": true,
	modulePath + "/internal/forge/pluginscaffold":     true,
	modulePath + "/internal/forge/viewscaffold":       true,
	modulePath + "/internal/inventory/devicescaffold": true,
}

// TestExactlyOneRendererImplementation asserts no package outside the
// code-generator allowlist imports a template engine.
func TestExactlyOneRendererImplementation(t *testing.T) {
	offenders := make([]string, 0)

	for _, pkg := range goList(t, false, modulePath+"/...") {
		if templateEngineAllowlist[pkg.ImportPath] {
			continue
		}
		// tools/ is build tooling rather than shipped code, and several
		// of its members generate files the same way the scaffolds do.
		if strings.HasPrefix(pkg.ImportPath, modulePath+"/tools/") {
			continue
		}
		for _, imp := range pkg.Imports {
			if hasPrefix(imp, templateEnginePrefixes) {
				offenders = append(offenders, pkg.ImportPath+" imports "+imp)
			}
		}
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(
			"a second template engine has appeared. PLAN.md Section 25 allows the template renderer exactly one "+
				"implementation, which is internal/render, reached through its Engine port.\n"+
				"If a call site needs something the strict subset cannot express, change internal/render's one "+
				"implementation behind that port rather than adding an engine here.\n%v",
			offenders)
	}
}

// TestRendererAllowlistHasNoStaleEntries keeps the allowlist a true record
// of what actually imports a template engine, mirroring
// TestAdapterAllowlistHasNoStaleEntries one for one.
//
// A stale entry is not harmless. It is a standing, unexamined permission,
// and the next package to move into that import path inherits it silently.
func TestRendererAllowlistHasNoStaleEntries(t *testing.T) {
	seen := make(map[string]bool, len(templateEngineAllowlist))
	for _, pkg := range goList(t, false, modulePath+"/internal/...") {
		for _, imp := range pkg.Imports {
			if hasPrefix(imp, templateEnginePrefixes) {
				seen[pkg.ImportPath] = true
			}
		}
	}

	stale := make([]string, 0)
	for pkg := range templateEngineAllowlist {
		if !seen[pkg] {
			stale = append(stale, pkg)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("templateEngineAllowlist entries that no longer import a template engine: %v", stale)
	}
}

// TestRenderPackageDependsOnNothingButTheStandardLibrary guards the
// primitive's own reusability.
//
// Section 25 names four call sites for the renderer, spread across
// credential injection, notification delivery, inventory construction and
// survey defaults. A shared primitive that dragged in a dependency on any
// one consumer's domain would stop being shareable by the others, and the
// failure would show up as an import cycle in whichever phase came second.
// Keeping it standard-library-only makes that impossible rather than
// unlikely.
func TestRenderPackageDependsOnNothingButTheStandardLibrary(t *testing.T) {
	assertOnlyStandardLibrary(t, modulePath+"/internal/render")
}

// TestRedactPackageDependsOnNothingButTheStandardLibrary is the same guard
// for the masking ruleset, and it matters more.
//
// redact is imported by internal/engine, by both execution adapters, by
// internal/launch, and by every cmd/ binary. A dependency on any domain
// package would create a cycle with at least one of them, and the pressure
// to "just import the credential package for its constants" is real.
func TestRedactPackageDependsOnNothingButTheStandardLibrary(t *testing.T) {
	assertOnlyStandardLibrary(t, modulePath+"/internal/redact")
}

// assertOnlyStandardLibrary fails if pattern's package has any dependency
// outside the standard library.
func assertOnlyStandardLibrary(t *testing.T, pattern string) {
	t.Helper()

	foreign := make([]string, 0)
	for _, pkg := range goList(t, true, pattern) {
		for _, dep := range pkg.Deps {
			// A standard-library path has no dot in its first element.
			// "encoding/json" does not; "github.com/x/y" does.
			first := dep
			if i := strings.Index(dep, "/"); i >= 0 {
				first = dep[:i]
			}
			if strings.Contains(first, ".") {
				foreign = append(foreign, dep)
			}
		}
	}

	sort.Strings(foreign)
	if len(foreign) > 0 {
		t.Errorf("%s must depend only on the standard library, but depends on: %v", pattern, foreign)
	}
}

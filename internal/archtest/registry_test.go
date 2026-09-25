// This file asserts the Section 25 "Typed generic Registry" build-once
// contract (.SPECIFICATION/PLAN.md Section 25's Enforcement note): the
// package-level vocabularies this repository builds on pkg/registry.Registry
// stay wired to that one shared primitive, so a future hand-rolled map
// added beside it -- the "quietly-copied-primitive failure" Section 25
// itself names -- shows up as a failing test rather than a silent drift.
//
// Import-graph analysis can only prove the positive claim (these packages
// still import pkg/registry); it cannot prove a fourth, unrelated
// hand-rolled map does not exist anywhere else in the tree. That remains a
// code-review-time convention, the same as the rest of Section 25's list.
package archtest

import "testing"

// registryConsumers is every package known to build its own vocabulary on
// pkg/registry.Registry rather than a bespoke map: pkg/capability's
// capability vocabulary, internal/inventory/record's device-type table
// (both Phase 6), pkg/collection's namespaced method registry (Phase 31,
// this primitive's third consumer), internal/inventory/syncplugin's plugin
// table, and internal/ui/view's web UI resource table (Phase 19, the
// fifth). The UI entry matters for the same reason as the rest: a view
// registry is exactly the kind of table somebody would otherwise hand-roll
// as a map[string]Descriptor beside the four that already exist.
//
// The last three arrived when this list was found to be asking its
// question in one direction only. internal/launch's kind table and
// internal/credtype's target table are both package-level registries that
// had never been recorded here, and internal/engine builds a
// Registry[TransportBinding] per call rather than at package scope -- its
// own doc comment at transport_bindings.go calls it "a fourth consumer of
// the one shared generic Registry primitive", a claim the guard meant to
// track consumers could not see. That is LESSONS_LEARNED.md #155's "ask
// the question from both ends" missing from the very file the rule is
// about; TestEveryPkgRegistryImporterIsOnTheAllowlist below is the other
// end. internal/inventory/onboard's prober table (Phase 111) is the
// newest; internal/inventory/record also holds the onboarded-type table
// beside its device-type one.
var registryConsumers = map[string]bool{
	modulePath + "/pkg/capability":                true,
	modulePath + "/internal/inventory/record":     true,
	modulePath + "/pkg/collection":                true,
	modulePath + "/internal/inventory/syncplugin": true,
	modulePath + "/internal/ui/view":              true,
	modulePath + "/internal/launch":               true,
	modulePath + "/internal/launchable":           true,
	modulePath + "/internal/credtype":             true,
	modulePath + "/internal/engine":               true,
	modulePath + "/internal/inventory/onboard":    true,
}

// TestKnownRegistryConsumersImportPkgRegistry asserts every package on the
// registryConsumers list actually imports pkg/registry, proving each one
// consumes the shared primitive rather than a look-alike map it rolled
// itself.
func TestKnownRegistryConsumersImportPkgRegistry(t *testing.T) {
	for _, pkg := range goList(t, false, modulePath+"/...") {
		if !registryConsumers[pkg.ImportPath] {
			continue
		}
		found := false
		for _, imp := range pkg.Imports {
			if imp == modulePath+"/pkg/registry" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is a known registryConsumers entry but does not import %s/pkg/registry", pkg.ImportPath, modulePath)
		}
	}
}

// TestRegistryConsumerAllowlistHasNoStaleEntries fails if a package on
// registryConsumers no longer exists, keeping the list an honest record
// rather than accumulating dead entries nothing checks.
func TestRegistryConsumerAllowlistHasNoStaleEntries(t *testing.T) {
	seen := make(map[string]bool)
	for _, pkg := range goList(t, false, modulePath+"/...") {
		seen[pkg.ImportPath] = true
	}

	for pkg := range registryConsumers {
		if !seen[pkg] {
			t.Errorf("registryConsumers entry %q does not match any package in the module", pkg)
		}
	}
}

// TestEveryPkgRegistryImporterIsOnTheAllowlist asks the question the two
// tests above do not: not "does every listed package still use the shared
// primitive", but "is every package that uses it listed".
//
// Both directions are needed and neither implies the other. Without this
// one the allowlist degrades into a list of the consumers somebody
// remembered, which is what it had become: it named five while go list
// reported eight, and the three it omitted included the package whose own
// doc comment advertises itself as a consumer. A list that cannot notice
// its own omissions documents nothing and guards nothing.
//
// It matters beyond bookkeeping because this list is where a reader looks
// to answer "which tables in this module are process-wide". Every entry
// here except internal/engine's, which builds its registry per call, owns
// a package-level table that outlives any single test -- the property that
// made nine packages unable to run under -count>1 at once. A consumer
// missing from this list is a table nobody thought to check.
func TestEveryPkgRegistryImporterIsOnTheAllowlist(t *testing.T) {
	for _, pkg := range goList(t, false, modulePath+"/...") {
		if registryConsumers[pkg.ImportPath] {
			continue
		}
		for _, imp := range pkg.Imports {
			if imp != modulePath+"/pkg/registry" {
				continue
			}
			t.Errorf("%s imports %s/pkg/registry but is not on registryConsumers. "+
				"Add it: this list is where a reader looks to find every table in this module built on "+
				"the shared primitive, and one that is missing is one nobody knows to check",
				pkg.ImportPath, modulePath)
		}
	}
}

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
// (both Phase 6), and pkg/collection's namespaced method registry (Phase
// 31, this primitive's third consumer).
var registryConsumers = map[string]bool{
	modulePath + "/pkg/capability":                true,
	modulePath + "/internal/inventory/record":     true,
	modulePath + "/pkg/collection":                true,
	modulePath + "/internal/inventory/syncplugin": true,
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

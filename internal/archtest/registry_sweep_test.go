// This file is the registry consistency sweep: one place that proves every
// vocabulary this codebase registers is internally coherent and actually
// reachable from a real binary.
//
// It exists because the same failure has now happened twice. A generated
// Collection catalog compiled and unit-tested cleanly while being entirely
// unreachable from the real binary (FAILURE_PATTERNS.md #52), because a
// package's init() only runs when something imports it and each generated
// package's own test binary was the only thing that did.
// LESSONS_LEARNED.md #56 generalizes it: a per-package init()-registration
// pattern needs a composition-root aggregator from day one, proven by an
// integration-level test rather than by each package's isolated unit test.
//
// The sweep covers what the per-entry tests in catalog_test.go do not:
// cross-registry agreement (a manifest naming a capability that exists),
// hierarchy closure (a capability whose parent is registered), and
// uniqueness within each vocabulary.
package archtest

import (
	"strings"
	"testing"

	_ "github.com/SubjectVoidLLC/the-pleiades/internal/catalog"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/plugins"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// TestCapabilityHierarchyIsClosed proves every registered capability's
// declared parent is itself registered.
//
// An unregistered parent is not a cosmetic gap. capability.Resolves walks
// upward to decide whether a device declaring AptCapable also satisfies a
// method requiring the broader PackageManagerCapable, and a missing link
// silently truncates that walk: the device would stop resolving to the
// parent, and a runbook that should have matched would quietly not.
func TestCapabilityHierarchyIsClosed(t *testing.T) {
	all := capability.All()
	if len(all) == 0 {
		t.Fatal("no capabilities are registered, which means pkg/capability was not linked in")
	}

	for name, desc := range all {
		if desc.Parent == "" {
			continue
		}
		if _, ok := all[desc.Parent]; !ok {
			t.Errorf("capability %q declares parent %q, which is not registered: capability.Resolves would stop walking here",
				name, desc.Parent)
		}
		if desc.Parent == name {
			t.Errorf("capability %q is its own parent, which would make capability.Resolves loop", name)
		}
	}
}

// TestCapabilityDescriptorsAreWellFormed proves every registered capability
// can actually be asserted against. A descriptor with no Assert would make
// HasCapability's structural half unanswerable, and the whole point of
// HasCapability is that a true result is a guarantee rather than a hope.
func TestCapabilityDescriptorsAreWellFormed(t *testing.T) {
	for name, desc := range capability.All() {
		if desc.Name != name {
			t.Errorf("capability registered under key %q reports name %q", name, desc.Name)
		}
		if desc.Assert == nil {
			t.Errorf("capability %q has no Assert, so its structural half can never be checked", name)
		}
		if !strings.HasSuffix(string(name), "Capable") {
			t.Errorf("capability %q does not end in Capable, breaking the vocabulary's naming convention", name)
		}
	}
}

// TestCollectionManifestsNameKnownCapabilities proves every capability any
// registered Collection method requires actually exists in the capability
// vocabulary.
//
// pkg/collection.Register already rejects an unknown capability at
// registration, so this is a backstop rather than the primary guard. It
// earns its place by covering the whole registry at once: a method
// registered through some future path that skips Register would be caught
// here rather than at the first runbook that calls it.
func TestCollectionManifestsNameKnownCapabilities(t *testing.T) {
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Errorf("catalog entry %q is not registered", cfg.Name)
			continue
		}
		for _, name := range desc.Manifest.RequiredCapabilities {
			if _, known := capability.Lookup(name); !known {
				t.Errorf("collection %q requires capability %q, which is not registered", cfg.Name, name)
			}
		}
	}
}

// TestCollectionNamesAreNamespacedAndUnique proves PLAN.md Section 2's
// naming rule holds across the whole catalog, and that no two entries in
// the source of truth claim the same name.
//
// Uniqueness is checked against catalogdata rather than the registry
// because the registry cannot answer it: a duplicate registration is
// refused outright, so by the time a name is in the registry the duplicate
// has already been dropped. The data table is where a duplicate would sit
// undetected.
func TestCollectionNamesAreNamespacedAndUnique(t *testing.T) {
	seen := make(map[string]bool, len(catalogdata.Collections))
	for _, cfg := range catalogdata.Collections {
		if !strings.Contains(cfg.Name, ".") {
			t.Errorf("collection %q is not namespaced (PLAN.md Section 2 requires <namespace>.<method>)", cfg.Name)
		}
		if seen[cfg.Name] {
			t.Errorf("collection %q appears more than once in catalogdata.Collections", cfg.Name)
		}
		seen[cfg.Name] = true
	}
}

// TestDeviceTypeKeysAreUnique proves no two catalogdata device entries
// claim the same registry key. record.RegisterType panics on a duplicate,
// so a collision would take the process down at init rather than being
// silently resolved, but the data table is still where the mistake is made
// and this is where it is cheapest to see.
func TestDeviceTypeKeysAreUnique(t *testing.T) {
	seen := make(map[string]bool, len(catalogdata.Devices))
	for _, cfg := range catalogdata.Devices {
		if seen[cfg.TypeKey] {
			t.Errorf("device type %q appears more than once in catalogdata.Devices", cfg.TypeKey)
		}
		seen[cfg.TypeKey] = true
	}
}

// TestDeviceCapabilitiesAreKnown proves every capability a generated device
// type declares exists in the vocabulary, so a typo in catalogdata fails
// here rather than as a capability that silently never matches anything.
func TestDeviceCapabilitiesAreKnown(t *testing.T) {
	for _, cfg := range catalogdata.Devices {
		for _, name := range cfg.Capabilities {
			if _, known := capability.Lookup(name); !known {
				t.Errorf("device type %q declares capability %q, which is not registered", cfg.TypeKey, name)
			}
		}
	}
}

// TestCatalogPlugins_AllRegistered proves every catalogdata.Plugins entry is
// reachable through the sync plugin registry in a binary that imports the
// plugins composition root.
//
// This is the same reachability check catalog_test.go applies to Collection
// methods and device types, applied to the third registry. It is the check
// that would have caught FAILURE_PATTERNS.md #52 the first time.
func TestCatalogPlugins_AllRegistered(t *testing.T) {
	for _, cfg := range catalogdata.Plugins {
		desc, ok := syncplugin.Lookup(cfg.Name)
		if !ok {
			t.Errorf("catalogdata.Plugins entry %q is not registered; internal/inventory/plugins/builtins.go may be missing its blank import", cfg.Name)
			continue
		}
		if desc.New == nil {
			t.Errorf("sync plugin %q has no constructor", cfg.Name)
		}
		if desc.Description == "" {
			t.Errorf("sync plugin %q has no description", cfg.Name)
		}
	}
}

// TestRegisteredPluginsAreWellFormed proves every sync plugin in the
// registry, whether it came from catalogdata or predates it, satisfies the
// invariants the CLI depends on.
func TestRegisteredPluginsAreWellFormed(t *testing.T) {
	all := syncplugin.All()
	if len(all) == 0 {
		t.Fatal("no sync plugins are registered, which means internal/inventory/plugins was not linked in")
	}

	for name, desc := range all {
		if desc.Name != name {
			t.Errorf("sync plugin registered under key %q reports name %q", name, desc.Name)
		}
		if desc.New == nil {
			t.Errorf("sync plugin %q has no constructor", name)
			continue
		}
		if desc.New() == nil {
			t.Errorf("sync plugin %q constructor returned nil", name)
		}
		if desc.DefaultConfig.Name != "" && desc.DefaultConfig.Name != name {
			t.Errorf("sync plugin %q ships a default config named %q, so a synced device would carry the wrong source authority",
				name, desc.DefaultConfig.Name)
		}
		switch desc.Status {
		case "", syncplugin.StatusDeclared, syncplugin.StatusImplemented:
		default:
			t.Errorf("sync plugin %q has unknown status %q", name, desc.Status)
		}
	}
}

// TestCatalogPackagesImportOnlyPkg enforces the layering rule every
// generated Collection package's own doc comment states and nothing checked
// until now: a Collection may import pkg/ and the standard library, and
// nothing else in this module.
//
// The rule is not about visibility, which Go's internal/ mechanism already
// handles. It is that a built-in Collection must live under exactly the
// constraint a third-party Collection will have to satisfy once Part X's
// OCI distribution exists. A built-in that quietly reached into internal/
// would be proving a pattern nobody outside this repository can follow, and
// the resulting API would look fine right up until the first external
// Collection tried to compile.
//
// This is why pkg/catalystcenter exists at all: the net.catalyst.* methods
// and the inventory sync plugin share a REST client, and the plugin lives
// under internal/, so the shared code had to go where both can reach it.
func TestCatalogPackagesImportOnlyPkg(t *testing.T) {
	const catalogPrefix = modulePath + "/internal/catalog"

	pkgs := goList(t, false, catalogPrefix+"/...")
	if len(pkgs) == 0 {
		t.Fatalf("go list found no packages under %s", catalogPrefix)
	}

	var checked int
	for _, pkg := range pkgs {
		// internal/catalog itself is the blank-import aggregator: importing
		// every generated package is its entire job, so the rule cannot
		// apply to it.
		if pkg.ImportPath == catalogPrefix {
			continue
		}
		checked++

		for _, imp := range pkg.Imports {
			if !strings.HasPrefix(imp, modulePath+"/") {
				// Standard library or a third-party module. Neither is what
				// this rule is about.
				continue
			}
			if strings.HasPrefix(imp, modulePath+"/pkg/") {
				continue
			}
			t.Errorf("%s imports %s: a Collection package may import only pkg/, matching the constraint a third-party Collection must satisfy",
				pkg.ImportPath, imp)
		}
	}

	if checked == 0 {
		t.Fatal("no generated Collection packages were checked, so this test proved nothing")
	}
}

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
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/resources"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// viewSweepOnce registers the view table exactly once for this package.
//
// It registers over zero-valued ports deliberately. Everything below is a
// structural question -- names, nav order, field declarations, which
// endpoints a descriptor points at -- and none of it calls a handler, so
// the ports are never reached. Standing up a real repository, job store and
// dispatcher to ask whether two views share a nav position would make this
// sweep depend on Docker to check a struct field.
var viewSweepOnce sync.Once

func registerViewsForSweep(t *testing.T) {
	t.Helper()
	viewSweepOnce.Do(func() {
		if err := resources.RegisterAll(resources.Deps{}); err != nil {
			t.Fatalf("resources.RegisterAll() = %v, want nil: every built-in view must register", err)
		}
	})
}

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

// TestImplementedCollectionCapabilitiesAreSatisfiable proves every
// StatusImplemented Collection method's RequiredCapabilities can actually
// be satisfied by at least one registered device type -- not just that
// the capability NAME exists in the vocabulary
// (TestCollectionManifestsNameKnownCapabilities already covers that), but
// that some concrete type structurally implements it, via the same
// HasCapability check engine.checkMethodCapabilities runs before ever
// dispatching a task.
//
// This is the gap Phase 73 found and closed: container.docker.run/stop/
// remove shipped StatusImplemented with RequiredCapabilities naming
// DockerCapable while zero device types implemented it, so
// engine.checkMethodCapabilities refused every real invocation.
// docker_test.go's own suite never caught it, because it builds its
// device as an inventorytest.Stub, which deliberately skips the
// structural assertion HasCapability performs on a real type (its own
// doc comment says so) -- RULE 0's exact thesis, and
// linux/server.go:89-99's own comment records the identical class of gap
// happening once before for POSIXFileSystemCapable/FactGathererCapable.
// See FAILURE_PATTERNS.md.
//
// Running this for the first time also found five capabilities besides
// DockerCapable that no device type satisfies. Four of the five
// (acceptedUnsatisfiableCapabilities below) turned out to already be
// disclosed, settled, intentional architecture in their own implementing
// package's doc comment -- genuinely per-distro or not-yet-collected
// classification data, the same reasoning AptCapable's own doc comment
// gives. The fifth, FirewalldCapable, was not disclosed anywhere until
// this sweep found it, and got the same disclosure added
// (internal/catalog/fw/firewalld/firewalld.go) rather than a silent
// exemption. A sixth, NetworkAddressableCapable, was fixed for real
// instead of allowlisted: IPAddress() is trivial, already-known data on
// every network-reachable device type, unlike AptCapable's genuinely
// exclusive per-distro choice, so there was no honest architectural
// reason to leave it unsatisfiable.
func TestImplementedCollectionCapabilitiesAreSatisfiable(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)

	var checked int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			// TestCollectionManifestsNameKnownCapabilities already fails
			// loudly on this; this test's own dimension is satisfiability,
			// not reachability, so it does not duplicate that error.
			continue
		}
		if desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		checked++
		for _, name := range desc.Manifest.RequiredCapabilities {
			if satisfiable[name] {
				continue
			}
			if _, accepted := acceptedUnsatisfiableCapabilities[name]; accepted {
				continue
			}
			t.Errorf("collection %q is StatusImplemented and requires capability %q, but no registered device type structurally implements it, so engine.checkMethodCapabilities would refuse every real invocation of it -- either wire a device type to it, or add it to acceptedUnsatisfiableCapabilities with a citation to an honest disclosure in its implementing package's own doc comment", cfg.Name, name)
		}
	}

	if checked == 0 {
		t.Fatal("no StatusImplemented collection methods were checked, so this test proved nothing")
	}
}

// acceptedUnsatisfiableCapabilities is this sweep's allowlist, matching
// gosec-waivers.json's established convention: a per-entry reason, no
// blanket suppression by rule or package. Every entry here cites a real
// disclosure already written into the implementing package's own doc
// comment -- this map does not invent a new exemption, it points at one
// that already exists in source, so removing the map and reading the
// cited comment directly would tell the same story.
//
// TestAcceptedUnsatisfiableCapabilitiesAreNotStale (below) is this
// allowlist's own drift guard, the same role
// TestAdapterAllowlistHasNoStaleEntries and TestResolverConsumerAllowlistHasNoStaleEntries
// play for their allowlists: an entry that quietly stopped being true
// (because a device type was later wired to it for real) must be
// removed, not left to accumulate.
var acceptedUnsatisfiableCapabilities = map[capability.Name]string{
	// Empty since 2026-09-24, when linux_server gained the accessors for
	// AptCapable, DnfCapable, PackageManagerCapable, FirewalldCapable and
	// PosixAccountCapable; every capability an implemented method requires
	// is now satisfiable by some device type.
}

// TestAcceptedUnsatisfiableCapabilitiesAreNotStale proves every entry in
// acceptedUnsatisfiableCapabilities is still genuinely unsatisfiable. An
// exemption that silently stopped applying is exactly as wrong as one
// that was never justified, just in the opposite direction: it would
// hide that a real fix landed and the doc comment it cites is now false.
func TestAcceptedUnsatisfiableCapabilitiesAreNotStale(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)
	for name := range acceptedUnsatisfiableCapabilities {
		if satisfiable[name] {
			t.Errorf("capability %q is allowlisted as unsatisfiable in acceptedUnsatisfiableCapabilities, but a registered device type now structurally implements it -- remove the stale entry and the doc comment it cites", name)
		}
	}
}

// satisfiableCapabilities constructs one instance of every registered
// device type and returns the set of capability names at least one of
// them structurally satisfies via HasCapability -- the exact check
// engine.checkMethodCapabilities performs before dispatch, not merely a
// name lookup in the vocabulary.
//
// Every probe Record is hydrated with EVERY registered capability name
// as classification data (Capabilities: allNames), not left empty. This
// matters, and getting it wrong would defeat this sweep's own purpose: a
// capability this codebase intends to be classification-only (AptCapable,
// DnfCapable, PosixAccountCapable, FirewalldCapable -- all four say so in
// their own implementing package's doc comment) is never in any type's
// static baseline, so HasCapability against an unclassified probe would
// report every one of them permanently unsatisfiable regardless of
// whether a real accessor exists -- which would make
// TestAcceptedUnsatisfiableCapabilitiesAreNotStale unable to ever detect
// that one of those four genuinely got fixed, silently defeating the one
// test that exists to catch a stale allowlist entry. Declaring every
// name up front makes HasCapability's Declares half trivially true for
// every device, so what remains under test is exactly the structural
// half (capability.Implements, called through each type's own
// HasCapability override rather than around it), which is the question
// this sweep actually needs answered: not "did classification run,"
// but "could classification ever make this true."
func satisfiableCapabilities(t *testing.T) map[capability.Name]bool {
	t.Helper()

	types := record.AllTypes()
	if len(types) == 0 {
		t.Fatal("record.AllTypes() returned no device types, so this test proved nothing")
	}

	all := capability.All()
	if len(all) == 0 {
		t.Fatal("capability.All() returned no capabilities, which means pkg/capability was not linked in")
	}
	allNames := make([]capability.Name, 0, len(all))
	for name := range all {
		allNames = append(allNames, name)
	}

	satisfiable := make(map[capability.Name]bool)
	for typeKey, ctor := range types {
		item, err := ctor(record.Record{
			ID:           "archtest-probe",
			Name:         "archtest-probe",
			Type:         typeKey,
			Capabilities: allNames,
			Properties:   genericProbeProperties(typeKey),
		})
		if err != nil {
			t.Fatalf("constructing a fully classified %q device failed: %v", typeKey, err)
		}
		for name := range all {
			if item.HasCapability(name) {
				satisfiable[name] = true
			}
		}
	}
	return satisfiable
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
		if desc.New(syncplugin.Deps{}) == nil {
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

// TestViewsAreCoherent sweeps the web UI's view registry the same way the
// tests above sweep the capability, Collection and device-type tables.
//
// The registry's own Register already refuses an incoherent descriptor at
// process start, so this is not a second copy of those rules. It checks the
// properties that are only visible across the whole table -- a nav that
// reshuffles because two views claim one position, a URL segment two views
// both answer to -- which no single registration can see.
//
// It imports internal/ui/resources for its side effects only, which is also
// what proves that package is wired: a view absent from registrars() is
// absent here, and this test is where that shows up rather than in a user's
// empty sidebar.
func TestViewsAreCoherent(t *testing.T) {
	registerViewsForSweep(t)

	names := view.Names()
	if len(names) == 0 {
		t.Fatal("no views are registered, so this test proved nothing")
	}

	navOrders := make(map[int]string, len(names))
	for _, name := range names {
		d, ok := view.Lookup(name)
		if !ok {
			t.Fatalf("view %q is in Names() but not in Lookup()", name)
		}

		// Two views at one nav position make the sidebar order depend on
		// the tiebreaker rather than on intent, which is how a nav quietly
		// reorders itself between releases.
		if prev, dup := navOrders[d.NavOrder]; dup {
			t.Errorf("views %q and %q both claim nav order %d", prev, name, d.NavOrder)
		}
		navOrders[d.NavOrder] = name

		// Every endpoint a view names must be one the API really declares.
		// Register checks this too; checking it here as well is what
		// catches an apispec entry that was renamed after registration.
		for _, endpoint := range d.Ops.Candidates() {
			if endpoint.Scope == "" {
				t.Errorf("view %q names an operation with no scope, so nothing gates it", name)
			}
		}

		fieldNames := make(map[string]bool, len(d.Fields))
		for _, f := range d.Fields {
			if fieldNames[f.Name] {
				t.Errorf("view %q declares field %q more than once", name, f.Name)
			}
			fieldNames[f.Name] = true

			// A badge class outside the closed set reaches a class
			// attribute and silently renders unstyled, which is how a
			// failed job ends up looking like a successful one.
			if f.BadgeClass == nil {
				continue
			}
			for _, probe := range []string{"", "completed", "failed", "unknown-value-nobody-declared"} {
				if class := f.BadgeClass(probe); !view.ValidBadgeClasses[class] {
					t.Errorf("view %q field %q maps %q to class %q, which is outside the validated set",
						name, f.Name, probe, class)
				}
			}
		}

		if d.ListsRecords() && !fieldNames[d.IDField] {
			t.Errorf("view %q lists records keyed on %q, which is not one of its fields", name, d.IDField)
		}
	}
}

// TestEveryImplementedMethodAnswersReversibility sweeps the catalog for a
// method that never says whether it can be undone.
//
// Registration already refuses a non-reversible method with no reason, so
// what this adds is the whole-table view registration cannot have: that
// every implemented method really was asked, and that the sweep examined
// something rather than passing by looking at nothing.
//
// It replaced a sweep that checked an inverse FQCN resolved to a
// registered method. That check went away with the field: the manifest no
// longer names an inverse, because the real one depends on what a run
// found rather than on what the method is, and a method emits it at run
// time instead. What used to be a build-time typo check is now covered
// where it belongs, by each method's own tests asserting the emitted
// instruction, including that its parameter names match the method that
// would receive it.
func TestEveryImplementedMethodAnswersReversibility(t *testing.T) {
	if len(catalogdata.Collections) == 0 {
		t.Fatal("catalogdata registered no collections, so this test proved nothing")
	}

	var answered int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Manifest.Status != collection.StatusImplemented {
			continue
		}
		answered++

		// The one thing that can be wrong here and nowhere else: a method
		// claiming it cannot be undone without saying what about its effect
		// this platform cannot observe. Registration refuses it, so reaching
		// this loop means it passed; asserting it again is cheap and keeps
		// the rule visible where a reader is looking for it.
		if !desc.Manifest.Reversibility.Reversible && desc.Manifest.Reversibility.Notes == "" {
			t.Errorf("%s declares itself not reversible with no reason", cfg.Name)
		}
	}

	if answered == 0 {
		t.Fatal("no implemented method was examined, so this test proved nothing")
	}
	t.Logf("%d implemented method(s) answered reversibility", answered)
}

// TestCatalogDataDocsMatchTheRegistry proves each entry in
// internal/forge/catalogdata carries the same Doc the corresponding
// package actually registered.
//
// The two are unavoidably separate copies. catalogdata is the source
// `forge new-collection` is driven from, but the generator only ever
// emits Doc.Summary (collectionscaffold's template), and the rest of a
// real method's documentation is written by hand into the generated file
// afterward. Nothing checked that the hand-written half matched what
// catalogdata says it should be, so a Doc could be edited in one place
// and silently disagree with the other. The disagreement is invisible
// until somebody regenerates the catalog from scratch, at which point
// the documentation quietly reverts.
//
// Equality is the right invariant rather than "catalogdata is a subset,"
// because catalogdata's own doc comment states the rule: editing the
// catalog means editing that data, never hand-editing the output.
func TestCatalogDataDocsMatchTheRegistry(t *testing.T) {
	if len(catalogdata.Collections) == 0 {
		t.Fatal("catalogdata registered no collections, so this test proved nothing")
	}

	var checked int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			// TestEveryCatalogDataEntryIsRegistered is what owns this
			// failure; reporting it twice would only make one problem look
			// like two.
			continue
		}
		checked++

		if diff := describeDocDiff(cfg.Doc, desc.Manifest.Doc); diff != "" {
			t.Errorf("%s: catalogdata and the registered manifest disagree: %s", cfg.Name, diff)
		}
	}

	if checked == 0 {
		t.Fatal("no catalogdata entry resolved in the registry, so this test proved nothing")
	}
}

// TestEveryUncheckableMethodSaysWhy holds the built-in catalog to
// Manifest.NoCheckReason: every implemented method without check support
// says why it cannot be checked, since that reason is what a check run's
// "could not check" line, validation's refusal of check_mode and the
// reference page print, and the bare "does not declare check support"
// tells an operator nothing to act on.
func TestEveryUncheckableMethodSaysWhy(t *testing.T) {
	var uncheckable int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Manifest.Status != collection.StatusImplemented || desc.Manifest.SupportsCheck {
			continue
		}
		uncheckable++
		if desc.Manifest.NoCheckReason == "" {
			t.Errorf("%s supports no check and does not say why (Manifest.NoCheckReason)", cfg.Name)
		}
	}
	if uncheckable == 0 {
		t.Log("every implemented method supports check, so no reason was needed")
	}
}

// describeDocDiff returns a human-readable description of the first way
// two Docs differ, or the empty string when they match.
//
// It compares field by field rather than with reflect.DeepEqual on the
// whole struct so a failure says which field drifted, which is the
// difference between a one-line fix and re-reading two thirty-line
// literals side by side.
func describeDocDiff(want, got collection.Doc) string {
	switch {
	case want.Summary != got.Summary:
		return fmt.Sprintf("Summary: catalogdata has %q, the manifest has %q", want.Summary, got.Summary)
	case want.Description != got.Description:
		return fmt.Sprintf("Description: catalogdata has %q, the manifest has %q", want.Description, got.Description)
	case want.SinceVersion != got.SinceVersion:
		return fmt.Sprintf("SinceVersion: catalogdata has %q, the manifest has %q", want.SinceVersion, got.SinceVersion)
	case want.Deprecated != got.Deprecated:
		return fmt.Sprintf("Deprecated: catalogdata has %q, the manifest has %q", want.Deprecated, got.Deprecated)
	case !reflect.DeepEqual(want.Params, got.Params):
		return fmt.Sprintf("Params: catalogdata has %d entr(ies), the manifest has %d, and they are not identical", len(want.Params), len(got.Params))
	case !reflect.DeepEqual(want.Fragments, got.Fragments):
		return fmt.Sprintf("Fragments: catalogdata has %v, the manifest has %v", want.Fragments, got.Fragments)
	case !reflect.DeepEqual(want.Returns, got.Returns):
		return fmt.Sprintf("Returns: catalogdata has %d entr(ies), the manifest has %d, and they are not identical", len(want.Returns), len(got.Returns))
	case !reflect.DeepEqual(want.Examples, got.Examples):
		return fmt.Sprintf("Examples: catalogdata has %d entr(ies), the manifest has %d, and they are not identical", len(want.Examples), len(got.Examples))
	case !reflect.DeepEqual(want.SeeAlso, got.SeeAlso):
		return fmt.Sprintf("SeeAlso: catalogdata has %v, the manifest has %v", want.SeeAlso, got.SeeAlso)
	default:
		return ""
	}
}

// genericProbeProperties returns what a generic device type needs to be
// fully equipped, and nil for any other type. A generic type ignores
// classification (its capabilities come from the device), so the sweep's
// "declare everything" is, for it, a discovery granting everything
// onboarding may grant, plus the address properties its constructor
// requires. The question stays the one the sweep asks: could onboarding
// ever make this capability true.
func genericProbeProperties(typeKey string) map[string]inventory.PropertyValue {
	grants := generic.Discoverable(typeKey)
	if grants == nil {
		return nil
	}
	props := map[string]inventory.PropertyValue{
		"host":                     "archtest.invalid",
		generic.BaseURLProperty:    "https://archtest.invalid",
		generic.GRPCTargetProperty: "archtest.invalid:443",
	}
	// Bound to these very properties, as onboarding binds a discovery, so
	// a type that binds its discovery (generic_http, generic_grpc) holds
	// what it grants.
	d := inventory.Discovery{Protocol: "archtest", Capabilities: grants}
	d.Binding = generic.Binding(typeKey, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = d.Property()
	return props
}

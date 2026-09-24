package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	// Blank-imported for the reason internal/inventory/builtins.go itself
	// exists: record.AllTypes is empty until every vendor device package's
	// init() has run, and the device gates below would then pass by proving
	// nothing.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestImplementedMethodsHaveCompleteDocs is the completeness gate the plan
// calls for: every catalogdata entry whose live Manifest carries
// Status == StatusImplemented must have a real Doc, not just a Summary. A
// declared (not yet implemented) method is exempt by design (see
// collection.Doc's own doc comment); this test only tightens as methods
// flip from declared to implemented, one phase at a time, rather than all
// at once at the end of the catalog.
func TestImplementedMethodsHaveCompleteDocs(t *testing.T) {
	for _, cfg := range catalogdata.Collections {
		fqcn := cfg.Name
		desc, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s: in catalogdata but never registered into pkg/collection", fqcn)
		}
		m := desc.Manifest
		if m.Status != collection.StatusImplemented {
			continue
		}
		t.Run(fqcn, func(t *testing.T) {
			if m.Doc.Summary == "" {
				t.Errorf("%s: implemented but Doc.Summary is empty", fqcn)
			}
			for i, p := range m.Doc.Params {
				if p.Type == "" {
					t.Errorf("%s: param %q (index %d) has no Type", fqcn, p.Name, i)
				}
				if p.Description == "" {
					t.Errorf("%s: param %q (index %d) has no Description", fqcn, p.Name, i)
				}
			}
			if len(m.Doc.Examples) == 0 {
				t.Errorf("%s: implemented but Doc.Examples is empty", fqcn)
			}
			for _, name := range m.Doc.Fragments {
				if _, ok := catalogdata.Fragments[name]; !ok {
					t.Errorf("%s: references fragment %q, not present in catalogdata.Fragments", fqcn, name)
				}
			}
		})
	}
}

// TestDeviceRowsMatchLiveRegistry is the completeness gate devices.go
// depends on and nothing provided until now. That page's rows come from
// catalogdata.Devices plus the two entries recorded by hand in
// handWrittenDevices, so without this test a device type registered
// anywhere else would silently be missing from the page, and a row whose
// type key was renamed would silently keep documenting a type no binary can
// hydrate.
func TestDeviceRowsMatchLiveRegistry(t *testing.T) {
	live := record.AllTypes()
	if len(live) == 0 {
		t.Fatal("no device types are registered, which means internal/inventory was not linked in")
	}

	rendered := map[string]bool{}
	for _, d := range handWrittenDevices {
		rendered[d.TypeKey] = true
	}
	for _, d := range catalogdata.Devices {
		rendered[d.TypeKey] = true
	}

	for key := range live {
		if !rendered[key] {
			t.Errorf("device type %q is registered but devices.md would not list it: add it to catalogdata.Devices, or to handWrittenDevices if it predates the Forge", key)
		}
	}
	for key := range rendered {
		if _, ok := live[key]; !ok {
			t.Errorf("device type %q would be listed on devices.md but is not registered: the page would document a type no binary can hydrate", key)
		}
	}
}

// TestHandWrittenDeviceCapabilitiesMatchTheirTypes proves the capability
// lists recorded by hand in handWrittenDevices are still the ones the real
// constructor hands back. Those two lists are the one thing on the device
// page typed into this generator rather than read from a registry, so this
// is where a hand-edit that fell out of step with the code gets caught.
func TestHandWrittenDeviceCapabilitiesMatchTheirTypes(t *testing.T) {
	for _, d := range handWrittenDevices {
		t.Run(d.TypeKey, func(t *testing.T) {
			constructor, ok := record.LookupType(d.TypeKey)
			if !ok {
				t.Fatalf("%s is not registered", d.TypeKey)
			}
			// A bare Record carrying only the type key is enough here: the
			// baseline capability set is what the constructor adds itself,
			// never something the record supplies.
			item, err := constructor(record.Record{Name: d.TypeKey, Type: d.TypeKey})
			if err != nil {
				t.Fatalf("hydrating %s: %v", d.TypeKey, err)
			}

			got := make([]string, 0, len(item.Capabilities()))
			for _, c := range item.Capabilities() {
				got = append(got, string(c))
			}
			want := append([]string(nil), d.Capabilities...)
			sort.Strings(got)
			sort.Strings(want)

			if len(got) != len(want) {
				t.Fatalf("%s hydrates with capabilities %v; handWrittenDevices records %v", d.TypeKey, got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%s hydrates with capabilities %v; handWrittenDevices records %v", d.TypeKey, got, want)
				}
			}

			// Each conditional capability must be absent from the bare
			// record above and present once its property is set, so the
			// table cannot claim a condition the constructor does not have.
			for _, c := range d.Conditional {
				for _, name := range got {
					if name == c.Name {
						t.Fatalf("%s declares %s with no %s set, so it is not conditional", d.TypeKey, c.Name, c.Property)
					}
				}
				enabled, err := constructor(record.Record{
					Name: d.TypeKey, Type: d.TypeKey,
					Properties: map[string]inventory.PropertyValue{c.Property: c.Value},
				})
				if err != nil {
					t.Fatalf("hydrating %s with %s set: %v", d.TypeKey, c.Property, err)
				}
				found := false
				for _, name := range enabled.Capabilities() {
					found = found || string(name) == c.Name
				}
				if !found {
					t.Fatalf("%s does not declare %s when %s is %v", d.TypeKey, c.Name, c.Property, c.Value)
				}
			}
		})
	}
}

// TestPluginRowsMatchLiveRegistry is what makes plugins.md's own opening
// sentence checkable rather than merely stated: every registered plugin gets
// a row, and that row carries the registry's own description, not a second
// copy of it written somewhere else. The page used to describe static_yaml
// as reading "the project's own inventory.yaml" while the binary said "a
// static hosts.yaml inventory file", which is the drift this catches.
func TestPluginRowsMatchLiveRegistry(t *testing.T) {
	all := syncplugin.All()
	if len(all) == 0 {
		t.Fatal("no sync plugins are registered, which means internal/inventory/plugins was not linked in")
	}

	page, err := renderPlugins(syncplugin.Names())
	if err != nil {
		t.Fatalf("renderPlugins(): %v", err)
	}

	for name, desc := range all {
		if !strings.Contains(page, "`"+name+"`") {
			t.Errorf("plugin %q is registered but does not appear on the generated page", name)
		}
		if !strings.Contains(page, desc.Description) {
			t.Errorf("plugin %q renders a description the registry does not carry; the registry says %q", name, desc.Description)
		}
	}
	if !strings.Contains(page, itoa(len(all))+" sync plugins registered.") {
		t.Errorf("the generated page's count line does not report the %d registered plugins", len(all))
	}
}

// TestRenderPlugins_FailsClosed proves the two ways this page could be
// generated wrong are refusals rather than a quietly incomplete table. An
// empty registry means the plugins composition root was not linked in, and a
// name the registry cannot resolve means it changed mid-run; either one must
// stop the generator, because both would otherwise ship a page that tells a
// reader this product has no sync plugins.
func TestRenderPlugins_FailsClosed(t *testing.T) {
	if _, err := renderPlugins(nil); err == nil {
		t.Error("renderPlugins(nil) returned no error; an empty registry must fail the run")
	}
	if _, err := renderPlugins([]string{"not_a_registered_plugin"}); err == nil {
		t.Error("renderPlugins() accepted an unregistered name; an unresolvable name must fail the run")
	}
}

// TestFragmentsAreComplete proves every registered Fragment carries fully
// typed and described Params: a Fragment is only ever rendered inline
// under an implemented method's own Parameters table (writeParameters), so
// an incomplete Fragment would silently produce an incomplete-looking row
// with no test catching it.
func TestFragmentsAreComplete(t *testing.T) {
	for name, frag := range catalogdata.Fragments {
		for i, p := range frag.Params {
			if p.Type == "" {
				t.Errorf("fragment %q: param %q (index %d) has no Type", name, p.Name, i)
			}
			if p.Description == "" {
				t.Errorf("fragment %q: param %q (index %d) has no Description", name, p.Name, i)
			}
		}
	}
}

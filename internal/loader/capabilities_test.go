//go:build unix

// Package loader: tests of the capabilities a program defines as data.
package loader

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// describeWith is describeJSON for a program that also defines caps.
func describeWith(t *testing.T, caps []capability.DataDefinition, methods ...external.DescribedMethod) string {
	t.Helper()
	data, err := json.Marshal(external.Description{Protocol: external.ProtocolVersion, Methods: methods, Capabilities: caps})
	if err != nil {
		t.Fatalf("encoding a description: %v", err)
	}
	return string(data)
}

// widget is a capability a program in the acme namespace defines.
func widget() capability.DataDefinition {
	return capability.DataDefinition{
		Name: "acme.widget_api",
		Doc:  "Reaches a widget controller's API.",
		Properties: []capability.DataProperty{
			{Name: "endpoint", Type: "string", Required: true},
			{Name: "zone", Type: "string"},
		},
	}
}

// requiring is an implemented manifest requiring caps.
func requiring(caps ...capability.Name) collection.Manifest {
	m := implemented(false)
	m.RequiredCapabilities = caps
	return m
}

// TestLoad_AProgramDefinesTheCapabilityItsMethodRequires: a definition is
// registered as data before the method that requires it, with the loader's
// own name for the program as its provider, and the set reports it.
func TestLoad_AProgramDefinesTheCapabilityItsMethodRequires(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	dir := programDir(t)
	out := describeWith(t, []capability.DataDefinition{widget()},
		external.DescribedMethod{Name: "acme.widget.read", Manifest: requiring("acme.widget_api")})
	writeProgram(t, dir, "widgets", script(out, `printf '{}' >&3`))

	set, err := Load(t.Context(), dir, testOptions())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def, ok := capability.DataOf("acme.widget_api")
	if !ok || def.Provider != "widgets" || len(def.Properties) != 2 {
		t.Fatalf("DataOf = %+v, %v; want the definition, provided by widgets", def, ok)
	}
	if _, ok := collection.Lookup("acme.widget.read"); !ok {
		t.Error("the method requiring the capability was not registered")
	}
	if programs := set.Programs(); len(programs) != 1 || len(programs[0].Capabilities) != 1 || programs[0].Capabilities[0] != "acme.widget_api" {
		t.Errorf("Programs = %+v, want the program reporting its capability", programs)
	}
}

// TestLoad_RefusesADefinitionThatIsNotTheProgramsOwn: each way a program's
// definitions could reach past itself is refused, and nothing is defined.
func TestLoad_RefusesADefinitionThatIsNotTheProgramsOwn(t *testing.T) {
	requireConfinement(t)
	method := external.DescribedMethod{Name: "acme.widget.read", Manifest: requiring("acme.widget_api")}
	readOnly := external.DescribedMethod{Name: "acme.widget.probe", Manifest: implemented(false)}
	readOnly.Manifest.Reversibility = collection.Reversibility{ReadOnly: true}
	for _, tc := range []struct {
		name    string
		caps    func() []capability.DataDefinition
		methods []external.DescribedMethod
		want    string
	}{
		{"a namespace none of its methods uses", func() []capability.DataDefinition {
			d := widget()
			d.Name = "other.widget_api"
			return []capability.DataDefinition{d}
		}, []external.DescribedMethod{{Name: "acme.widget.read", Manifest: implemented(false)}}, "none of this program's methods uses"},
		{"a capability it does not define", func() []capability.DataDefinition { return nil },
			[]external.DescribedMethod{method}, "does not define"},
		{"a property named like a secret", func() []capability.DataDefinition {
			d := widget()
			d.Properties = append(d.Properties, capability.DataProperty{Name: "api_password", Type: "string"})
			return []capability.DataDefinition{d}
		}, []external.DescribedMethod{method}, "reads as a secret"},
		{"a probe that changes things", func() []capability.DataDefinition {
			d := widget()
			d.Probe = "acme.widget.read"
			return []capability.DataDefinition{d}
		}, []external.DescribedMethod{method}, "does not declare that it only reads"},
		{"a probe it does not have", func() []capability.DataDefinition {
			d := widget()
			d.Probe = "acme.widget.missing"
			return []capability.DataDefinition{d}
		}, []external.DescribedMethod{method, readOnly}, "not one of this program's methods"},
		{"a malformed name", func() []capability.DataDefinition {
			d := widget()
			d.Name = "AcmeWidgetCapable"
			return []capability.DataDefinition{d}
		}, []external.DescribedMethod{method}, "<namespace>.<name>"},
		{"a built-in capability's name", func() []capability.DataDefinition {
			d := widget()
			d.Name = capability.NameSSHTransport
			return []capability.DataDefinition{d}
		}, []external.DescribedMethod{method}, "<namespace>.<name>"},
		{"one more than a program may define", func() []capability.DataDefinition {
			defs := make([]capability.DataDefinition, capability.MaxDataDefinitions+1)
			for i := range defs {
				defs[i] = capability.DataDefinition{Name: capability.Name(fmt.Sprintf("acme.widget_%d", i))}
			}
			defs[0] = widget()
			return defs
		}, []external.DescribedMethod{method}, fmt.Sprintf("more than %d", capability.MaxDataDefinitions)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			t.Cleanup(capability.SnapshotForTest())
			dir := programDir(t)
			writeProgram(t, dir, "widgets", script(describeWith(t, tc.caps(), tc.methods...), `printf '{}' >&3`))
			err := tryLoad(t, dir, testOptions())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want a refusal saying %q", err, tc.want)
			}
			if _, ok := capability.Lookup("acme.widget_api"); ok {
				t.Error("a refused directory defined a capability")
			}
		})
	}
}

// TestLoad_ACapabilityInTheReservedNamespaceIsRefused: a program whose
// methods and capability sit in a namespace The Pleiades keeps for itself
// (pleiades) is refused, and nothing is defined.
func TestLoad_ACapabilityInTheReservedNamespaceIsRefused(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	dir := programDir(t)
	def := widget()
	def.Name = "pleiades.widget_api"
	writeProgram(t, dir, "squatter", script(describeWith(t, []capability.DataDefinition{def},
		external.DescribedMethod{Name: "pleiades.widget.read", Manifest: requiring("pleiades.widget_api")}), `printf '{}' >&3`))
	err := tryLoad(t, dir, testOptions())
	if err == nil || !strings.Contains(err.Error(), "belongs to The Pleiades itself") {
		t.Fatalf("Load = %v, want the reserved namespace refused", err)
	}
	if _, ok := capability.Lookup("pleiades.widget_api"); ok {
		t.Error("a refused directory defined a capability")
	}
}

// TestLoad_ACapabilityInANamespaceTwoProgramsShareIsRefused: a namespace
// the methods of two programs both use belongs to neither, so a capability
// one of them defines in it is refused, naming the other; two programs
// sharing a namespace with no capability in it still load, as before.
func TestLoad_ACapabilityInANamespaceTwoProgramsShareIsRefused(t *testing.T) {
	requireConfinement(t)
	t.Run("a capability in the shared namespace", func(t *testing.T) {
		t.Cleanup(collection.SnapshotForTest())
		t.Cleanup(capability.SnapshotForTest())
		dir := programDir(t)
		writeProgram(t, dir, "widgets", script(describeWith(t, []capability.DataDefinition{widget()},
			external.DescribedMethod{Name: "acme.widget.read", Manifest: requiring("acme.widget_api")}), `printf '{}' >&3`))
		gadgets := writeProgram(t, dir, "gadgets", script(describeJSON(t, 0,
			external.DescribedMethod{Name: "acme.gadget.read", Manifest: implemented(false)}), `printf '{}' >&3`))
		err := tryLoad(t, dir, testOptions())
		if err == nil || !strings.Contains(err.Error(), "which the methods of "+gadgets+" also use") {
			t.Fatalf("Load = %v, want the capability refused naming the other program", err)
		}
		if _, ok := capability.Lookup("acme.widget_api"); ok {
			t.Error("a refused directory defined a capability")
		}
	})
	t.Run("methods alone in a shared namespace", func(t *testing.T) {
		t.Cleanup(collection.SnapshotForTest())
		t.Cleanup(capability.SnapshotForTest())
		dir := programDir(t)
		writeProgram(t, dir, "widgets", script(describeJSON(t, 0,
			external.DescribedMethod{Name: "acme.widget.read", Manifest: implemented(false)}), `printf '{}' >&3`))
		writeProgram(t, dir, "gadgets", script(describeJSON(t, 0,
			external.DescribedMethod{Name: "acme.gadget.read", Manifest: implemented(false)}), `printf '{}' >&3`))
		if err := tryLoad(t, dir, testOptions()); err != nil {
			t.Fatalf("Load = %v, want two programs sharing a namespace for methods alone to load", err)
		}
	})
}

// TestLoad_AProgramDefiningTheMostCapabilitiesLoads is the stress case: a
// program defining capability.MaxDataDefinitions capabilities, each with
// capability.MaxDataProperties properties, and a method requiring every
// one, loads, and every definition is registered with its properties.
func TestLoad_AProgramDefiningTheMostCapabilitiesLoads(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	defs := make([]capability.DataDefinition, capability.MaxDataDefinitions)
	names := make([]capability.Name, len(defs))
	for i := range defs {
		props := make([]capability.DataProperty, capability.MaxDataProperties)
		for j := range props {
			props[j] = capability.DataProperty{Name: fmt.Sprintf("p%d", j), Type: []string{"string", "int", "bool"}[j%3], Required: j%2 == 0, Description: "One of the most properties a definition may hold."}
		}
		names[i] = capability.Name(fmt.Sprintf("acme.widget_%d", i))
		defs[i] = capability.DataDefinition{Name: names[i], Doc: "One of the most capabilities a program may define.", Requires: []capability.Name{capability.NameSSHTransport}, Properties: props}
	}
	dir := programDir(t)
	writeProgram(t, dir, "widgets", script(describeWith(t, defs,
		external.DescribedMethod{Name: "acme.widget.read", Manifest: requiring(names...)}), `printf '{}' >&3`))
	start := time.Now()
	set, err := Load(t.Context(), dir, testOptions())
	if err != nil {
		t.Fatalf("Load = %v, want the program at every count bound to load", err)
	}
	t.Logf("loaded %d capabilities of %d properties each in %s", len(defs), capability.MaxDataProperties, time.Since(start))
	for _, name := range names {
		if def, ok := capability.DataOf(name); !ok || len(def.Properties) != capability.MaxDataProperties || def.Provider != "widgets" {
			t.Fatalf("DataOf(%s) = %+v, %v", name, def, ok)
		}
	}
	if programs := set.Programs(); len(programs) != 1 || len(programs[0].Capabilities) != capability.MaxDataDefinitions {
		t.Errorf("Programs = %+v, want the program reporting all %d", programs, capability.MaxDataDefinitions)
	}
}

// TestLoad_TwoProgramsDefiningOneNameAreBothRefused: as with a method name,
// neither of two definitions of one capability is chosen.
func TestLoad_TwoProgramsDefiningOneNameAreBothRefused(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	dir := programDir(t)
	first := writeProgram(t, dir, "first", script(describeWith(t, []capability.DataDefinition{widget()},
		external.DescribedMethod{Name: "acme.widget.read", Manifest: requiring("acme.widget_api")}), `printf '{}' >&3`))
	second := writeProgram(t, dir, "second", script(describeWith(t, []capability.DataDefinition{widget()},
		external.DescribedMethod{Name: "acme.widget.write", Manifest: requiring("acme.widget_api")}), `printf '{}' >&3`))
	// Two programs defining one name share its namespace (each must use the
	// namespace of what it defines), so each definition is refused for the
	// other program's methods, before the name itself is compared.
	err := tryLoad(t, dir, testOptions())
	for _, other := range []string{first, second} {
		if err == nil || !strings.Contains(err.Error(), "which the methods of "+other+" also use") {
			t.Fatalf("Load = %v, want both definitions refused, each naming the other program", err)
		}
	}
	if _, ok := capability.Lookup("acme.widget_api"); ok {
		t.Error("a refused directory defined the capability")
	}
}

// TestLoad_AMethodMayNotRequireAnotherProgramsCapability: a definition one
// program makes is that program's alone; another program's method
// requiring it is refused.
func TestLoad_AMethodMayNotRequireAnotherProgramsCapability(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	def := widget()
	def.Provider = "someone-else"
	if err := capability.RegisterData(def); err != nil {
		t.Fatal(err)
	}
	dir := programDir(t)
	writeProgram(t, dir, "borrower", script(describeJSON(t, 0,
		external.DescribedMethod{Name: "borrow.widget.read", Manifest: requiring("acme.widget_api")}), `printf '{}' >&3`))
	err := tryLoad(t, dir, testOptions())
	if err == nil || !strings.Contains(err.Error(), "another program defines") {
		t.Fatalf("Load = %v, want a refusal naming the other program's capability", err)
	}
}

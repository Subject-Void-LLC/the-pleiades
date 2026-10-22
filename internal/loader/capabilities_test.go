//go:build unix

// Package loader: tests of the capabilities a program defines as data.
package loader

import (
	"encoding/json"
	"strings"
	"testing"

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

// TestLoad_TwoProgramsDefiningOneNameAreBothRefused: as with a method name,
// neither of two definitions of one capability is chosen.
func TestLoad_TwoProgramsDefiningOneNameAreBothRefused(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	dir := programDir(t)
	writeProgram(t, dir, "first", script(describeWith(t, []capability.DataDefinition{widget()},
		external.DescribedMethod{Name: "acme.widget.read", Manifest: requiring("acme.widget_api")}), `printf '{}' >&3`))
	writeProgram(t, dir, "second", script(describeWith(t, []capability.DataDefinition{widget()},
		external.DescribedMethod{Name: "acme.widget.write", Manifest: requiring("acme.widget_api")}), `printf '{}' >&3`))
	err := tryLoad(t, dir, testOptions())
	if err == nil || !strings.Contains(err.Error(), "is defined by both") {
		t.Fatalf("Load = %v, want both definitions refused", err)
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

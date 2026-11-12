// This file holds every built-in Collection method to the rule Phase
// 127a's decision rests on: a capability a program defines as data gates
// that program's own methods and nothing else, so no built-in manifest
// names one.
package archtest

import (
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // triggers every Collection package's init()

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// dataCapabilitiesRequired returns every capability m requires that has a
// data capability's shape (capability.IsDataName). Every capability a
// program defines has that shape (capability.ValidateData refuses any
// other), and no compiled one can (TestIsDataName_NeverMatchesACompiledName
// in pkg/capability), so a manifest returning nothing here requires no data
// capability, whichever programs are loaded.
func dataCapabilitiesRequired(m collection.Manifest) []capability.Name {
	var out []capability.Name
	for _, name := range m.RequiredCapabilities {
		if capability.IsDataName(name) {
			out = append(out, name)
		}
	}
	return out
}

// TestNoBuiltinManifestNamesADataCapability: a grant of a data capability
// is checked only against its own program's definition, and only that
// program's methods may require it (the loader's rule). A built-in method
// requiring one would let a grant, made by any program's probe or any
// admin's assertion, gate code The Pleiades ships. Today collection.Register
// refuses one only because built-ins register before any program loads, so
// a name nothing defines yet is unknown; this sweep holds the rule
// whatever the order.
func TestNoBuiltinManifestNamesADataCapability(t *testing.T) {
	// The control: the predicate finds a data capability in a manifest
	// that names one beside a compiled one, so an empty answer below is
	// evidence rather than a misaimed question.
	control := collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameSSHTransport, "acme.widget_api"}}
	if got := dataCapabilitiesRequired(control); len(got) != 1 || got[0] != "acme.widget_api" {
		t.Fatalf("the predicate found %v in a manifest requiring acme.widget_api, so this sweep proves nothing", got)
	}

	checked := 0
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Provider != nil {
			continue
		}
		checked++
		for _, name := range dataCapabilitiesRequired(desc.Manifest) {
			t.Errorf("built-in method %s requires %s, a capability a program defines as data; a built-in may require only compiled capabilities", cfg.Name, name)
		}
	}
	if checked == 0 {
		t.Fatal("no catalogdata entry resolved in the registry, so this sweep examined nothing")
	}
}

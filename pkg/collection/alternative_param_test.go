// Tests that a call naming an alternative by a manifest's AlternativeParam is
// held to it alone, through the For every tier shapes a call with, and that
// Register refuses a parameter a call could not choose by.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// viaManifest is net.yang.schema's shape: NETCONF (NetconfCapable over the
// netconf transport) or RESTCONF (RestconfCapable over restconf), chosen by
// via.
func viaManifest() collection.Manifest {
	return collection.Manifest{
		Status:              collection.StatusDeclared,
		SupportedTransports: []string{capability.TransportNetconf, capability.TransportRestconf},
		Alternatives: []collection.Alternative{
			{Name: "netconf", SupportedTransports: []string{capability.TransportNetconf}, RequiredCapabilities: []capability.Name{capability.NameNetconf}},
			{Name: "restconf", SupportedTransports: []string{capability.TransportRestconf}, RequiredCapabilities: []capability.Name{capability.NameRestconf}},
		},
		AlternativeParam: "via",
		Doc: collection.Doc{Params: []collection.Param{
			{Name: "via", Type: "string", Default: "auto", Choices: []string{"auto", "netconf", "restconf"}, Description: "x"},
		}},
	}
}

// TestFor_HoldsACallToTheAlternativeItNames: a device serving both
// protocols takes NETCONF by the order the manifest declares, RESTCONF when
// the call names it, and a device serving only NETCONF is refused a call
// naming RESTCONF, by the transport check, naming the one alternative; auto,
// an absent value, a template and a word naming none leave the choice to the
// device.
func TestFor_HoldsACallToTheAlternativeItNames(t *testing.T) {
	m := viaManifest()
	both := stubWith(capability.NameNetconf, capability.NameRestconf)
	netconfOnly := stubWith(capability.NameNetconf)
	for _, tc := range []struct {
		params map[string]any
		want   string
	}{
		{nil, "netconf"},
		{map[string]any{"via": "auto"}, "netconf"},
		{map[string]any{"via": "restconf"}, "restconf"},
		{map[string]any{"via": "netconf"}, "netconf"},
		{map[string]any{"via": "{{ vars.via }}"}, "netconf"},
		{map[string]any{"via": 3}, "netconf"},
	} {
		shaped := m.For(tc.params)
		if alt, err := collection.ChooseAlternative(both, "net.yang.schema", shaped); err != nil || alt.Name != tc.want {
			t.Errorf("For(%v) chose %q (%v), want %q", tc.params, alt.Name, err, tc.want)
		}
	}
	shaped := m.For(map[string]any{"via": "restconf"})
	if len(shaped.Alternatives) != 1 || strings.Join(shaped.SupportedTransports, ",") != capability.TransportRestconf {
		t.Errorf("For(restconf) = %+v, want the one alternative and its transport", shaped)
	}
	if err := collection.Admits(netconfOnly, "net.yang.schema", shaped); err == nil || !strings.Contains(err.Error(), "restconf") {
		t.Errorf("a NETCONF-only device asked for RESTCONF: %v, want a refusal naming restconf", err)
	}
	if err := collection.Admits(netconfOnly, "net.yang.schema", m.For(map[string]any{"via": "netconf"})); err != nil {
		t.Errorf("a NETCONF device asked for NETCONF: %v", err)
	}
	// A manifest with no AlternativeParam is shaped by nothing a call names.
	plain := m
	plain.AlternativeParam = ""
	if got := plain.For(map[string]any{"via": "restconf", "": "restconf"}); len(got.Alternatives) != 2 {
		t.Errorf("a manifest choosing by no parameter was narrowed: %+v", got.Alternatives)
	}
}

// TestRegister_RefusesAnAlternativeParamNoCallCouldChooseBy: a parameter on
// a manifest with no alternatives, an undocumented one, one of another type,
// default or choices, one matching its choices without regard to case, and
// an alternative named auto are each refused; viaManifest registers.
func TestRegister_RefusesAnAlternativeParamNoCallCouldChooseBy(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	with := func(f func(*collection.Manifest)) collection.Manifest {
		m := viaManifest()
		f(&m)
		return m
	}
	for want, m := range map[string]collection.Manifest{
		"declares no alternatives": with(func(m *collection.Manifest) {
			m.Alternatives = nil
			m.SupportedTransports = nil
		}),
		"which it does not document": with(func(m *collection.Manifest) { m.AlternativeParam = "route" }),
		"must be a string parameter": with(func(m *collection.Manifest) { m.Doc.Params[0].Type = "list of string" }),
		"defaulting to auto":         with(func(m *collection.Manifest) { m.Doc.Params[0].Default = "netconf" }),
		"exactly the choices auto, netconf, restconf": with(func(m *collection.Manifest) {
			m.Doc.Params[0].Choices = []string{"auto", "netconf"}
		}),
		"matched exactly": with(func(m *collection.Manifest) { m.Doc.Params[0].ChoicesIgnoreCase = true }),
		"the word that leaves the choice": with(func(m *collection.Manifest) {
			m.Alternatives[1].Name = "auto"
			m.Doc.Params[0].Choices = []string{"auto", "netconf"}
		}),
	} {
		if err := collection.Register(collection.Descriptor{Name: "viatest.method", Manifest: m}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Register = %v", want, err)
		}
	}
	if err := collection.Register(collection.Descriptor{Name: "viatest.method", Manifest: viaManifest()}); err != nil {
		t.Errorf("viaManifest: %v", err)
	}
}

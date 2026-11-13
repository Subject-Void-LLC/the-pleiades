// Tests that a device is admitted when it fits one of a method's
// alternatives whole, that the choice is the first it fits, that the
// alternatives survive the module catalog a Controller admits by, and that
// Register refuses alternatives it could not choose between.
package collection_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// routerManifest is wait.routes's shape: an FRR router (FRRCapable and
// CommandExecCapable over SSH) or a Cisco IOS one (CiscoIOSCapable over
// SSH, Telnet or a console).
func routerManifest() collection.Manifest {
	return collection.Manifest{
		SupportedTransports: []string{capability.TransportSSH, capability.TransportTelnet, capability.TransportConsole},
		Alternatives: []collection.Alternative{
			{Name: "frr", SupportedTransports: []string{capability.TransportSSH}, RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec}},
			{Name: "ios", SupportedTransports: []string{capability.TransportSSH, capability.TransportTelnet, capability.TransportConsole}, RequiredCapabilities: []capability.Name{capability.NameCiscoIOS}},
		},
	}
}

// TestAdmits_HoldsADeviceToOneAlternativeWhole: a device fitting either
// kind is admitted and the first kind it fits is chosen; one holding half
// of a kind, or a kind's capabilities over another kind's transport, is
// refused naming both kinds; no device at all is refused; and a manifest
// with no alternatives chooses nothing and refuses nothing.
func TestAdmits_HoldsADeviceToOneAlternativeWhole(t *testing.T) {
	m := routerManifest()
	for _, tc := range []struct {
		name string
		caps []capability.Name
		want string
	}{
		{"an FRR router over SSH", []capability.Name{capability.NameFRR, capability.NameCommandExec, capability.NameSSHTransport}, "frr"},
		{"an IOS router over Telnet", []capability.Name{capability.NameCiscoIOS, capability.NameTelnet}, "ios"},
		{"both, over SSH", []capability.Name{capability.NameFRR, capability.NameCommandExec, capability.NameCiscoIOS, capability.NameSSHTransport}, "frr"},
		{"FRR without command execution", []capability.Name{capability.NameFRR, capability.NameSSHTransport}, ""},
		{"FRR through a console server", []capability.Name{capability.NameFRR, capability.NameCommandExec, capability.NameConsoleServed}, ""},
		{"a Linux server", []capability.Name{capability.NameLinux, capability.NameCommandExec, capability.NameSSHTransport}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev := stubWith(tc.caps...)
			err := collection.Admits(dev, "wait.routes", m)
			alt, chooseErr := collection.ChooseAlternative(dev, "wait.routes", m)
			if tc.want != "" {
				if err != nil || chooseErr != nil || alt.Name != tc.want {
					t.Fatalf("Admits = %v, chose %q (%v); want %q admitted", err, alt.Name, chooseErr, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), `runs on a device that is one of: frr (FRRCapable and CommandExecCapable, over ssh); ios (CiscoIOSCapable, over ssh, telnet, console), and device "dev" is none of them`) {
				t.Fatalf("Admits = %v, want a refusal naming both kinds", err)
			}
			var transport *collection.TransportError
			if errors.As(err, &transport) {
				t.Errorf("a device reaching ssh was refused as a transport: %v", err)
			}
		})
	}
	if err := collection.Admits(nil, "wait.routes", m); err == nil || !strings.Contains(err.Error(), "no target device") {
		t.Errorf("no device: %v", err)
	}
	if alt, err := collection.ChooseAlternative(stubWith(), "x.y", collection.Manifest{}); err != nil || alt.Name != "" {
		t.Errorf("a manifest with no alternatives chose %q: %v", alt.Name, err)
	}
	if err := collection.Admits(stubWith(), "x.y", collection.Manifest{}); err != nil {
		t.Errorf("a manifest requiring nothing refused a device: %v", err)
	}
}

// TestChooseAlternative_AKindWithNoTransportTakesAny: an alternative that
// declares no transport of its own is chosen by its capabilities alone,
// over whatever transport the device reaches.
func TestChooseAlternative_AKindWithNoTransportTakesAny(t *testing.T) {
	m := collection.Manifest{Alternatives: []collection.Alternative{
		{Name: "ios", RequiredCapabilities: []capability.Name{capability.NameCiscoIOS}},
		{Name: "frr", RequiredCapabilities: []capability.Name{capability.NameFRR}},
	}}
	alt, err := collection.ChooseAlternative(stubWith(capability.NameFRR, capability.NameTelnet), "x.y", m)
	if err != nil || alt.Name != "frr" {
		t.Fatalf("chose %q (%v), want frr over any transport", alt.Name, err)
	}
}

// TestAdmits_ATransportRefusalIsATransportError: a device reaching none of
// the union's transports is refused by CheckTransports first, as a
// *TransportError, which is how validate tells the transport rule's
// refusals from the capability rule's.
func TestAdmits_ATransportRefusalIsATransportError(t *testing.T) {
	err := collection.Admits(stubWith(capability.NameCiscoIOS, capability.NameWinRM), "wait.routes", routerManifest())
	var transport *collection.TransportError
	if !errors.As(err, &transport) || !strings.Contains(err.Error(), "reaches its device over ssh or telnet or console") {
		t.Fatalf("Admits = %v, want a transport refusal", err)
	}
}

// TestAdmitsAnyShape_HoldsAlternatives: a Controller's admission, which
// asks without a call's parameters, refuses a device of neither kind.
func TestAdmitsAnyShape_HoldsAlternatives(t *testing.T) {
	m := routerManifest()
	if err := collection.AdmitsAnyShape(stubWith(capability.NameCiscoIOS, capability.NameConsoleServed), "wait.routes", m); err != nil {
		t.Errorf("an IOS router: %v", err)
	}
	if err := collection.AdmitsAnyShape(stubWith(capability.NameLinux, capability.NameSSHTransport), "wait.routes", m); err == nil {
		t.Error("a Linux server was admitted")
	}
	if err := collection.CheckAnyShape(stubWith(capability.NameLinux, capability.NameSSHTransport), "wait.routes", m); err == nil {
		t.Error("the deprecated name admitted a Linux server")
	}
}

// TestAlternatives_SurviveTheModuleCatalog: a manifest's alternatives are
// in its JSON, the document a Controller installs as its module catalog
// and admits devices by, so admission there holds them as a registry does.
func TestAlternatives_SurviveTheModuleCatalog(t *testing.T) {
	t.Cleanup(collection.CatalogForTest())
	data, err := json.Marshal(map[string]collection.Manifest{"router.read": routerManifest()})
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(data); err != nil {
		t.Fatal(err)
	}
	m, ok := collection.ManifestOf("router.read")
	if !ok || len(m.Alternatives) != 2 {
		t.Fatalf("ManifestOf = %+v, %v; want both alternatives", m, ok)
	}
	if err := collection.AdmitsAnyShape(stubWith(capability.NameLinux, capability.NameSSHTransport), "router.read", m); err == nil {
		t.Error("the catalog's manifest admitted a Linux server")
	}
}

// TestManifest_RequiresAndNamedCapabilities: Requires holds a capability
// every call needs, from the manifest's own list or from every alternative,
// and NamedCapabilities lists every capability any part names, once.
func TestManifest_RequiresAndNamedCapabilities(t *testing.T) {
	m := routerManifest()
	if m.Requires(capability.NameFRR) || m.Requires(capability.NameCiscoIOS) {
		t.Error("a capability one alternative needs reads as needed by every call")
	}
	both := m
	both.Alternatives = []collection.Alternative{
		{Name: "a", RequiredCapabilities: []capability.Name{capability.NameDocker, capability.NameEngineLocal}},
		{Name: "b", RequiredCapabilities: []capability.Name{capability.NameDocker}},
	}
	if !both.Requires(capability.NameDocker) {
		t.Error("a capability every alternative needs does not read as required")
	}
	if !(collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameLinux}}).Requires(capability.NameLinux) {
		t.Error("the manifest's own capability does not read as required")
	}
	if (collection.Manifest{}).Requires(capability.NameLinux) {
		t.Error("an empty manifest requires something")
	}
	shaped := shapedManifest()
	shaped.RequiredCapabilities = append(shaped.RequiredCapabilities, capability.NameFRR)
	named := append(shaped.NamedCapabilities(), m.NamedCapabilities()...)
	for _, want := range []capability.Name{capability.NameLinux, capability.NameContainerEngine, capability.NameFRR, capability.NameCommandExec, capability.NameCiscoIOS} {
		if !slices.Contains(named, want) {
			t.Errorf("NamedCapabilities leaves out %s: %v", want, named)
		}
	}
	if got := m.NamedCapabilities(); len(got) != 3 {
		t.Errorf("NamedCapabilities = %v, want each of the three once", got)
	}
}

// TestRegister_RefusesAlternativesItCannotChooseBetween: alternatives beside
// shapes, one alone, an unnamed, mis-named or repeated one, one with no
// capability, an unknown capability or transport, a transport outside the
// manifest's, and a manifest transport none takes are each refused; the
// router shape registers.
func TestRegister_RefusesAlternativesItCannotChooseBetween(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	ssh := []string{capability.TransportSSH}
	frr := collection.Alternative{Name: "frr", SupportedTransports: ssh, RequiredCapabilities: []capability.Name{capability.NameFRR}}
	ios := collection.Alternative{Name: "ios", SupportedTransports: ssh, RequiredCapabilities: []capability.Name{capability.NameCiscoIOS}}
	with := func(f func(*collection.Manifest)) collection.Manifest {
		m := collection.Manifest{Status: collection.StatusDeclared, SupportedTransports: ssh, Alternatives: []collection.Alternative{frr, ios}}
		f(&m)
		return m
	}
	for want, m := range map[string]collection.Manifest{
		"both call shapes and alternatives": with(func(m *collection.Manifest) {
			m.Shapes = []collection.CallShape{{Param: "container", RequiredCapabilities: []capability.Name{capability.NameContainerEngine}}}
			m.Doc = collection.Doc{Params: []collection.Param{{Name: "container", Type: "string", Description: "x"}}}
		}),
		"declares one alternative":  with(func(m *collection.Manifest) { m.Alternatives = m.Alternatives[:1] }),
		"an alternative named \"\"": with(func(m *collection.Manifest) { m.Alternatives[0].Name = "" }),
		"an alternative named \"FRR\"": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{{Name: "FRR", SupportedTransports: ssh, RequiredCapabilities: frr.RequiredCapabilities}, ios}
		}),
		"two alternatives named": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{frr, {Name: "frr", SupportedTransports: ssh, RequiredCapabilities: ios.RequiredCapabilities}}
		}),
		"requires no capability": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{frr, {Name: "ios", SupportedTransports: ssh}}
		}),
		"requires unknown capability": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{frr, {Name: "ios", SupportedTransports: ssh, RequiredCapabilities: []capability.Name{"NoSuchCapable"}}}
		}),
		"which is not one this platform": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{frr, {Name: "ios", SupportedTransports: []string{"carrier-pigeon"}, RequiredCapabilities: ios.RequiredCapabilities}}
		}),
		"declares no transport, and the method": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{frr, {Name: "ios", RequiredCapabilities: ios.RequiredCapabilities}}
		}),
		"own transports do not list": with(func(m *collection.Manifest) {
			m.Alternatives = []collection.Alternative{frr, {Name: "ios", SupportedTransports: []string{capability.TransportTelnet}, RequiredCapabilities: ios.RequiredCapabilities}}
		}),
		"none of its alternatives takes": with(func(m *collection.Manifest) {
			m.SupportedTransports = []string{capability.TransportSSH, capability.TransportTelnet}
		}),
	} {
		if err := collection.Register(collection.Descriptor{Name: "alttest.method", Manifest: m}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Register = %v", want, err)
		}
	}
	good := routerManifest()
	good.Status = collection.StatusDeclared
	if err := collection.Register(collection.Descriptor{Name: "alttest.method", Manifest: good}); err != nil {
		t.Errorf("the router shape: %v", err)
	}
}

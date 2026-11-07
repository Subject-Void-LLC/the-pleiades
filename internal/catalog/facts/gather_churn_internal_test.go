// Tests of what keeps a stable fact from changing on a device nobody changed
// (sweep 2026-11-04, items 6 and 10): a service's state is its own volatile
// fact, paired with its service by key, and a Windows per-user service is
// reported by its template, which has no state, never by each sign-in's
// instance.
package facts

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestGatherFacts_AServicesStateIsVolatileAndTheRestStable(t *testing.T) {
	d, ok := collection.Lookup("facts.gather")
	if !ok {
		t.Fatal("facts.gather is not registered")
	}
	cases := []struct {
		key  string
		kind collection.FactKind
		typ  collection.FactType
	}{
		{"service.sshd.service", collection.FactStable, collection.FactObject},
		{"service_state.sshd.service", collection.FactVolatile, collection.FactString},
		{gatherFactFamily, collection.FactStable, collection.FactString},
		{gatherFactSwapMB, collection.FactStable, collection.FactInteger},
	}
	for _, tc := range cases {
		spec, ok := collection.FactFor(d.Manifest.Facts, tc.key)
		if !ok || spec.Kind != tc.kind || spec.Type != tc.typ {
			t.Errorf("%s is declared %+v (%v), want %s %s", tc.key, spec, ok, tc.kind, tc.typ)
		}
	}
}

func TestFamilyItems_SplitsAServicesStateUnderItsOwnKey(t *testing.T) {
	names := gatherNames{}
	sshd := gatherFact{key: "service.sshd", value: map[string]any{"state": "running", "enabled": true, "status": "enabled", "source": "systemd"}}
	got := familyItems(names, sshd)
	want := []gatherFact{
		{key: "service.sshd", value: map[string]any{"enabled": true, "status": "enabled", "source": "systemd"}},
		{key: "service_state.sshd", value: "running"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
	// A second service of the same name is told apart, and its state goes
	// with it under the same suffix.
	other := gatherFact{key: "service.sshd", value: map[string]any{"state": "stopped", "enabled": false, "status": "disabled", "source": "openrc"}}
	got = familyItems(names, other)
	if len(got) != 2 || got[0].key != "service.sshd@openrc" || got[1].key != "service_state.sshd@openrc" || got[1].value != "stopped" {
		t.Errorf("a second sshd: %v", got)
	}
	// A package is not split, and a service with no state yields no state.
	pkg := gatherFact{key: "package.bash", value: map[string]any{"version": "5", "state": "x"}}
	if got := familyItems(names, pkg); !reflect.DeepEqual(got, []gatherFact{pkg}) {
		t.Errorf("a package: %v", got)
	}
	template := gatherFact{key: "service.WpnUserService", value: map[string]any{"enabled": true, "status": "auto", "source": "scm", "per_user": true}}
	if got := familyItems(names, template); len(got) != 1 || got[0].key != "service.WpnUserService" {
		t.Errorf("a per-user template: %v", got)
	}
}

func TestParseWindowsServices_ATemplateHasNoState(t *testing.T) {
	// The services script's answer on win-check while a user was signed in
	// (2026-11-06), with the instance the script now leaves out already gone
	// and its template read from the registry.
	out := `[{"name":"WinRM","state":"Running","mode":"Auto","delayed":false},` +
		`{"name":"KeyIso","state":"Stopped","mode":"Manual","delayed":false},` +
		`{"name":"WpnUserService","mode":"Auto","delayed":false,"per_user":true},` +
		`{"name":"DeviceAssociationBrokerSvc","mode":"Manual","delayed":false,"per_user":true}]`
	got := valuesOf(parseWindowsServices(out))
	want := map[string]any{
		"service.WinRM":                      map[string]any{"state": "running", "enabled": true, "status": "auto", "source": "scm"},
		"service.KeyIso":                     map[string]any{"state": "stopped", "enabled": false, "status": "manual", "source": "scm"},
		"service.WpnUserService":             map[string]any{"enabled": true, "status": "auto", "source": "scm", "per_user": true},
		"service.DeviceAssociationBrokerSvc": map[string]any{"enabled": false, "status": "manual", "source": "scm", "per_user": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v\nwant %v", got, want)
	}
}

func TestWindowsProbes_ReportTheNativeFamilyAndNoSwap(t *testing.T) {
	var min, hardware gatherProbe
	for _, p := range windowsProbes() {
		switch p.subset {
		case gatherSubsetMin:
			min = p
		case gatherSubsetHardware:
			hardware = p
		}
	}
	// Windows has no swap natively (a page file is not swap); Ansible's 0
	// there is pkg/ansiblefacts' to give.
	if !gatherWanted(min.keys, gatherFactFamily) || gatherWanted(hardware.keys, gatherFactSwapMB) {
		t.Fatalf("min keys %v, hardware keys %v", min.keys, hardware.keys)
	}
	// The scripts' objects in the shape the host prints them.
	if got := valuesOf(parseWindowsMin(`{"host.name":"WIN-CHECK","os.family":"windows","os.version":"10.0.26100.0"}`)); got[gatherFactFamily] != "windows" {
		t.Errorf("min: %v", got)
	}
	if got := valuesOf(parseWindowsObject(windowsHardwareSample)); len(got) != 1 || got[gatherFactMemoryMB] != 4095 {
		t.Errorf("hardware: %v, want memory alone", got)
	}
}

// windowsHardwareSample is the hardware script's object with only memory,
// as a host whose CIM answers nothing else prints it.
const windowsHardwareSample = `{"hardware.memory_mb":4095,"hardware.cpu.sockets":null}`

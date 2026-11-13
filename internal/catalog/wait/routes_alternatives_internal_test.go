// Package wait: wait.routes's alternatives, held to the reader it opens.
//
// The manifest's alternatives are what validate, a Controller's admission
// and the engine admit a device by (collection.Admits); chooseRouteKind is
// what the method reads the device's table with. These tests hold the two
// to one answer over capability and transport sets, so a reader added
// without its alternative, or an alternative whose capabilities differ from
// what the reader needs, fails here.
package wait

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestRouteKinds_AdmissionEqualsTheReaderChosen: for every capability and
// transport set, plan-time admission (collection.Admits over the registered
// manifest, the check validate, admission and the engine make) admits a
// device exactly when the method chooses a reader for it, and the reader it
// chooses is the kind each case names: an FRR router needs FRRCapable and
// CommandExecCapable over SSH, a Cisco IOS one CiscoIOSCapable over SSH,
// Telnet or a console, and a device that is both is read as FRR.
func TestRouteKinds_AdmissionEqualsTheReaderChosen(t *testing.T) {
	registered, ok := collection.Lookup(routesFQCN)
	if !ok {
		t.Fatal("wait.routes is not registered")
	}
	ssh, telnet, console := capability.NameSSHTransport, capability.NameTelnet, capability.NameConsoleServed
	frr, exec, ios := capability.NameFRR, capability.NameCommandExec, capability.NameCiscoIOS
	for _, tc := range []struct {
		name string
		caps []capability.Name
		want string // the kind read, or "" for a device refused
	}{
		{"FRR over SSH", []capability.Name{frr, exec, ssh}, "frr"},
		{"FRR without command execution", []capability.Name{frr, ssh}, ""},
		{"FRR reached only by a console", []capability.Name{frr, exec, console}, ""},
		{"FRR reached only by Telnet", []capability.Name{frr, exec, telnet}, ""},
		{"FRR reached by nothing", []capability.Name{frr, exec}, ""},
		{"IOS over SSH", []capability.Name{ios, ssh}, "ios"},
		{"IOS over Telnet", []capability.Name{ios, telnet}, "ios"},
		{"IOS through a console server", []capability.Name{ios, console}, "ios"},
		{"IOS reached by nothing", []capability.Name{ios}, ""},
		{"both kinds over SSH", []capability.Name{frr, exec, ios, ssh}, "frr"},
		{"FRR's capabilities, IOS's console", []capability.Name{frr, exec, ios, console}, "ios"},
		{"a Linux server", []capability.Name{capability.NameLinux, exec, ssh}, ""},
		{"nothing at all", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := &inventorytest.Stub{StubName: "r1", Caps: tc.caps}
			admitted := collection.Admits(device, routesFQCN, registered.Manifest)
			kind, chosen := chooseRouteKind(device)
			if (admitted == nil) != (chosen == nil) {
				t.Fatalf("admission says %v and the method says %v: the two disagree", admitted, chosen)
			}
			got := ""
			if chosen == nil {
				got = kind.alt.Name
			}
			if got != tc.want {
				t.Fatalf("read as %q (admission %v), want %q", got, admitted, tc.want)
			}
			// A device reaching none of the union's transports is refused for
			// that first; any other is refused naming both kinds.
			var transport *collection.TransportError
			if tc.want == "" && !errors.As(admitted, &transport) &&
				!strings.Contains(admitted.Error(), "frr (FRRCapable and CommandExecCapable, over ssh); ios (CiscoIOSCapable, over ssh, telnet, console)") {
				t.Errorf("the refusal does not name both kinds: %v", admitted)
			}
		})
	}
}

// TestRouteKinds_EveryKindIsAnAlternative: the registered manifest's
// alternatives are routeKinds' own, in order, and each kind's open is set,
// so no reader exists that admission does not know and no alternative is
// admitted that nothing reads.
func TestRouteKinds_EveryKindIsAnAlternative(t *testing.T) {
	registered, _ := collection.Lookup(routesFQCN)
	alts := registered.Manifest.Alternatives
	if len(alts) != len(routeKinds) || len(alts) < 2 {
		t.Fatalf("the manifest declares %d alternatives and the method reads %d kinds", len(alts), len(routeKinds))
	}
	for i, k := range routeKinds {
		if alts[i].Name != k.alt.Name || k.open == nil {
			t.Errorf("alternative %d is %q, the kind is %q (open set: %t)", i, alts[i].Name, k.alt.Name, k.open != nil)
		}
	}
}

// TestRouteReader_RechecksAGrantThatLapsed: a device admitted as an FRR
// router whose FRRCapable grant lapsed before the task ran (Phase 127a's
// case 10) is refused by the method itself before anything is opened,
// since the run-time check is the one admission cannot make for it.
func TestRouteReader_RechecksAGrantThatLapsed(t *testing.T) {
	admitted := &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameFRR, capability.NameCommandExec, capability.NameSSHTransport}}
	registered, _ := collection.Lookup(routesFQCN)
	if err := collection.Admits(admitted, routesFQCN, registered.Manifest); err != nil {
		t.Fatalf("the device was not admitted to begin with: %v", err)
	}
	lapsed := &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameCommandExec, capability.NameSSHTransport}}
	read, closer, err := routeReader(context.Background(), emptyRC{}, lapsed, nil)
	if err == nil || read != nil || closer != nil || !strings.Contains(err.Error(), `device "r1" is none of them`) {
		t.Fatalf("a lapsed grant: read %v, close %v, err %v; want a refusal before anything opened", read != nil, closer != nil, err)
	}
}

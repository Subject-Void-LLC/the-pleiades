// Tests net.batfish.check on a DMVPN hub and spoke whose WAN interfaces are
// in a front-door VRF, the design the blind CVD lab built: NHRP is refused
// until accepted by name (and the mGRE tunnel Batfish cannot place), a reach from an interface in a VRF is the router
// sending from it (a packet entering it is a crash in Batfish, named as
// such), a quoted interface is read as Batfish writes it, and a route is
// asserted in one VRF apart from another.
package batfish_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/batfish"
	bfdevice "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// dmvpnWAN is the transport between the two sites, in its global table.
const dmvpnWAN = "hostname wan1\n!\ninterface Ethernet0/0\n ip address 172.16.0.2 255.255.255.252\n no shutdown\n!\n" +
	"interface Ethernet0/1\n ip address 172.16.1.2 255.255.255.252\n no shutdown\n!\nend\n"

// dmvpnRouter is a DMVPN hub or spoke: its WAN interface in the front-door
// VRF INET with a default route there, and Tunnel100 in the global table.
func dmvpnRouter(name, wan, peer, tunnel string, hub bool) string {
	c := "hostname " + name + "\n!\nvrf definition INET\n address-family ipv4\n exit-address-family\n!\n" +
		"interface Ethernet0/1\n vrf forwarding INET\n ip address " + wan + " 255.255.255.252\n no shutdown\n!\n" +
		"interface Tunnel100\n ip address " + tunnel + " 255.255.255.0\n ip nhrp network-id 100\n"
	if hub {
		c += " ip nhrp redirect\n"
	} else {
		c += " ip nhrp nhs 10.255.0.1 nbma 172.16.0.1 multicast\n ip nhrp shortcut\n"
	}
	return c + " tunnel source Ethernet0/1\n tunnel mode gre multipoint\n!\n" +
		"ip route vrf INET 0.0.0.0 0.0.0.0 " + peer + "\n!\nend\n"
}

func TestCheck_DMVPNInAFrontDoorVRF(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	svc, err := bfdevice.NewService(record.Record{Name: "batfish", Type: bfdevice.TypeService, Properties: map[string]inventory.PropertyValue{
		bfdevice.APIBaseProperty: strings.Replace(testsupport.StartBatfish(t), "localhost", "127.0.0.1", 1),
	}})
	if err != nil {
		t.Fatal(err)
	}
	devices := []any{
		map[string]any{"name": "wan1", "format": "cisco_ios", "config": dmvpnWAN},
		map[string]any{"name": "hub", "format": "cisco_ios", "config": dmvpnRouter("hub", "172.16.0.1", "172.16.0.2", "10.255.0.1", true)},
		map[string]any{"name": "spoke", "format": "cisco_ios", "config": dmvpnRouter("spoke", "172.16.1.1", "172.16.1.2", "10.255.0.11", false)},
	}
	accepted := []any{
		map[string]any{"feature": "nhrp", "reason": "the overlay is proven live"},
		map[string]any{"text": "mode gre multipoint", "reason": "the overlay is proven live"},
		map[string]any{"text": "Could not determine src/dst IPs for tunnel Tunnel100", "reason": "an mGRE tunnel has no one destination"},
	}
	check := func(assertions []any, accept []any) (*statContext, error) {
		rc := &statContext{stats: map[string]any{}}
		_, err := batfish.Check(context.Background(), rc, svc, map[string]any{"devices": devices, "assertions": assertions, "accept_unrecognized": accept})
		return rc, err
	}
	reach := func(from string) []any {
		return []any{map[string]any{"reach": map[string]any{"from": from, "dst": "172.16.0.1", "accepted_by": "hub"}}}
	}

	if _, err := check(reach("spoke[Ethernet0/1]"), nil); err == nil || !strings.Contains(err.Error(), "uses nhrp") {
		t.Fatalf("NHRP unaccepted: %v", err)
	}
	for _, from := range []string{"spoke[Ethernet0/1]", `spoke["Ethernet0/1"]`} {
		if rc, err := check(reach(from), accepted); err != nil {
			t.Errorf("the spoke's WAN address reaching the hub's, from %s: %v\n%v", from, err, rc.stats["assertions"])
		}
	}
	if _, err := check(reach("@enter(spoke[Ethernet0/1])"), accepted); err == nil || !strings.Contains(err.Error(), `ask from spoke["Ethernet0/1"]`) {
		t.Errorf("a packet entering an interface in a VRF: %v", err)
	}

	route := func(kind, vrf string) map[string]any {
		a := map[string]any{"node": "spoke", "prefix": "0.0.0.0/0"}
		if vrf != "" {
			a["vrf"] = vrf
		}
		return map[string]any{kind: a}
	}
	if rc, err := check([]any{route("route", "INET"), route("no_route", "default")}, accepted); err != nil {
		t.Errorf("the default route in INET and not in default: %v\n%v", err, rc.stats["assertions"])
	}
	if _, err := check([]any{route("no_route", "")}, accepted); err == nil || !strings.Contains(err.Error(), "spoke routes 0.0.0.0/0") {
		t.Errorf("no_route across every VRF holds the front door's default route: %v", err)
	}
	if _, err := check([]any{route("route", "default")}, accepted); err == nil || !strings.Contains(err.Error(), "(VRF default) has no route") {
		t.Errorf("a route in the default VRF that is only in INET: %v", err)
	}
}

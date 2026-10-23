// Tests that net.batfish.check reads Cisco IOS configurations as they are
// and predicts a first-hop redundancy failover: the HSRP group's elected
// owner answers the virtual address, and with its interface down the
// standby does.
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

// ios returns a Cisco IOS configuration for one of an HSRP pair, with a
// secret in it.
func ios(name, lan, priority, uplink string) string {
	return "hostname " + name + "\n!\nenable secret 9 $9$notarealhash\n!\n" +
		"interface GigabitEthernet1\n ip address " + lan + " 255.255.255.0\n standby version 2\n standby 5 ip 10.0.12.254\n standby 5 priority " + priority + "\n standby 5 preempt\n no shutdown\n!\n" +
		"interface GigabitEthernet2\n ip address " + uplink + " 255.255.255.0\n no shutdown\n!\nend\n"
}

// TestCheck_CiscoHSRPFailover: the owner by priority answers the virtual
// address, and the standby does in the scenario where the owner's LAN
// interface is down; a hostname that does not match is refused.
func TestCheck_CiscoHSRPFailover(t *testing.T) {
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
		map[string]any{"name": "r1", "format": "cisco_ios", "config": ios("r1", "10.0.12.1", "200", "10.0.13.1")},
		map[string]any{"name": "r2", "format": "cisco_ios", "config": ios("r2", "10.0.12.2", "100", "10.0.13.2")},
	}
	vip := func(owner string) []any {
		return []any{map[string]any{"reach": map[string]any{"from": "r2[GigabitEthernet2]", "dst": "10.0.12.254", "accepted_by": owner}}}
	}
	rc := &statContext{stats: map[string]any{}}
	if _, err := batfish.Check(context.Background(), rc, svc, map[string]any{
		"devices":    devices,
		"assertions": vip("r1"),
		"scenarios": []any{map[string]any{
			"name": "r1's LAN down", "down_interfaces": []any{"r1[GigabitEthernet1]"}, "assertions": vip("r2"),
		}},
	}); err != nil {
		t.Fatalf("%v\n%v", err, rc.stats["assertions"])
	}
	if _, err := batfish.Check(context.Background(), &statContext{stats: map[string]any{}}, svc, map[string]any{
		"devices": devices, "assertions": vip("r2"),
	}); err == nil || !strings.Contains(err.Error(), "not answered by r2") {
		t.Errorf("the standby as owner with both up: err %v", err)
	}
	if _, err := batfish.Check(context.Background(), &statContext{stats: map[string]any{}}, svc, map[string]any{
		"devices":    []any{map[string]any{"name": "r9", "format": "cisco_ios", "config": ios("r1", "10.0.12.1", "200", "10.0.13.1")}},
		"assertions": []any{map[string]any{"ospf_established": true}},
	}); err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Errorf("a hostname that does not match: err %v", err)
	}
}

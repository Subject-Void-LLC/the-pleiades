// Tests that drive net.ovs.bridge and net.ovs.port against a real Open
// vSwitch switch in a container, over SSH as the unprivileged netops
// account, judging each by the switch's database read as root.
package ovs_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ovs"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// statContext is a RunbookContext holding a credential and the stats set.
type statContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *statContext) InjectSecrets() map[string]string { return c.secrets }
func (c *statContext) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *statContext) EmitFact(string, any) error       { return nil }

// method is one of this package's two, as Invoke or Check.
type method func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)

// undoOf returns the undo a run recorded, failing when there is none.
func undoOf(t *testing.T, rc *statContext) (map[string]any, bool) {
	t.Helper()
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("no undo was recorded: %v", rc.stats)
	}
	params, _ := inv[sdk.InverseParamsKey].(map[string]any)
	partial, _ := inv[sdk.InversePartialKey].(bool)
	return params, partial
}

// TestBridgeAndPort_OnARealSwitch: a bridge and ports are made, retagged,
// moved and removed, a check changes nothing, a converged run records no
// undo, and every undo puts the switch back as it was.
func TestBridgeAndPort_OnARealSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	links := []testsupport.NetLabLink{
		{Name: "a", Subnet: netip.MustParsePrefix("10.205.0.0/29"), Ends: map[string]netip.Addr{"sw1": netip.MustParseAddr("10.205.0.2")}},
		{Name: "b", Subnet: netip.MustParsePrefix("10.205.0.8/29"), Ends: map[string]netip.Addr{"sw1": netip.MustParseAddr("10.205.0.10")}},
	}
	lab := testsupport.StartNetLab(t, nil, []string{"sw1"}, links)
	lab.TrustHostKeys(t)
	sw := lab.Nodes["sw1"]
	ethA, ethB := sw.InterfaceWith(t, links[0].Ends["sw1"]), sw.InterfaceWith(t, links[1].Ends["sw1"])
	dev := sw.Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameOpenVSwitch)
	ctx := context.Background()
	rc := func() *statContext { return &statContext{secrets: lab.Secrets(), stats: map[string]any{}} }
	run := func(m method, params map[string]any) (*statContext, collection.Result) {
		t.Helper()
		c := rc()
		res, err := m(ctx, c, dev, params)
		if err != nil {
			t.Fatalf("%v: %v", params, err)
		}
		return c, res
	}
	vsctl := func(args ...string) string {
		return strings.TrimSpace(sw.Exec(t, append([]string{"ovs-vsctl"}, args...)...))
	}
	bridges := func() string { return vsctl("list-br") }

	// A check predicts the bridge and makes nothing.
	if _, res := run(ovs.CheckBridge, map[string]any{"name": "br9", "datapath_type": "netdev"}); !res.Changed || bridges() != "" {
		t.Fatalf("check: changed %v, bridges %q", res.Changed, bridges())
	}
	made, res := run(ovs.Bridge, map[string]any{"name": "br9", "datapath_type": "netdev"})
	if !res.Changed || bridges() != "br9" || vsctl("get", "Bridge", "br9", "datapath_type") != "netdev" {
		t.Fatalf("create: changed %v, bridges %q", res.Changed, bridges())
	}
	if again, res := run(ovs.Bridge, map[string]any{"name": "br9", "datapath_type": "netdev"}); res.Changed || again.stats[sdk.StatInverse] != nil {
		t.Errorf("a converged bridge: changed %v, undo %v", res.Changed, again.stats[sdk.StatInverse])
	}
	run(ovs.Bridge, map[string]any{"name": "br8", "datapath_type": "netdev"})

	// A port with a tag; a check of a retag changes nothing; the retag and
	// a move are each undone to what was there.
	_, res = run(ovs.Port, map[string]any{"bridge": "br9", "name": ethA, "tag": 10})
	if !res.Changed || vsctl("port-to-br", ethA) != "br9" || vsctl("get", "Port", ethA, "tag") != "10" {
		t.Fatalf("add: changed %v", res.Changed)
	}
	if _, res := run(ovs.CheckPort, map[string]any{"bridge": "br9", "name": ethA, "tag": 20}); !res.Changed || vsctl("get", "Port", ethA, "tag") != "10" {
		t.Errorf("a check of a retag: changed %v, tag %s", res.Changed, vsctl("get", "Port", ethA, "tag"))
	}
	retag, _ := run(ovs.Port, map[string]any{"bridge": "br9", "name": ethA, "tag": 0})
	if vsctl("get", "Port", ethA, "tag") != "[]" {
		t.Errorf("tag 0 left %s", vsctl("get", "Port", ethA, "tag"))
	}
	undo, _ := undoOf(t, retag)
	run(ovs.Port, undo)
	if vsctl("get", "Port", ethA, "tag") != "10" {
		t.Errorf("the retag's undo left tag %s", vsctl("get", "Port", ethA, "tag"))
	}
	moved, _ := run(ovs.Port, map[string]any{"bridge": "br8", "name": ethA})
	if vsctl("port-to-br", ethA) != "br8" || vsctl("get", "Port", ethA, "tag") != "10" {
		t.Errorf("the move left %s on %s", ethA, vsctl("port-to-br", ethA))
	}
	undo, _ = undoOf(t, moved)
	run(ovs.Port, undo)
	if vsctl("port-to-br", ethA) != "br9" {
		t.Errorf("the move's undo left %s on %s", ethA, vsctl("port-to-br", ethA))
	}
	run(ovs.Port, map[string]any{"bridge": "br9", "name": ethB})

	// A port carrying more than a tag (trunks here): a move would drop
	// them, so it is refused; a removal's undo cannot put them back, so it
	// says it is partial.
	vsctl("set", "Port", ethB, "trunks=10,20")
	if _, err := ovs.Port(ctx, rc(), dev, map[string]any{"bridge": "br8", "name": ethB}); err == nil || !strings.Contains(err.Error(), "trunks") {
		t.Errorf("moving a trunk port: err %v", err)
	}
	if vsctl("port-to-br", ethB) != "br9" {
		t.Error("a refused move moved the port")
	}
	trunk, _ := run(ovs.Port, map[string]any{"bridge": "br9", "name": ethB, "state": "absent"})
	if undo, partial := undoOf(t, trunk); !partial || !strings.Contains(trunk.stats[sdk.StatInverse].(map[string]any)["description"].(string), "trunks") {
		t.Errorf("a trunk port's removal: undo %v partial %v", undo, partial)
	}
	run(ovs.Port, map[string]any{"bridge": "br9", "name": ethB})

	// Removing a port is undone with its tag; removing a bridge with ports
	// is a partial undo, and the undo brings the bridge back.
	removed, _ := run(ovs.Port, map[string]any{"bridge": "br9", "name": ethA, "state": "absent"})
	undo, _ = undoOf(t, removed)
	run(ovs.Port, undo)
	if vsctl("port-to-br", ethA) != "br9" || vsctl("get", "Port", ethA, "tag") != "10" {
		t.Errorf("the removal's undo left %s on %q tag %s", ethA, vsctl("port-to-br", ethA), vsctl("get", "Port", ethA, "tag"))
	}
	gone, _ := run(ovs.Bridge, map[string]any{"name": "br9", "state": "absent"})
	if strings.Contains(bridges(), "br9") {
		t.Fatal("br9 is still there")
	}
	undo, partial := undoOf(t, gone)
	if !partial || undo["datapath_type"] != "netdev" {
		t.Errorf("a bridge removed with ports: undo %v partial %v", undo, partial)
	}
	run(ovs.Bridge, undo)
	if !strings.Contains(bridges(), "br9") || vsctl("get", "Bridge", "br9", "datapath_type") != "netdev" {
		t.Errorf("the bridge's undo left %q", bridges())
	}
	undo, _ = undoOf(t, made)
	run(ovs.Bridge, undo)
	if strings.Contains(bridges(), "br9") {
		t.Error("the create's undo left br9")
	}

	for _, tc := range []struct {
		m      method
		params map[string]any
	}{
		{ovs.Bridge, map[string]any{"name": "br0; reboot"}},
		{ovs.Bridge, map[string]any{"name": "br7", "datapath_type": "dpdk"}},
		{ovs.Bridge, map[string]any{"name": "br7", "state": "gone"}},
		{ovs.Port, map[string]any{"bridge": "br8", "name": ethB, "tag": 4095}},
		{ovs.Port, map[string]any{"bridge": "nobridge", "name": ethB}},
		{ovs.Port, map[string]any{"bridge": "br8", "name": "--all"}},
	} {
		if _, err := tc.m(ctx, rc(), dev, tc.params); err == nil {
			t.Errorf("%v was accepted", tc.params)
		}
	}
}

// The net.linux methods against a real Linux router in a container, reached
// over SSH as an unprivileged account the way a real host is. The account has
// no right to change the network, so the methods are shown refusing with ip's
// own words, then run as root through sudo (become), and each change is judged
// by the container's own state read as root, never by what a method reported.
package linux_test

import (
	"context"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// runContext holds the lab's credential and the stats a method set.
type runContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *runContext) InjectSecrets() map[string]string { return c.secrets }
func (c *runContext) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *runContext) EmitFact(string, any) error       { return nil }

// host is one lab router and how to call a method on it.
type host struct {
	t   *testing.T
	lab *testsupport.NetLab
	dev inventory.InventoryItem
}

// call runs a method as the login (root false) or as root through sudo.
func (h *host) call(fqcn string, params map[string]any, root, check bool) (*runContext, collection.Result, error) {
	h.t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		h.t.Fatalf("%s is not registered", fqcn)
	}
	fn := d.Invoke
	if check {
		fn = d.Check
	}
	ctx := context.Background()
	if root {
		ctx = sdk.WithBecome(ctx, "root")
	}
	rc := &runContext{secrets: h.lab.Secrets(), stats: map[string]any{}}
	res, err := fn(ctx, rc, h.dev, params)
	return rc, res, err
}

// must is call as root that fails the test on an error.
func (h *host) must(fqcn string, params map[string]any) (*runContext, collection.Result) {
	h.t.Helper()
	rc, res, err := h.call(fqcn, params, true, false)
	if err != nil {
		h.t.Fatalf("%s: %v", fqcn, err)
	}
	return rc, res
}

// sh reads the container's own state as root.
func (h *host) sh(script string) string {
	h.t.Helper()
	return strings.TrimSpace(h.lab.Nodes["r1"].Exec(h.t, "sh", "-c", script))
}

// undo runs the undo a call recorded, as root.
func (h *host) undo(rc *runContext) {
	h.t.Helper()
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		h.t.Fatalf("no undo was recorded: %v", rc.stats)
	}
	params, _ := inv[sdk.InverseParamsKey].(map[string]any)
	fqcn, _ := inv[sdk.InverseFQCNKey].(string)
	if _, _, err := h.call(fqcn, params, true, false); err != nil {
		h.t.Fatalf("the undo %s %v: %v", fqcn, params, err)
	}
}

// TestLinux_BridgeAddressAndRouteOnARealRouter is the three methods' contract
// on one router.
func TestLinux_BridgeAddressAndRouteOnARealRouter(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	lab := testsupport.StartNetLab(t, []string{"r1"}, nil, nil)
	lab.TrustHostKeys(t)
	h := &host{t: t, lab: lab, dev: lab.Nodes["r1"].Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameLinux)}
	// Two veth pairs to attach: the pairs are the test's own setup, as root.
	h.sh("ip link add v0 type veth peer name v0p && ip link add v1 type veth peer name v1p")

	// The login may not change the network: the refusal is ip's own, with the
	// way out named, and nothing changed.
	_, _, err := h.call("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{"v0"}}, false, false)
	if err == nil || !strings.Contains(err.Error(), "not permitted") || !strings.Contains(err.Error(), "become") {
		t.Fatalf("a bridge as the unprivileged login: %v", err)
	}
	if got := h.sh("ls /sys/class/net | grep -c '^br9$' || true"); got != "0" {
		t.Fatalf("a refused task made the bridge: %s", got)
	}

	// A bridge: a check predicts and changes nothing; a run makes it, up, with
	// its port; a second run is unchanged; each is read from the container.
	if _, res, err := h.call("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{"v0"}}, true, true); err != nil || !res.Changed {
		t.Fatalf("a check of a new bridge: changed %v, err %v", res.Changed, err)
	}
	if got := h.sh("ls /sys/class/net | grep -c '^br9$' || true"); got != "0" {
		t.Fatalf("a check made the bridge: %s", got)
	}
	made, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{"v0"}})
	if !res.Changed || made.stats["exists"] != true || made.stats["up"] != true {
		t.Fatalf("bridge: changed %v, stats %v", res.Changed, made.stats)
	}
	if got := h.sh("ls /sys/class/net/br9/brif; cat /sys/class/net/br9/flags"); !strings.HasPrefix(got, "v0\n") || !strings.HasSuffix(got, "3") && !strings.Contains(got, "0x1") {
		t.Errorf("the container's bridge: %q", got)
	}
	if _, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{"v0"}}); res.Changed {
		t.Error("a bridge already as asked changed")
	}
	// A second port is attached, and the undo of that detaches it only.
	more, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{"v0", "v1"}})
	if !res.Changed || h.sh("ls /sys/class/net/br9/brif | tr '\\n' ' '") != "v0 v1" {
		t.Fatalf("attaching a second port: changed %v, ports %q", res.Changed, h.sh("ls /sys/class/net/br9/brif"))
	}
	h.undo(more)
	if got := h.sh("ls /sys/class/net/br9/brif | tr '\\n' ' '"); got != "v0" {
		t.Errorf("the undo of an attach left ports %q", got)
	}
	// remove_other_ports detaches what is not listed.
	if _, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{}, "remove_other_ports": true}); !res.Changed || h.sh("ls /sys/class/net/br9/brif | wc -l") != "0" {
		t.Errorf("remove_other_ports: changed %v", res.Changed)
	}
	// A device that is not a bridge is refused, not replaced.
	if _, _, err := h.call("net.linux.bridge", map[string]any{"name": "lo"}, true, false); err == nil || !strings.Contains(err.Error(), "not a bridge") {
		t.Errorf("a name that is not a bridge: %v", err)
	}

	// An address on the bridge, idempotent, with its undo.
	addr := map[string]any{"interface": "br9", "address": "10.77.0.1/24"}
	if _, res, err := h.call("net.linux.address", addr, true, true); err != nil || !res.Changed {
		t.Fatalf("a check of a new address: changed %v, err %v", res.Changed, err)
	}
	a1, res := h.must("net.linux.address", addr)
	if !res.Changed || !strings.Contains(h.sh("ip -o addr show dev br9"), "10.77.0.1/24") || a1.stats["present"] != true {
		t.Fatalf("address: changed %v, stats %v", res.Changed, a1.stats)
	}
	if _, res := h.must("net.linux.address", addr); res.Changed {
		t.Error("an address already there changed")
	}
	h.undo(a1)
	if strings.Contains(h.sh("ip -o addr show dev br9"), "10.77.0.1/24") {
		t.Error("the undo left the address")
	}
	h.must("net.linux.address", addr)
	if _, _, err := h.call("net.linux.address", map[string]any{"interface": "nosuch0", "address": "10.1.1.1/24"}, true, false); err == nil {
		t.Error("an address on an interface that is not there was accepted")
	}

	// A route through the bridge's subnet: add, idempotent, replace with an
	// exact undo, remove with an undo that adds it back.
	route := map[string]any{"destination": "10.88.0.0/24", "via": "10.77.0.2"}
	r1, res := h.must("net.linux.route", route)
	if !res.Changed || !strings.Contains(h.sh("ip route show 10.88.0.0/24"), "via 10.77.0.2") {
		t.Fatalf("route: changed %v, table %q", res.Changed, h.sh("ip route show"))
	}
	if _, res := h.must("net.linux.route", route); res.Changed {
		t.Error("a route already there changed")
	}
	r2, res := h.must("net.linux.route", map[string]any{"destination": "10.88.0.0/24", "via": "10.77.0.3"})
	if !res.Changed || !strings.Contains(h.sh("ip route show 10.88.0.0/24"), "via 10.77.0.3") {
		t.Fatalf("a replaced gateway: changed %v, table %q", res.Changed, h.sh("ip route show 10.88.0.0/24"))
	}
	h.undo(r2)
	if got := h.sh("ip route show 10.88.0.0/24"); !strings.Contains(got, "via 10.77.0.2") {
		t.Errorf("the undo of a replace left %q", got)
	}
	gone, res := h.must("net.linux.route", map[string]any{"destination": "10.88.0.0/24", "state": "absent"})
	if !res.Changed || h.sh("ip route show 10.88.0.0/24") != "" {
		t.Fatalf("a removed route: changed %v, table %q", res.Changed, h.sh("ip route show"))
	}
	h.undo(gone)
	if !strings.Contains(h.sh("ip route show 10.88.0.0/24"), "via 10.77.0.2") {
		t.Error("the undo of a removal did not add the route back")
	}
	h.undo(r1)
	if h.sh("ip route show 10.88.0.0/24") != "" {
		t.Error("the undo of the add left the route")
	}
	for name, p := range map[string]map[string]any{
		"no gateway or device": {"destination": "10.9.0.0/24"},
		"a bad destination":    {"destination": "ten", "via": "10.77.0.2"},
		"a bad gateway":        {"destination": "10.9.0.0/24", "via": "gw"},
	} {
		if _, _, err := h.call("net.linux.route", p, true, false); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// The bridge and the address come off, and the undo of removing a bridge
	// makes it again with its port.
	if _, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "ports": []any{"v0"}}); !res.Changed {
		t.Error("re-attaching a port changed nothing")
	}
	removed, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "state": "absent"})
	if !res.Changed || h.sh("ls /sys/class/net | grep -c '^br9$' || true") != "0" {
		t.Fatalf("removing the bridge: changed %v", res.Changed)
	}
	if _, res := h.must("net.linux.bridge", map[string]any{"name": "br9", "state": "absent"}); res.Changed {
		t.Error("removing an absent bridge changed")
	}
	h.undo(removed)
	if got := h.sh("ls /sys/class/net/br9/brif | tr '\\n' ' '"); got != "v0" {
		t.Errorf("the undo of a removal made a bridge with ports %q", got)
	}
}

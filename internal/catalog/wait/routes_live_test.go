// Tests that wait.routes waits for a route OSPF really learns between two
// FRRouting routers, and fails naming what never arrived.
package wait_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestRoutes_WaitsForARouteOSPFLearns: r2 advertises a loopback address
// over OSPF; r1's wait returns once it routes there through r2, and fails
// for a prefix nobody advertises and for a next hop the route does not
// use.
func TestRoutes_WaitsForARouteOSPFLearns(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	link := testsupport.NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.203.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.203.0.2"), "r2": netip.MustParseAddr("10.203.0.3"),
	}}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2"}, nil, []testsupport.NetLabLink{link})
	lab.TrustHostKeys(t)
	for _, r := range []struct{ name, id, extra string }{{"r1", "10.255.0.1", ""}, {"r2", "10.255.0.2", "10.99.2.1/32"}} {
		n := lab.Nodes[r.name]
		args := []string{"vtysh", "-c", "configure terminal",
			"-c", "interface " + n.InterfaceWith(t, link.Ends[r.name]), "-c", "ip ospf network point-to-point", "-c", "exit"}
		if r.extra != "" {
			args = append(args, "-c", "interface lo", "-c", "ip address "+r.extra, "-c", "exit")
		}
		args = append(args, "-c", "router ospf", "-c", "ospf router-id "+r.id, "-c", "network 10.203.0.0/29 area 0")
		if r.extra != "" {
			args = append(args, "-c", "network "+r.extra+" area 0")
		}
		n.Exec(t, args...)
	}
	r1 := lab.Nodes["r1"].Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameFRR)
	rc := func() *waitContext { return &waitContext{secrets: lab.Secrets(), stats: map[string]any{}} }

	got := rc()
	if _, err := wait.Routes(context.Background(), got, r1, map[string]any{
		"prefixes": []any{"10.99.2.1/32"}, "next_hops": map[string]any{"10.99.2.1/32": []any{"10.203.0.3"}},
		"stable_for": 2, "timeout": 90,
	}); err != nil {
		t.Fatal(err)
	}
	if routes, _ := got.stats["routes"].(map[string][]string); len(routes["10.99.2.1/32"]) != 1 || routes["10.99.2.1/32"][0] != "10.203.0.3" {
		t.Errorf("routes = %v, want 10.99.2.1/32 through 10.203.0.3", got.stats["routes"])
	}
	if out := lab.Nodes["r1"].Exec(t, "ip", "route", "get", "10.99.2.1"); !strings.Contains(out, "via 10.203.0.3") {
		t.Errorf("the wait returned, and r1's kernel does not route through r2: %s", out)
	}
	if waitDiffHalf(t, got, "before") == nil {
		t.Error("no diff was recorded")
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"a prefix nobody advertises", map[string]any{"prefixes": []any{"10.250.0.0/24"}, "stable_for": 1, "timeout": 3}, "to 10.250.0.0/24"},
		{"a next hop the route does not use", map[string]any{"prefixes": []any{"10.99.2.1/32"}, "next_hops": map[string]any{"10.99.2.1/32": []any{"10.203.0.4"}}, "stable_for": 1, "timeout": 3}, "through 10.203.0.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := wait.Routes(context.Background(), rc(), r1, tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

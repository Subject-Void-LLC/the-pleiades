// Tests that net.trace tells an asymmetric path, a loop and a path no hop
// answers apart, on real routers whose routes are set to make each.
package net

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// TestTrace_AsymmetryLoopsAndSilenceOnRealRouters builds a triangle of
// three FRR routers and traces from r1, with kernel routes making each
// case:
//
//   - to r3 through r2, while r3 answers r1 over their own link: the probe
//     goes out over 2 hops and the reply comes back over 1, as tracepath
//     says itself (asymm and back), and expect.symmetric: true fails;
//   - to r2 over their link: symmetric;
//   - to an address r2 and r3 route to each other: r2 and r3 answer in
//     turn, a loop;
//   - to an address r2 discards with a blackhole route: no hop answers, the
//     path is unknown and the task fails, unless only reached: false was
//     expected.
func TestTrace_AsymmetryLoopsAndSilenceOnRealRouters(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	addr := netip.MustParseAddr
	links := []testsupport.NetLabLink{
		{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.206.0.0/29"), Ends: map[string]netip.Addr{"r1": addr("10.206.0.2"), "r2": addr("10.206.0.3")}},
		{Name: "r2-r3", Subnet: netip.MustParsePrefix("10.206.0.8/29"), Ends: map[string]netip.Addr{"r2": addr("10.206.0.10"), "r3": addr("10.206.0.11")}},
		{Name: "r1-r3", Subnet: netip.MustParsePrefix("10.206.0.16/29"), Ends: map[string]netip.Addr{"r1": addr("10.206.0.18"), "r3": addr("10.206.0.19")}},
	}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2", "r3"}, nil, links)
	lab.TrustHostKeys(t)
	for _, n := range []string{"r1", "r2", "r3"} {
		// Forward, and accept a reply on an interface other than the one
		// its source is routed out of, which an asymmetric path needs.
		lab.Nodes[n].Exec(t, "sh", "-c", "sysctl -qw net.ipv4.ip_forward=1 && for f in /proc/sys/net/ipv4/conf/*/rp_filter; do echo 0 > $f; done")
	}
	route := func(node string, args ...string) {
		lab.Nodes[node].Exec(t, append([]string{"ip", "route", "add"}, args...)...)
	}
	// r1 reaches r3's r2-r3 address through r2; r3 answers r1's r1-r2
	// address over the r1-r3 link.
	route("r1", "10.206.0.11/32", "via", "10.206.0.3")
	route("r3", "10.206.0.0/29", "via", "10.206.0.18")
	// A loop between r2 and r3.
	route("r1", "192.0.2.77/32", "via", "10.206.0.3")
	route("r2", "192.0.2.77/32", "via", "10.206.0.11")
	route("r3", "192.0.2.77/32", "via", "10.206.0.10")
	// A destination r2 discards without a word.
	route("r1", "192.0.2.99/32", "via", "10.206.0.3")
	route("r2", "blackhole", "192.0.2.99/32")

	r1 := lab.Nodes["r1"].Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameLinux)
	rc := func() *statContext { return &statContext{secrets: lab.Secrets(), stats: map[string]any{}} }
	ctx := context.Background()

	asym := rc()
	if _, err := Trace(ctx, asym, r1, map[string]any{"dest": "10.206.0.11", "expect": map[string]any{"reached": true}}); err != nil {
		t.Fatal(err)
	}
	if asym.stats["asymmetric"] != true || asym.stats["return_hops"] != 1 || asym.stats["path"] != pathReached {
		t.Errorf("stats %v, want reached, asymmetric, 1 hop back", asym.stats)
	}
	// The destination's count comes from the Resume line: tracepath prints
	// no asymm on the line that reached it.
	if d := hopMaps(asym.stats["hop_details"]); len(d) != 2 || d[1]["return_hops"] != 1 || d[0]["return_hops"] != nil {
		t.Errorf("hop_details %v, want return_hops 1 on hop 2 alone", asym.stats["hop_details"])
	}
	if _, err := Trace(ctx, rc(), r1, map[string]any{"dest": "10.206.0.11", "expect": map[string]any{"symmetric": true}}); err == nil || !strings.Contains(err.Error(), "asymmetric") {
		t.Errorf("an asymmetric path expected symmetric: %v", err)
	}

	sym := rc()
	if _, err := Trace(ctx, sym, r1, map[string]any{"dest": "10.206.0.3", "expect": map[string]any{"symmetric": true}}); err != nil {
		t.Errorf("a symmetric path: %v", err)
	}
	if sym.stats["asymmetric"] != false {
		t.Errorf("stats %v, want a symmetric path", sym.stats)
	}

	loop := rc()
	if _, err := Trace(ctx, loop, r1, map[string]any{"dest": "192.0.2.77", "max_hops": 6}); err != nil {
		t.Fatal(err)
	}
	if loop.stats["path"] != pathLoop || loop.stats["reached"] != false {
		t.Errorf("stats %v, want a loop", loop.stats)
	}

	silent := rc()
	_, err := Trace(ctx, silent, r1, map[string]any{"dest": "192.0.2.99", "max_hops": 3})
	if err == nil || !strings.Contains(err.Error(), "path is unknown") {
		t.Errorf("a path no hop answered: %v, want it unknown and failed", err)
	}
	if silent.stats["path"] != pathUnknown {
		t.Errorf("stats %v, want an unknown path", silent.stats)
	}
	if _, err := Trace(ctx, rc(), r1, map[string]any{"dest": "192.0.2.99", "max_hops": 3, "expect": map[string]any{"reached": false}}); err != nil {
		t.Errorf("a silent path expected unreached: %v", err)
	}
	if _, err := Trace(ctx, rc(), r1, map[string]any{"dest": "192.0.2.99", "max_hops": 3, "expect": map[string]any{"avoids": []any{"10.206.0.3"}}}); err == nil {
		t.Error("a silent path proved an address was avoided")
	}
}

// hopMaps is a hop_details stat as its maps, or nil when it is not one.
func hopMaps(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		m, ok := d.(map[string]any)
		if !ok {
			return nil
		}
		out = append(out, m)
	}
	return out
}

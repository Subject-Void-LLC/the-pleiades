// Tests of how wait.routes reads a Cisco IOS routing table: show ip route,
// as pkg/iosparse parses it, becomes the route shape FRR's JSON has, so a
// connected route, a static one and an OSPF one through two next hops are
// each selected with their next hops; and a device that is neither FRR nor
// IOS is refused before it is reached.
package wait

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
)

// iosTable is show ip route from an IOS XE router: a connected tunnel, a
// static default and an OSPF route with two equal-cost next hops.
const iosTable = `Codes: L - local, C - connected, S - static, R - RIP, M - mobile, B - BGP
       D - EIGRP, EX - EIGRP external, O - OSPF, IA - OSPF inter area
Gateway of last resort is 10.0.12.2 to network 0.0.0.0

S*    0.0.0.0/0 [1/0] via 10.0.12.2
      10.0.0.0/8 is variably subnetted, 4 subnets, 3 masks
O        10.3.3.0/24 [110/20] via 10.0.12.2, 00:01:02, Ethernet0/1
                     [110/20] via 10.0.13.3, 00:01:02, Ethernet0/2
C        10.0.12.0/30 is directly connected, Ethernet0/1
L        10.0.12.1/32 is directly connected, Ethernet0/1
      172.31.0.0/16 is variably subnetted, 2 subnets, 2 masks
C        172.31.0.0/30 is directly connected, Tunnel0
L        172.31.0.1/32 is directly connected, Tunnel0
`

func TestIOSRoutes(t *testing.T) {
	_, parsed, err := iosparse.Parse("show ip route", iosTable)
	if err != nil {
		t.Fatal(err)
	}
	table := iosRoutes(parsed)
	hops := func(prefix string) []string {
		var out []string
		for _, r := range table[prefix] {
			if !r.Selected {
				t.Errorf("%s is not selected", prefix)
			}
			for _, h := range r.NextHops {
				if h.IP != "" {
					out = append(out, h.IP)
				} else {
					out = append(out, h.Interface)
				}
			}
		}
		return out
	}
	if got := hops("10.3.3.0/24"); !slices.Equal(got, []string{"10.0.12.2", "10.0.13.3"}) {
		t.Errorf("the OSPF route's next hops = %v", got)
	}
	if got := hops("172.31.0.0/30"); !slices.Equal(got, []string{"Tunnel0"}) {
		t.Errorf("the connected tunnel = %v", got)
	}
	if got := hops("0.0.0.0/0"); !slices.Equal(got, []string{"10.0.12.2"}) {
		t.Errorf("the static default = %v", got)
	}
	if r := table["10.3.3.0/24"][0]; r.Protocol != "ospf" || r.Distance != 110 || r.Metric != 20 {
		t.Errorf("the OSPF route = %+v", r)
	}
	want := routesWant{prefixes: []string{"10.3.3.0/24", "172.31.0.0/30"}, nextHops: map[string][]string{"10.3.3.0/24": {"10.0.13.3"}}}
	if _, missing := want.satisfied(table); len(missing) != 0 {
		t.Errorf("missing %v", missing)
	}
	// Uptimes are not in the shape, so a table read twice compares equal.
	_, again, _ := iosparse.Parse("show ip route", strings.ReplaceAll(iosTable, "00:01:02", "00:01:09"))
	if fingerprint(iosRoutes(again)) != fingerprint(table) {
		t.Error("an uptime moved the fingerprint")
	}

	if _, _, err := routeReader(context.Background(), nil, &inventorytest.Stub{StubName: "linux1"}, nil); err == nil || !strings.Contains(err.Error(), "neither an FRR router") {
		t.Errorf("a device neither FRR nor IOS: %v", err)
	}
}

// Tests that net.batfish.check holds real routers' configurations, read
// live through net.frr.facts, to a real Batfish: the network as it runs
// passes (OSPF and BGP), its link failing is predicted, a candidate that stops
// advertising a route is refused, and an unrecognised line is refused
// unless accepted.
package batfish_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
	bfdevice "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// statContext is a RunbookContext holding a credential and the stats set.
type statContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *statContext) InjectSecrets() map[string]string { return c.secrets }
func (c *statContext) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *statContext) EmitFact(string, any) error       { return nil }

// TestCheck_RealRoutersAgainstRealBatfish is the method's contract, end to
// end on real services.
func TestCheck_RealRoutersAgainstRealBatfish(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	ctx := context.Background()
	link := testsupport.NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.210.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.210.0.2"), "r2": netip.MustParseAddr("10.210.0.3"),
	}}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2"}, nil, []testsupport.NetLabLink{link})
	lab.TrustHostKeys(t)
	ifaces := map[string]string{}
	for name, lo := range map[string]string{"r1": "10.99.1.1/32", "r2": "10.99.2.1/32"} {
		n := lab.Nodes[name]
		ifaces[name] = n.InterfaceWith(t, link.Ends[name])
		n.Exec(t, "vtysh", "-c", "configure terminal", "-c", "hostname "+name,
			"-c", "interface "+ifaces[name], "-c", "ip address "+link.Ends[name].String()+"/29", "-c", "ip ospf area 0", "-c", "ip ospf network point-to-point", "-c", "ip ospf authentication-key k3y", "-c", "exit",
			"-c", "interface lo", "-c", "ip address "+lo, "-c", "ip ospf area 0", "-c", "exit",
			"-c", "router ospf", "-c", "ospf router-id "+strings.TrimSuffix(lo, "/32"))
	}
	// eBGP over the link, r2 announcing a prefix it holds as a static
	// route, so the model is held to BGP too.
	lab.Nodes["r1"].Exec(t, "vtysh", "-c", "configure terminal", "-c", "router bgp 65001", "-c", "no bgp ebgp-requires-policy",
		"-c", "neighbor 10.210.0.3 remote-as 65002")
	lab.Nodes["r2"].Exec(t, "vtysh", "-c", "configure terminal", "-c", "ip route 10.98.2.0/24 Null0", "-c", "router bgp 65002",
		"-c", "no bgp ebgp-requires-policy", "-c", "neighbor 10.210.0.2 remote-as 65001",
		"-c", "address-family ipv4 unicast", "-c", "network 10.98.2.0/24", "-c", "exit-address-family")
	facts := map[string]map[string]any{}
	for _, name := range []string{"r1", "r2"} {
		rc := &statContext{secrets: lab.Secrets(), stats: map[string]any{}}
		dev := lab.Nodes[name].Device(capability.NameSSHTransport, capability.NameCommandExec, capability.NameFRR)
		// Facts until OSPF has given r1 r2's loopback, which a real run
		// waits for with wait.routes.
		for i := 0; ; i++ {
			if _, err := frr.Facts(ctx, rc, dev, map[string]any{"include_config": true}); err != nil {
				t.Fatal(err)
			}
			routes, _ := rc.stats["routes"].(map[string]any)
			if name != "r1" || (routes["10.99.2.1/32"] != nil && routes["10.98.2.0/24"] != nil) || i > 90 {
				break
			}
			lab.Nodes[name].Exec(t, "sleep", "1")
		}
		facts[name] = rc.stats
	}
	// The management network's routes (connected, and FRR's local one) are
	// in no configuration.
	var mgmt []any
	for prefix := range facts["r1"]["routes"].(map[string]any) {
		if strings.HasPrefix(prefix, "172.") {
			mgmt = append(mgmt, prefix)
		}
	}

	svc, err := bfdevice.NewService(record.Record{Name: "batfish", Type: bfdevice.TypeService, Properties: map[string]inventory.PropertyValue{
		bfdevice.APIBaseProperty: strings.Replace(testsupport.StartBatfish(t), "localhost", "127.0.0.1", 1),
	}})
	if err != nil {
		t.Fatal(err)
	}
	config := func(name string) string { return facts[name]["running_config"].(string) }
	devices := func(r2 string) []any {
		return []any{map[string]any{"name": "r1", "config": config("r1")}, map[string]any{"name": "r2", "config": r2}}
	}
	accept := []any{
		map[string]any{"text": "domainname localdomain", "reason": "DNS search domain, not routing"},
		map[string]any{"text": "ip ospf authentication-key", "reason": "Batfish does not model OSPF simple authentication; both ends hold one key"},
		map[string]any{"text": "no bgp ebgp-requires-policy", "reason": "FRR's rule that an eBGP session needs a policy, which both ends turn off; Batfish never requires one"},
	}
	run := func(params map[string]any) (*statContext, error) {
		rc := &statContext{stats: map[string]any{}}
		_, err := batfish.Check(ctx, rc, svc, params)
		return rc, err
	}

	// The network as it runs passes, and the link failing is predicted.
	rc, err := run(map[string]any{
		"devices": devices(config("r2")),
		"assertions": []any{
			map[string]any{"ospf_established": true},
			map[string]any{"bgp_established": true},
			map[string]any{"route": map[string]any{"node": "r1", "prefix": "10.99.2.1/32", "protocol": "ospf", "next_hops": []any{"10.210.0.3"}}},
			map[string]any{"route": map[string]any{"node": "r1", "prefix": "10.98.2.0/24", "protocol": "bgp", "next_hops": []any{"10.210.0.3"}}},
			map[string]any{"reach": map[string]any{"from": "r1", "dst": "10.99.2.1", "through": []any{"r2"}}},
			map[string]any{"routes_match": map[string]any{"node": "r1", "routes": facts["r1"]["routes"], "ignore": mgmt}},
		},
		"scenarios": []any{map[string]any{
			"name": "the link down", "down_interfaces": []any{"r1[" + ifaces["r1"] + "]"},
			"assertions": []any{
				map[string]any{"no_route": map[string]any{"node": "r1", "prefix": "10.99.2.1/32"}},
				map[string]any{"reach": map[string]any{"from": "r1", "dst": "10.99.2.1", "delivered": false}},
			},
		}},
		"accept_unrecognized": accept,
		// sw1 stands for a switch the lab holds and the model was never given.
		"expect_devices": []any{"r1", "r2", "sw1"},
	})
	if err != nil {
		t.Fatalf("the running network: %v\nassertions: %v\nunrecognized: %v", err, rc.stats["assertions"], rc.stats["unrecognized"])
	}
	if rc.stats["passed"] != true || len(rc.stats["assertions"].([]any)) != 8 {
		t.Errorf("passed %v, assertions %v", rc.stats["passed"], rc.stats["assertions"])
	}
	sum := sha256.Sum256([]byte(config("r2")))
	if rc.stats["sha256"].(map[string]any)["r2"] != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %v", rc.stats["sha256"])
	}
	// The routers' own OSPF key, not "domainname localdomain", which FRR
	// writes only when the Docker host gives the container a search domain
	// (this machine's Docker Desktop does; GitHub's runners do not).
	if nm := rc.stats["not_modelled"].([]string); len(nm) == 0 || !strings.Contains(strings.Join(nm, " "), "ip ospf authentication-key") {
		t.Errorf("not_modelled = %v", nm)
	}
	// The device the model was never given is named as absent from it, and the
	// two it was given are not.
	joined := strings.Join(rc.stats["not_modelled"].([]string), "\n")
	if !strings.Contains(joined, "sw1: no configuration was given for it") || strings.Contains(joined, "r1: no configuration") || strings.Contains(joined, "r2: no configuration") {
		t.Errorf("expect_devices: not_modelled = %q", joined)
	}

	// A candidate in which r2 stops advertising its loopback is refused,
	// naming the assertion.
	bad := strings.Replace(config("r2"), "interface lo\n ip address 10.99.2.1/32\n ip ospf area 0\n", "interface lo\n ip address 10.99.2.1/32\n", 1)
	if bad == config("r2") {
		t.Fatalf("the candidate did not change:\n%s", config("r2"))
	}
	rc, err = run(map[string]any{
		"devices":             devices(bad),
		"assertions":          []any{map[string]any{"route": map[string]any{"node": "r1", "prefix": "10.99.2.1/32"}}},
		"accept_unrecognized": accept,
	})
	if err == nil || !strings.Contains(err.Error(), "r1 has no route to 10.99.2.1/32") || rc.stats["passed"] != false {
		t.Errorf("a candidate that stops advertising: err %v, passed %v", err, rc.stats["passed"])
	}

	// A line the model does not recognise is refused unless accepted.
	if _, err = run(map[string]any{
		"devices":    devices(config("r2")),
		"assertions": []any{map[string]any{"ospf_established": true}},
	}); err == nil || !strings.Contains(err.Error(), "no bgp ebgp-requires-policy") {
		t.Errorf("an unaccepted unrecognised line: err %v", err)
	}
}

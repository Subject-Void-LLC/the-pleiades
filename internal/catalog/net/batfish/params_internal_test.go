// Tests of what net.batfish.check refuses before Batfish is reached, and
// of how it reads the model's words.
package batfish

import (
	"strings"
	"testing"
)

// TestReadParams_Refusals: every parameter that cannot mean what it says
// is refused by name.
func TestReadParams_Refusals(t *testing.T) {
	frr := "hostname r1\nrouter ospf\nexit\n"
	dev := func(extra map[string]any) []any {
		d := map[string]any{"name": "r1", "config": frr}
		for k, v := range extra {
			d[k] = v
		}
		return []any{d}
	}
	ok := []any{map[string]any{"ospf_established": true}}
	for name, p := range map[string]map[string]any{
		"no devices":                    {"assertions": ok},
		"nothing asked":                 {"devices": dev(nil)},
		"an unknown parameter":          {"devices": dev(nil), "assertions": ok, "asserts": ok},
		"config and src":                {"devices": dev(map[string]any{"src": "r1.conf"}), "assertions": ok},
		"src with no project":           {"devices": []any{map[string]any{"name": "r1", "src": "r1.conf"}}, "assertions": ok},
		"an unknown format":             {"devices": dev(map[string]any{"format": "junos"}), "assertions": ok},
		"a hostname mismatch":           {"devices": []any{map[string]any{"name": "r9", "format": "cisco_ios", "config": "hostname r1\n"}}, "assertions": ok},
		"a node name as a path":         {"devices": dev(map[string]any{"name": "../r1"}), "assertions": ok},
		"a device twice":                {"devices": append(dev(nil), dev(nil)...), "assertions": ok},
		"an unknown assertion":          {"devices": dev(nil), "assertions": []any{map[string]any{"ospf_up": true}}},
		"two kinds in one":              {"devices": dev(nil), "assertions": []any{map[string]any{"ospf_established": true, "bgp_established": true}}},
		"a route to a stranger":         {"devices": dev(nil), "assertions": []any{map[string]any{"route": map[string]any{"node": "r9", "prefix": "10.0.0.0/8"}}}},
		"a bad prefix":                  {"devices": dev(nil), "assertions": []any{map[string]any{"route": map[string]any{"node": "r1", "prefix": "10.0.0.0"}}}},
		"a bad next hop":                {"devices": dev(nil), "assertions": []any{map[string]any{"route": map[string]any{"node": "r1", "prefix": "10.0.0.0/8", "next_hops": []any{"r2"}}}}},
		"reach from nowhere":            {"devices": dev(nil), "assertions": []any{map[string]any{"reach": map[string]any{"from": "r9", "dst": "10.0.0.1"}}}},
		"reach to a name":               {"devices": dev(nil), "assertions": []any{map[string]any{"reach": map[string]any{"from": "r1", "dst": "r2"}}}},
		"routes_match no routes":        {"devices": dev(nil), "assertions": []any{map[string]any{"routes_match": map[string]any{"node": "r1"}}}},
		"a scenario with nothing down":  {"devices": dev(nil), "scenarios": []any{map[string]any{"name": "s", "assertions": ok}}},
		"a scenario with no assertions": {"devices": dev(nil), "scenarios": []any{map[string]any{"name": "s", "down_nodes": []any{"r1"}}}},
		"down a stranger's interface":   {"devices": dev(nil), "scenarios": []any{map[string]any{"name": "s", "down_interfaces": []any{"r9[eth1]"}, "assertions": ok}}},
		"an acceptance with no reason":  {"devices": dev(nil), "assertions": ok, "accept_unrecognized": []any{map[string]any{"text": "exit"}}},
		"an unknown feature":            {"devices": dev(nil), "assertions": ok, "accept_unrecognized": []any{map[string]any{"feature": "vxlan", "reason": "r"}}},
		"a text and a feature":          {"devices": dev(nil), "assertions": ok, "accept_unrecognized": []any{map[string]any{"feature": "glbp", "text": "x", "reason": "r"}}},
		"a question with no name":       {"devices": dev(nil), "assertions": []any{map[string]any{"question": map[string]any{}}}},
		"a question name as a path":     {"devices": dev(nil), "assertions": []any{map[string]any{"question": map[string]any{"name": "../routes"}}}},
		"a where holding a map":         {"devices": dev(nil), "assertions": []any{map[string]any{"question": map[string]any{"name": "routes", "where": map[string]any{"Node": map[string]any{}}}}}},
		"max_rows under min_rows":       {"devices": dev(nil), "assertions": []any{map[string]any{"question": map[string]any{"name": "routes", "min_rows": 2, "max_rows": 1}}}},
		"accepted_by on a drop":         {"devices": dev(nil), "assertions": []any{map[string]any{"reach": map[string]any{"from": "r1", "dst": "10.0.0.1", "delivered": false, "accepted_by": "r1"}}}},
	} {
		if _, err := readParams(nil, p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	req, err := readParams(nil, map[string]any{
		"devices": dev(nil),
		"scenarios": []any{map[string]any{"name": "s", "down_interfaces": []any{"R1[eth1]"}, "assertions": []any{
			map[string]any{"reach": map[string]any{"from": "r1[eth3]", "dst": "10.0.0.1", "avoids": []any{"R1"}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.devices[0].format != formatFRR || req.scenarios[0].interfaces[0].Hostname != "r1" || req.scenarios[0].assertions[0].from != "@enter(r1[eth3])" || !req.scenarios[0].assertions[0].delivered {
		t.Errorf("read %+v", req)
	}
}

// TestProtocolFamilyAndSecrets: Batfish's route types read as FRR's
// protocols, and an acceptance matches a line without its secret.
func TestProtocolFamilyAndSecrets(t *testing.T) {
	for in, want := range map[string]string{"ospfIA": "ospf", "ospfE2": "ospf", "ibgp": "bgp", "bgp": "bgp", "isisL2": "isis", "eigrpEX": "eigrp", "connected": "connected", "local": "local"} {
		if got := protocolFamily(in); got != want {
			t.Errorf("protocolFamily(%q) = %q, want %q", in, got, want)
		}
	}
	if got := withoutSecret("ip ospf authentication-key pleiades-masked-0123456789abcdef"); got != "ip ospf authentication-key" {
		t.Errorf("withoutSecret = %q", got)
	}
	if !strings.Contains(fileDevice("snapshot/configs/r1.cfg"), "r1") || fileDevice("configs/r2.cfg") != "r2" {
		t.Error("fileDevice")
	}
}

// TestUnmodelledIn finds each unmodelled feature a Cisco configuration
// uses, once, at its first line, and nothing in another format.
func TestUnmodelledIn(t *testing.T) {
	config := "hostname r1\nip sla 1\n icmp-echo 10.0.0.9\ntrack 1 ip sla 1 reachability\ninterface Gi1\n glbp 6 ip 10.0.0.253\n glbp 6 priority 200\n bfd interval 300 min_rx 300 multiplier 3\n"
	uses := unmodelledIn(formatCiscoIOS, config)
	if len(uses) != 4 || uses["glbp"].line != 6 || uses["ip_sla"].line != 2 || uses["sla_tracking"].line != 4 || uses["bfd"].text == "" {
		t.Errorf("uses = %+v", uses)
	}
	if u := unmodelledIn(formatCiscoIOS, "router bgp 1\n neighbor 10.0.0.2 fall-over bfd\n"); u["bfd"].line != 2 {
		t.Errorf("fall-over bfd = %+v", u)
	}
	if len(unmodelledIn(formatFRR, config)) != 0 {
		t.Error("a Cisco feature was found in an FRR configuration")
	}
	if renderCell(5.0) != "5" || renderCell(5) != "5" || renderCell(true) != "true" || renderCell([]any{"a"}) != `["a"]` {
		t.Error("renderCell")
	}
}

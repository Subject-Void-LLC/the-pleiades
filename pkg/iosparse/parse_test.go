// Package iosparse_test: the command forms the registry covers and refuses,
// the typed errors, and the parser paths no genieparser fixture reaches.
package iosparse_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
)

// TestParse_Refusals covers what Parse refuses before or instead of a
// result: commands no parser covers, BGP forms that need a second
// command, output with nothing in it, output genieparser itself fails on,
// and a result genieparser's schema refuses.
func TestParse_Refusals(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		output    string
		exception string // a *ParserError's Exception, when one is expected
		check     func(error) bool
	}{
		{name: "abbreviation", command: "sh ver", output: "x",
			check: func(err error) bool { var e *iosparse.UnsupportedCommandError; return errors.As(err, &e) }},
		{name: "another parser's command", command: "show interfaces status", output: "x",
			check: func(err error) bool { var e *iosparse.UnsupportedCommandError; return errors.As(err, &e) }},
		{name: "a pipe on a form without one", command: "show version | include uptime", output: "x",
			check: func(err error) bool { var e *iosparse.UnsupportedCommandError; return errors.As(err, &e) }},
		{name: "bgp vrf name holding rd", command: "show ip bgp vpnv4 vrf orders summary", output: "x",
			check: func(err error) bool {
				var e *iosparse.UnsupportedCommandError
				return errors.As(err, &e) && strings.Contains(e.Reason, "second command")
			}},
		{name: "bgp family word vrf", command: "show ip bgp vrf X summary", output: "x",
			check: func(err error) bool { var e *iosparse.UnsupportedCommandError; return errors.As(err, &e) }},
		{name: "whitespace only", command: "show version", output: " \r\n\t\n",
			check: func(err error) bool { return errors.Is(err, iosparse.ErrEmpty) }},
		{name: "nothing recognized", command: "show ip ospf neighbor", output: "no neighbors here\n",
			check: func(err error) bool { return errors.Is(err, iosparse.ErrEmpty) }},
		{
			// genieparser reads medium from the wrong match and raises
			// KeyError; the port keeps that.
			name: "medium line", command: "show interfaces", exception: "KeyError",
			output: "GigabitEthernet1 is up, line protocol is up\n  Encapsulation ARPA, medium is broadcast\n",
		},
		{
			// A host route with no "is subnetted" line before it reads
			// netmask before genieparser ever assigns it.
			name: "route before any mask", command: "show ip route", exception: "NameError",
			output: "C        10.4.1.1 is directly connected, Loopback0\n",
		},
		{
			name: "remote span range too wide", command: "show vlan", exception: "PortLimit",
			output: "Remote SPAN VLANs\n------\n1-999999999\n",
		},
		{
			// A CDP table with no "Total cdp entries" line lacks the key
			// genieparser's schema requires.
			name: "cdp without total", command: "show cdp neighbors",
			output: "R5.cisco.com Gig 0/0 125 R B Gig 0/0\n",
			check: func(err error) bool {
				var e *iosparse.SchemaError
				return errors.As(err, &e) && e.Parser == "ShowCdpNeighbors" && strings.Contains(e.Error(), "total_entries")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, parsed, err := iosparse.Parse(tt.command, tt.output)
			if err == nil {
				t.Fatalf("Parse = %v, want an error", parsed)
			}
			if tt.exception != "" {
				var pe *iosparse.ParserError
				if !errors.As(err, &pe) || pe.Exception != tt.exception {
					t.Fatalf("err = %v, want a ParserError raising %s", err, tt.exception)
				}
				return
			}
			if !tt.check(err) {
				t.Fatalf("err = %v (%T)", err, err)
			}
			if err.Error() == "" {
				t.Fatal("empty error text")
			}
		})
	}
}

// TestParse_UnsupportedNamesCommands proves the refusal names every
// covered command, which is what net.ios.parse shows its user.
func TestParse_UnsupportedNamesCommands(t *testing.T) {
	_, _, err := iosparse.Parse("show running-config", "x")
	for _, c := range iosparse.Commands() {
		if !strings.Contains(err.Error(), c) {
			t.Errorf("refusal does not name %q: %v", c, err)
		}
	}
	if len(iosparse.Commands()) == 0 {
		t.Fatal("no commands")
	}
}

// TestParse_CRLF proves output with "\r\n" line ends parses as the same
// output with "\n" does, as genieparser's harness reads its fixtures.
func TestParse_CRLF(t *testing.T) {
	out := "Neighbor ID     Pri   State           Dead Time   Address         Interface\n" +
		"10.1.1.1          1   FULL/DR         00:00:33    10.0.0.1        GigabitEthernet1\n"
	_, a, err := iosparse.Parse("show ip ospf neighbor", out)
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := iosparse.Parse("show ip ospf neighbor", strings.ReplaceAll(out, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d := firstDiff("", b, a); d != "" {
		t.Fatal(d)
	}
}

// TestShowIpRoute_DetailAndVia covers ShowIpRoute code no genieparser
// fixture reaches: the "show ip route <prefix>" detail lines (p100 to
// p900) and a "via ..." next-hop line (p6). The expected values were
// traced by hand through genieparser's ShowIpRoute.cli at the pinned
// commit, so they are reasoned rather than measured against Genie.
func TestShowIpRoute_DetailAndVia(t *testing.T) {
	detail := "Routing entry for 192.168.154.0/24\n" +
		"  Known via \"eigrp 1\", distance 130, metric 10880, type internal\n" +
		"  Redistributing via eigrp 1\n" +
		"  Last update from 192.168.151.2 on Vlan101, 2w3d ago\n" +
		"  Routing Descriptor Blocks:\n" +
		"  * 192.168.151.2, from 192.168.151.2, 2w3d ago, via Vlan101\n" +
		"      Route metric is 10880, traffic share count is 1\n" +
		"      Total delay is 20 microseconds, minimum bandwidth is 1000000 Kbit\n" +
		"      Reliability 255/255, minimum MTU 1500 bytes\n" +
		"      Loading 1/255, Hops 1\n"
	want := map[string]any{"vrf": map[string]any{"default": map[string]any{"address_family": map[string]any{"ipv4": map[string]any{
		"routes": map[string]any{"": map[string]any{
			"route": "192.168.154.0", "mask": "24", "active": true, "distance": 130.0, "metric": 10880.0,
			"type": "internal", "redist_via": "eigrp", "redist_via_tag": "1",
			"update": map[string]any{"from": "192.168.151.2", "interface": "Vlan101", "age": "2w3d"},
			"next_hop": map[string]any{"next_hop_list": map[string]any{"1": map[string]any{
				"index": 1.0, "next_hop": "192.168.151.2", "from": "192.168.151.2", "age": "2w3d",
				"outgoing_interface": "Vlan101", "metric": "10880", "share_count": "1", "total_delay": "20",
				"minimum_bandwidth": "1000000", "reliability": "255/255", "minimum_mtu": "1500",
				"loading": "1/255", "hops": "1",
			}}},
		}},
	}}}}}
	_, got, err := iosparse.Parse("show ip route", detail)
	if err != nil {
		t.Fatal(err)
	}
	if d := firstDiff("", normalize(t, got), want); d != "" {
		t.Fatal(d)
	}

	via := "Routing Table: VRF1\n" +
		"B        10.4.1.0/24 [200/0]\n" +
		"           via 10.4.1.1%default, indirectly connected\n"
	_, got, err = iosparse.Parse("show ip route vrf VRF1", via)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]any{"vrf": map[string]any{"VRF1": map[string]any{"address_family": map[string]any{"ipv4": map[string]any{
		"routes": map[string]any{"10.4.1.0/24": map[string]any{
			"route": "10.4.1.0/24", "active": true, "metric": 0.0, "route_preference": 200.0,
			"source_protocol_codes": "B", "source_protocol": "bgp",
			"next_hop": map[string]any{"next_hop_list": map[string]any{"1": map[string]any{
				"index": 1.0, "next_hop": "10.4.1.1", "vrf": "default",
			}}},
		}},
	}}}}}
	if d := firstDiff("", normalize(t, got), want); d != "" {
		t.Fatal(d)
	}
}

// TestCovers proves Covers names the parser Parse would run, and refuses
// as Parse refuses.
func TestCovers(t *testing.T) {
	if p, err := iosparse.Covers("show ip route vrf MGMT ospf"); err != nil || p != "ShowIpRoute" {
		t.Errorf("Covers = %q, %v", p, err)
	}
	if _, err := iosparse.Covers("show ip route 10.0.0.0"); err == nil {
		t.Error("a prefix after show ip route was covered; a different genieparser class parses it")
	}
}

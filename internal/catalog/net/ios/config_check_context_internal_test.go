// Package ios: net.ios.config's check following IOS out of a submode for a
// global command, and reading a switch's VLANs, which IOL keeps out of its
// running configuration, over a real SSH session to a scripted device. The
// lines are the CML lab's (proofs-mcast.yaml, 2026-11-17), and the VLAN
// table is SW-A's show vlan brief from that run.
package ios

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// iolSwitchRunning is an IOL L2 switch's running configuration as IOS XE
// 17.15 prints it in VTP server mode: no vlan block, though VLAN 50 exists.
const iolSwitchRunning = "Building configuration...\n\nCurrent configuration : 1180 bytes\n!\nhostname SW-A\n!\nvlan internal allocation policy ascending\n!\ninterface Ethernet0/0\n no switchport\n vrf forwarding Mgmt-intf\n ip address 192.0.2.5 255.255.255.0\n!\ninterface Ethernet0/1\n!\ninterface Ethernet0/2\n!\ninterface Vlan1\n no ip address\n shutdown\n!\nip forward-protocol nd\n!\nend"

// iolVLANBrief is SW-A's show vlan brief, as measured.
const iolVLANBrief = "\nVLAN Name                             Status    Ports\n---- -------------------------------- --------- -------------------------------\n1    default                          active    Et0/2, Et0/3, Et1/3\n50   host1-segment                    active    Et1/0, Et1/1, Et1/2\n1002 fddi-default                     act/unsup \n1003 token-ring-default               act/unsup \n1004 fddinet-default                  act/unsup \n1005 trnet-default                    act/unsup \n"

// switchDevice starts a scripted IOS switch whose show running-config
// answers running and whose show vlan brief answers vlans, or, when vlans
// is empty, is refused as a router refuses it.
func switchDevice(t *testing.T, running, vlans string) (*remoteexectest.Server, *secretContext, *sshDevice) {
	t.Helper()
	replies := map[string]string{"show running-config": running}
	unknown := ""
	if vlans != "" {
		replies["show vlan brief"] = vlans
	} else {
		unknown = "                ^\n% Invalid input detected at '^' marker.\n"
	}
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt:  "SW-A#",
		Config:  &remoteexectest.ConfigMode{Enter: "configure terminal", Exit: "end", Prompt: "SW-A(config)#"},
		Replies: replies,
		Unknown: unknown,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	device := &sshDevice{
		Stub: &inventorytest.Stub{StubName: "SW-A", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host, port: srv.Port,
	}
	return srv, &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}, device
}

// TestCheckConfig_AGlobalLineLeavesTheSubmode: the lab's lines, the trunk
// indented under its interface and the snooping lines at the margin, are
// predicted with the snooping lines at the top level, with or without the
// exit the lab wrote; VLAN 50, which the switch has and its configuration
// does not show, is no change. Each case runs against the scripted switch,
// which receives no configuration line.
func TestCheckConfig_AGlobalLineLeavesTheSubmode(t *testing.T) {
	trunk := []string{"interface Ethernet0/1", " +switchport trunk encapsulation dot1q", " +switchport mode trunk", "+ip igmp snooping", "+ip igmp snooping querier"}
	cases := []struct {
		name  string
		lines []any
		want  []string
	}{
		{"the lab's lines, with exit", []any{"vlan 50", "interface Ethernet0/1", " switchport trunk encapsulation dot1q", " switchport mode trunk", "exit", "ip igmp snooping", "ip igmp snooping querier"}, trunk},
		{"the lab's lines, without exit", []any{"vlan 50", "interface Ethernet0/1", " switchport trunk encapsulation dot1q", " switchport mode trunk", "ip igmp snooping", "ip igmp snooping querier"}, trunk},
		// Indented nowhere, a line found nowhere stays in the block: the
		// check does not guess which commands are global.
		{"indented nowhere", []any{"interface Ethernet0/1", "switchport mode trunk", "ip igmp snooping querier"}, []string{"interface Ethernet0/1", " +switchport mode trunk", " +ip igmp snooping querier"}},
		// Held at the top level, a global line is found there wherever it is
		// written, and the lines after it are looked for there.
		{"a global line the switch holds", []any{"interface Ethernet0/1", "ip forward-protocol nd", "shutdown"}, []string{"+shutdown"}},
		{"a no line the switch holds", []any{"interface Ethernet0/1", "no ip forward-protocol nd"}, []string{"-ip forward-protocol nd"}},
		// Only the whole command is a global one: a no line naming the start
		// of one stays in the submode.
		{"a no line naming the start of a global line", []any{"interface Ethernet0/2", "no ip forward-protocol"}, []string{"interface Ethernet0/2", " +no ip forward-protocol"}},
		{"a vlan's name as the switch has it", []any{"vlan 50", " name host1-segment"}, []string{}},
		{"a vlan renamed", []any{"vlan 50", " name other"}, []string{"vlan 50", " +name other"}},
		{"a vlan the switch lacks", []any{"vlan 60", " name new"}, []string{"+vlan 60", " +name new"}},
		{"the default vlan", []any{"vlan 1"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, rc, device := switchDevice(t, iolSwitchRunning, iolVLANBrief)
			res, err := CheckConfig(context.Background(), rc, device, sessionParams(map[string]any{paramLines: tc.lines}))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := rc.stats[statDifferences].([]string)
			if !slices.Equal(got, tc.want) || res.Changed != (len(tc.want) > 0) {
				t.Errorf("changed %v, differences %q; want %q", res.Changed, got, tc.want)
			}
			refuseConfiguration(t, srv.Commands(), tc.lines)
			// show vlan brief is read only for a vlan line the configuration
			// lacks.
			vlanLines := slices.ContainsFunc(tc.lines, func(l any) bool { return strings.HasPrefix(l.(string), "vlan ") })
			if read := slices.Contains(srv.Commands(), "show vlan brief"); read != vlanLines {
				t.Errorf("show vlan brief read %v, want %v (%q)", read, vlanLines, srv.Commands())
			}
		})
	}
}

// TestCheckConfig_ADeviceWithNoVLANTableSaysSo: a device that refuses show
// vlan brief has its vlan line predicted from the configuration alone, a
// change, and the run is told why.
func TestCheckConfig_ADeviceWithNoVLANTableSaysSo(t *testing.T) {
	_, rc, device := switchDevice(t, iolSwitchRunning, "")
	res, err := CheckConfig(context.Background(), rc, device, sessionParams(map[string]any{paramLines: []any{"vlan 50"}}))
	if err != nil || !res.Changed {
		t.Fatalf("changed %v, err %v", res.Changed, err)
	}
	if got, _ := rc.stats[statDifferences].([]string); !slices.Equal(got, []string{"+vlan 50"}) {
		t.Errorf("differences %q", got)
	}
	warns, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warns) != 1 || !strings.Contains(warns[0], "show vlan brief") || !strings.Contains(warns[0], "predicted to change") {
		t.Errorf("warnings %q", warns)
	}
}

// TestPredictLines_IndentationDecidesOnlyTheMargin: a line written less
// indented than an address family's lines, but not at the margin, stays in
// the address family, where IOS takes it, so the router's neighbor is a
// change there; a line at the margin is at the top level. And a VLAN table
// entry the configuration already holds is not added twice.
func TestPredictLines_IndentationDecidesOnlyTheMargin(t *testing.T) {
	roots := iosconfig.Parse(checkRunning)
	got := renderPredicted(predictLines(roots, []string{"router bgp 65000", " address-family ipv4", "  neighbor 192.0.2.1 activate", " neighbor 192.0.2.1 remote-as 65001", "hostname r1"}))
	if want := []string{"router bgp 65000", " address-family ipv4", "  +neighbor 192.0.2.1 remote-as 65001"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	held := iosconfig.Parse("vlan 50\n name held\n!\nend")
	if with := withVLANs(held, map[int]vlan{50: {"other", "active"}, 70: {"VLAN0070", "active"}}); len(with) != 2 || len(with[0].Children) != 1 || with[0].Children[0].Line != "name held" || with[1].Line != "vlan 70" || len(with[1].Children) != 0 {
		t.Errorf("withVLANs = %+v", with)
	}
	if vlans, err := parseVLANs(iolVLANBrief); err != nil || len(vlans) != 6 || vlans[50].name != "host1-segment" || vlans[1005].name != "trnet-default" {
		t.Errorf("parseVLANs = %v, %v", vlans, err)
	}
}

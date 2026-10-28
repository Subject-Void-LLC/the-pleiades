// Package ios: net.ios.config's check over a real SSH session, against a
// scripted device in configuration mode, proving what it predicts and that
// it sends the device no configuration line.
package ios

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// checkRunning is the running configuration the scripted device answers
// the checks with.
const checkRunning = "Building configuration...\n\nhostname r1\n!\nno ip domain lookup\n!\ninterface Loopback0\n description managed\n ip address 10.0.0.1 255.255.255.255\n!\ninterface Loopback1\n description same\n!\nrouter bgp 65000\n neighbor 192.0.2.1 remote-as 65001\n !\n address-family ipv4\n  neighbor 192.0.2.1 activate\n exit-address-family\n!\nend"

// TestCheckConfig_Registered: the check is declared, with its call-level
// answer, and no reason it cannot be checked.
func TestCheckConfig_Registered(t *testing.T) {
	d, ok := collection.Lookup(configFQCN)
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil || d.CheckCall == nil || d.Manifest.NoCheckReason != "" {
		t.Fatalf("%s: SupportsCheck %v, Check %v, CheckCall %v, NoCheckReason %q", configFQCN, d.Manifest.SupportsCheck, d.Check != nil, d.CheckCall != nil, d.Manifest.NoCheckReason)
	}
}

// TestCheckConfig_PredictsFromTheRunningConfigAndSendsNothing runs each
// check against the scripted device: lines already present predict no
// change, the rest are reported under the blocks they sit in, and the
// device receives show running-config and never configure terminal or any
// of the lines.
func TestCheckConfig_PredictsFromTheRunningConfigAndSendsNothing(t *testing.T) {
	cases := []struct {
		name  string
		lines []any
		want  []string
	}{
		{"already present", []any{"interface Loopback0", "description managed", "exit", "hostname r1", "no ip domain lookup"}, []string{}},
		{"partly present", []any{"interface Loopback0", "description new", "ip address 10.0.0.1 255.255.255.255"}, []string{"interface Loopback0", " +description new"}},
		{"a new block and its lines", []any{"interface Loopback9", "description x", "exit", "hostname r1"}, []string{"+interface Loopback9", " +description x"}},
		// IOS takes a line in the submode first, so a line found only in an
		// enclosing block is a change: a check errs toward change.
		{"a line found only in the enclosing block is a change", []any{"router bgp 65000", "address-family ipv4", "neighbor 192.0.2.1 remote-as 65001"}, []string{"router bgp 65000", " address-family ipv4", "  +neighbor 192.0.2.1 remote-as 65001"}},
		{"a top-level line written inside a block is a change", []any{"interface Loopback0", "hostname r1"}, []string{"interface Loopback0", " +hostname r1"}},
		{"a line of a new block is a change wherever else it is", []any{"interface Loopback9", "hostname r1"}, []string{"+interface Loopback9", " +hostname r1"}},
		{"a no line looks only in the current block", []any{"router bgp 65000", "address-family ipv4", "no neighbor 192.0.2.1 remote-as 65001"}, []string{"router bgp 65000", " address-family ipv4", "  +no neighbor 192.0.2.1 remote-as 65001"}},
		{"exit-address-family leaves the address family", []any{"router bgp 65000", "address-family ipv4", "neighbor 192.0.2.1 activate", "exit-address-family", "neighbor 192.0.2.1 remote-as 65001"}, []string{}},
		{"a second interface is looked for at the top level", []any{"interface Loopback0", "description managed", "interface Loopback1", "description same"}, []string{}},
		// The line is in the block the lines left, not the one they opened.
		{"a line of a new block is not found in the block before it", []any{"interface Loopback1", "description same", "interface Loopback9", "description same"}, []string{"+interface Loopback9", " +description same"}},
		{"a nested block", []any{"router bgp 65000", "address-family ipv4", "neighbor 192.0.2.1 activate", "neighbor 192.0.2.2 activate"}, []string{"router bgp 65000", " address-family ipv4", "  +neighbor 192.0.2.2 activate"}},
		{"a no line removes what it names", []any{"interface Loopback0", "no description"}, []string{"interface Loopback0", " -description managed"}},
		{"a no line with nothing printed is predicted to change", []any{"interface Loopback0", "no shutdown"}, []string{"interface Loopback0", " +no shutdown"}},
		{"exit leaves the block", []any{"interface Loopback0", "exit", "description managed"}, []string{"+description managed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, rc, device := backupDevice(t, checkRunning)
			res, err := CheckConfig(context.Background(), rc, device, sessionParams(map[string]any{paramLines: tc.lines}))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := rc.stats[statDifferences].([]string)
			if !slices.Equal(got, tc.want) || res.Changed != (len(tc.want) > 0) {
				t.Errorf("changed %v, differences %q; want %q", res.Changed, got, tc.want)
			}
			refuseConfiguration(t, srv.Commands(), tc.lines)
		})
	}
}

// refuseConfiguration fails the test when commands holds configure
// terminal or any of lines.
func refuseConfiguration(t *testing.T, commands []string, lines []any) {
	t.Helper()
	if !slices.Contains(commands, "show running-config") {
		t.Errorf("the check never read the configuration: %q", commands)
	}
	for _, c := range commands {
		if c == "configure terminal" || slices.Contains(lines, any(c)) {
			t.Errorf("the check sent %q (all: %q)", c, commands)
		}
	}
}

// TestCheckConfig_MasksAsTheBackupDoes: a predicted line holding a secret
// written in clear, and the backup a check also takes, are recorded with
// the secret masked.
func TestCheckConfig_MasksAsTheBackupDoes(t *testing.T) {
	srv, rc, device := backupDevice(t, checkRunning)
	lines := []any{"username ops secret 0 Pr3dicted5ecret", "snmp-server community pub RO"}
	res, err := CheckConfig(context.Background(), rc, device, sessionParams(map[string]any{paramLines: lines, paramBackup: true}))
	if err != nil || !res.Changed {
		t.Fatalf("changed %v, err %v", res.Changed, err)
	}
	got, _ := rc.stats[statDifferences].([]string)
	if len(got) != 2 || !strings.HasPrefix(got[0], "+username ops secret 0 "+netcfg.MaskPrefix) || !strings.Contains(got[1], netcfg.MaskPrefix) {
		t.Errorf("differences %q are not the masked lines", got)
	}
	for _, secret := range []string{"Pr3dicted5ecret", " pub "} {
		if strings.Contains(strings.Join(got, "\n"), secret) {
			t.Errorf("differences kept %q: %q", secret, got)
		}
	}
	if backup, _ := rc.stats[statBackup].(string); !strings.Contains(backup, "hostname r1") {
		t.Errorf("a check with backup recorded no backup: %q", backup)
	}
	refuseConfiguration(t, srv.Commands(), lines)
}

// TestCheckConfig_ACallItCannotPredictIsUncheckedBeforeContact: a secret
// reference, prompts, a key line, a banner and end each make the call
// unchecked, from the parameters alone (CheckCall gives the same answer),
// and the device is never reached.
func TestCheckConfig_ACallItCannotPredictIsUncheckedBeforeContact(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
	}{
		{"a secret reference", map[string]any{paramLines: []any{"crypto ikev2 keyring K", "peer p", "pre-shared-key {{ secret.ike.key }}"}}},
		{"prompts", map[string]any{paramLines: []any{"hostname r2"}, "prompts": []any{map[string]any{"match": "x", "answer": "y"}}}},
		{"a key line", map[string]any{paramLines: []any{"crypto key generate rsa modulus 2048"}}},
		{"a banner", map[string]any{paramLines: []any{"banner motd ^C hello ^C"}}},
		{"end", map[string]any{paramLines: []any{"hostname r2", "end", "write memory"}}},
		{"a do line", map[string]any{paramLines: []any{"hostname r2", "do write memory"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, rc, device := backupDevice(t, checkRunning)
			_, err := CheckConfig(context.Background(), rc, device, sessionParams(tc.params))
			var cannot *collection.CannotCheckError
			if !errors.As(err, &cannot) {
				t.Fatalf("err %v, want a CannotCheck", err)
			}
			if callErr := configCheckCall(tc.params); callErr == nil || callErr.Error() != err.Error() {
				t.Errorf("CheckCall answered %v, the check %v", callErr, err)
			}
			if strings.Contains(err.Error(), "secret.ike") {
				t.Errorf("the answer quoted the reference: %v", err)
			}
			if got := srv.Commands(); len(got) != 0 {
				t.Errorf("the device was reached: %q", got)
			}
		})
	}
	if err := configCheckCall(map[string]any{paramLines: []any{"hostname r2"}}); err != nil {
		t.Errorf("an ordinary call cannot be checked: %v", err)
	}
}

// TestPredictLines_NewBlocksAndNestedOpeners covers what the walk takes for
// a new block without a device: a top-level opener the configuration has
// none of, and an address family under a router that has none yet.
func TestPredictLines_NewBlocksAndNestedOpeners(t *testing.T) {
	roots := iosconfig.Parse("router ospf 1\n network 10.0.0.0 0.0.0.255 area 0\n!\nend")
	got := renderPredicted(predictLines(roots, []string{"ip access-list extended A", "permit ip any any", "router ospf 1", "address-family ipv4 unicast", "network 10.0.0.0 0.0.0.255 area 0"}))
	want := []string{"+ip access-list extended A", " +permit ip any any", "router ospf 1", " +address-family ipv4 unicast", "  +network 10.0.0.0 0.0.0.255 area 0"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// A whole block a no line removes is shown removed, line by line.
	got = renderPredicted(predictLines(roots, []string{"no router ospf 1"}))
	if want := []string{"-router ospf 1", " -network 10.0.0.0 0.0.0.255 area 0"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestPredictLines_ANeighborInAVRFIsNotTheRoutersNeighbor is the review's
// case: the neighbor is configured under router bgp 1 but not in the VRF's
// address family, where IOS adds it, so the line is a change there; and
// another VRF's address family is still found under the router.
func TestPredictLines_ANeighborInAVRFIsNotTheRoutersNeighbor(t *testing.T) {
	roots := iosconfig.Parse("router bgp 1\n neighbor 10.0.0.2 remote-as 2\n !\n address-family ipv4 vrf RED\n  neighbor 10.0.0.9 activate\n exit-address-family\n !\n address-family ipv4 vrf BLUE\n  neighbor 10.0.0.2 remote-as 2\n exit-address-family\n!\nend")
	got := renderPredicted(predictLines(roots, []string{"router bgp 1", "address-family ipv4 vrf RED", "neighbor 10.0.0.2 remote-as 2", "address-family ipv4 vrf BLUE", "neighbor 10.0.0.2 remote-as 2"}))
	want := []string{"router bgp 1", " address-family ipv4 vrf RED", "  +neighbor 10.0.0.2 remote-as 2"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// renderPredicted writes predicted lines with their signs, unmasked.
func renderPredicted(p []predictedLine) []string {
	out := []string{}
	for _, l := range p {
		out = append(out, strings.Repeat(" ", l.depth)+l.sign+l.line)
	}
	return out
}

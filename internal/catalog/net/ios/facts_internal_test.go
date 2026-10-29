// Package ios: net.ios.facts against output captured from a real IOL XE
// router, and the config subset's masking.
//
// The fixtures in testdata/facts/iol-xe-17.15.1 are R1 of the DevNet CML
// sandbox's default-lab (IOL XE 17.15.1), captured 2026-10-27 through the
// end-user path alone: pleiades import-cml, then pleiades adhoc R1
// net.ios.command command=<show command> --json, read-only. They are
// verbatim except where a secret could stand: every value pkg/netcfg masked
// was replaced by REDACTED-AT-CAPTURE before the file was written, as was
// the word after "15" on the admin user's line, since a type 0 password
// runs to the end of its line and the 15 may be part of it; carriage
// returns (a Telnet session ends each line with one) were dropped; and the
// "********" in show version and on one username line is the run's own
// masking of a word equal to the node's lab password, kept as printed.
package ios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// iolFixture reads one captured command's output.
func iolFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "facts", "iol-xe-17.15.1", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// userModeRefusal is what IOL XE 17.15.1 printed for show running-config
// to an account in user mode, measured through pleiades adhoc.
const userModeRefusal = "% Invalid input detected at '^' marker."

// iolSession answers every command net.ios.facts sends as R1 did.
func iolSession(t *testing.T) *fakeSession {
	t.Helper()
	return &fakeSession{outputs: map[string]string{
		"show version":            iolFixture(t, "show_version"),
		"show inventory":          iolFixture(t, "show_inventory"),
		"show ip interface brief": iolFixture(t, "show_ip_interface_brief"),
		"show interfaces":         iolFixture(t, "show_interfaces"),
		"show vrf":                iolFixture(t, "show_vrf"),
		"show ip vrf brief":       iolFixture(t, "show_ip_vrf_brief"),
		showRunningConfig:         iolFixture(t, "show_running_config"),
	}}
}

// runSession runs Facts against session with params.
func runSession(t *testing.T, session *fakeSession, params map[string]any) (*stubContext, error) {
	t.Helper()
	withSession(t, session)
	rc := newStubContext()
	_, err := Facts(context.Background(), rc, nil, params)
	return rc, err
}

// TestParseVersion_ReadsBothPlatforms is the min subset's parser against
// both captured platforms: IOL XE prints no "Cisco IOS XE Software" line and
// names no model, so its version comes from the IOS line and it has no
// model at all.
func TestParseVersion_ReadsBothPlatforms(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want map[string]any
	}{
		{name: "Catalyst 8000V, IOS XE 17.15.04c", out: realShowVersion, want: map[string]any{
			factHostname: "Cat8kv", factUptime: "1 hour, 32 minutes", factVersion: "17.15.04c",
			factModel: "C8000V", factSerialNum: "9XLWRY40A2V", factImage: "bootflash:packages.conf",
		}},
		{name: "IOL XE 17.15.1", out: iolFixture(t, "show_version"), want: map[string]any{
			factHostname: "R1", factUptime: "2 hours, 53 minutes", factVersion: "17.15.1",
			factSerialNum: "131184643", factImage: "unix:/x86_64_crb_linux-adventerprisek9-ms.iol",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseVersion(tc.out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseVersion = %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestParseInterfaceDetails_ReadsShowInterfaces is show interfaces' parser
// against R1's four interfaces: one with a description, one shut down.
func TestParseInterfaceDetails_ReadsShowInterfaces(t *testing.T) {
	want := []interfaceDetail{
		{name: "Ethernet0/0", status: "up", lineProtocol: "up", mtu: 1500},
		{name: "Ethernet0/1", status: "up", lineProtocol: "up", mtu: 1500},
		{name: "Ethernet0/2", status: "up", lineProtocol: "up", mtu: 1500, description: "to port1.sandbox-backend"},
		{name: "Ethernet0/3", status: "administratively down", lineProtocol: "down", mtu: 1500},
	}
	for _, tc := range []struct{ name, out string }{
		{name: "as captured", out: iolFixture(t, "show_interfaces")},
		{name: "with the carriage returns Telnet sends", out: strings.ReplaceAll(iolFixture(t, "show_interfaces"), "\n", "\r\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseInterfaceDetails(strings.ReplaceAll(tc.out, "\r", ""))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("parseInterfaceDetails = %+v\nwant %+v", got, want)
			}
		})
	}
}

// TestParseVRFs reads both VRF commands' real output, and the shapes no
// capture holds yet, each said as such: a VRF with a second interface on a
// continuation line (IOS prints one interface a line), a VRF with none,
// a device with no VRF at all, and output with no header to read by.
func TestParseVRFs(t *testing.T) {
	const twoInterfaces = "  Name                             Default RD            Protocols   Interfaces\n" +
		"  Mgmt-intf                        <not set>             ipv4,ipv6   Gi1\n" +
		"  blue                             65000:1               ipv4        Lo10\n" +
		"                                                                     Gi2.100\n" +
		"  empty                            65000:2               ipv4        \n"
	for _, tc := range []struct {
		name    string
		out     string
		want    []vrfMember
		refused bool
	}{
		{name: "show vrf, captured", out: iolFixture(t, "show_vrf"), want: []vrfMember{{vrf: "Mgmt-intf", iface: "Et0/2"}}},
		{name: "show ip vrf brief, captured", out: iolFixture(t, "show_ip_vrf_brief"), want: []vrfMember{{vrf: "Mgmt-intf", iface: "Et0/2"}}},
		{name: "a continuation line and an empty VRF, not captured", out: twoInterfaces, want: []vrfMember{
			{vrf: "Mgmt-intf", iface: "Gi1"}, {vrf: "blue", iface: "Lo10"}, {vrf: "blue", iface: "Gi2.100"},
		}},
		{name: "no VRF at all", out: "\n"},
		{name: "no header", out: "Mgmt-intf <not set> ipv4 Et0/2\n", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVRFs(tc.out)
			if (err != nil) != tc.refused {
				t.Fatalf("err = %v, want refused %v", err, tc.refused)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseVRFs = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestMatchInterface: a VRF table's abbreviation names the one interface it
// can stand for, and none when it could stand for several or for none.
func TestMatchInterface(t *testing.T) {
	interfaces := []map[string]any{
		{ifName: "Ethernet0/2"}, {ifName: "GigabitEthernet1"}, {ifName: "GigabitEthernet2.100"},
		{ifName: "Loopback10"}, {ifName: "Port-channel1"}, {ifName: "Tunnel1"}, {ifName: "TenGigabitEthernet1"},
	}
	for _, tc := range []struct{ abbrev, want string }{
		{"Et0/2", "Ethernet0/2"},
		{"Gi1", "GigabitEthernet1"},
		{"Gi2.100", "GigabitEthernet2.100"},
		{"Lo10", "Loopback10"},
		{"Po1", "Port-channel1"},
		{"Te1", "TenGigabitEthernet1"},
		{"GigabitEthernet1", "GigabitEthernet1"},
		{"T1", ""},  // Tunnel1 and TenGigabitEthernet1 both start with T
		{"Gi9", ""}, // no such interface
	} {
		got := matchInterface(interfaces, tc.abbrev)
		name, _ := got[ifName].(string)
		if name != tc.want {
			t.Errorf("matchInterface(%q) = %q, want %q", tc.abbrev, name, tc.want)
		}
	}
}

// TestFacts_InterfacesJoinTheThreeCommands is the interfaces subset over
// R1's own replies: each interface carries its brief address and states,
// show interfaces' description, MTU and states, and its VRF only when it is
// in one.
func TestFacts_InterfacesJoinTheThreeCommands(t *testing.T) {
	rc, err := runSession(t, iolSession(t), map[string]any{paramGatherSubset: []any{subsetInterfaces}})
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	got, _ := rc.stats[factInterfaces].([]map[string]any)
	want := []map[string]any{
		{ifName: "Ethernet0/0", ifIPAddress: "10.10.10.100", ifStatus: "up", ifProtocol: "up", ifMTU: 1500, ifAdminState: "up", ifLineProtocol: "up"},
		{ifName: "Ethernet0/1", ifIPAddress: "1.1.1.1", ifStatus: "up", ifProtocol: "up", ifMTU: 1500, ifAdminState: "up", ifLineProtocol: "up"},
		{ifName: "Ethernet0/2", ifIPAddress: "10.10.20.171", ifStatus: "up", ifProtocol: "up", ifMTU: 1500, ifAdminState: "up", ifLineProtocol: "up",
			ifDescription: "to port1.sandbox-backend", ifVRF: "Mgmt-intf"},
		{ifName: "Ethernet0/3", ifStatus: "administratively down", ifProtocol: "down", ifMTU: 1500, ifAdminState: "down", ifLineProtocol: "down"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s =\n%v\nwant\n%v", factInterfaces, got, want)
	}
	if _, set := rc.stats[sdk.StatWarnings]; set {
		t.Errorf("warnings for a device that answered everything: %v", rc.stats[sdk.StatWarnings])
	}
}

// TestFacts_TheVRFCommandsFallBackAndWarn: a device refusing show vrf is
// read through show ip vrf brief, one refusing both still returns its
// interfaces with a warning saying none has a vrf, and a VRF member that is
// no listed interface is warned about rather than dropped silently.
func TestFacts_TheVRFCommandsFallBackAndWarn(t *testing.T) {
	interfacesOnly := map[string]any{paramGatherSubset: []any{subsetInterfaces}}
	t.Run("show vrf refused", func(t *testing.T) {
		s := iolSession(t)
		s.outputs["show vrf"] = userModeRefusal
		rc, err := runSession(t, s, interfacesOnly)
		if err != nil {
			t.Fatal(err)
		}
		if got := rc.stats[factInterfaces].([]map[string]any)[2][ifVRF]; got != "Mgmt-intf" {
			t.Errorf("Ethernet0/2 vrf = %v, want it read from show ip vrf brief", got)
		}
	})
	t.Run("both refused", func(t *testing.T) {
		s := iolSession(t)
		s.outputs["show vrf"], s.outputs["show ip vrf brief"] = userModeRefusal, userModeRefusal
		rc, err := runSession(t, s, interfacesOnly)
		if err != nil {
			t.Fatal(err)
		}
		if w := fmt.Sprint(rc.stats[sdk.StatWarnings]); !strings.Contains(w, "neither show vrf nor show ip vrf brief") {
			t.Errorf("warnings = %v", w)
		}
		if _, set := rc.stats[factInterfaces].([]map[string]any)[2][ifVRF]; set {
			t.Error("a vrf was reported with no VRF command answered")
		}
	})
	t.Run("an unlisted member", func(t *testing.T) {
		s := iolSession(t)
		s.outputs["show vrf"] = iolFixture(t, "show_vrf") + "  red                              65000:9               ipv4        Gi7\n"
		rc, err := runSession(t, s, interfacesOnly)
		if err != nil {
			t.Fatal(err)
		}
		if w := fmt.Sprint(rc.stats[sdk.StatWarnings]); !strings.Contains(w, "VRF red names interface Gi7") {
			t.Errorf("warnings = %v", w)
		}
	})
}

// TestFacts_TheDefaultGathersTheWholeDevice: with no gather_subset, a
// privileged session returns every subset, every value as a stat and every
// value but the configuration as a fact too, each under a name Doc.Returns
// declares (the journal keeps only declared names).
func TestFacts_TheDefaultGathersTheWholeDevice(t *testing.T) {
	rc, err := runSession(t, iolSession(t), nil)
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if got := rc.stats[factGatherSubset]; !reflect.DeepEqual(got, []string{subsetConfig, subsetInterfaces, subsetMin}) {
		t.Errorf("%s = %v, want every subset", factGatherSubset, got)
	}
	for _, name := range []string{factHostname, factVersion, factInterfaces, factConfig, factGatherSubset} {
		if _, ok := rc.stats[name]; !ok {
			t.Errorf("no stat %s", name)
		}
	}
	for name, v := range rc.facts {
		if !reflect.DeepEqual(rc.stats[name], v) {
			t.Errorf("fact %s differs from its stat", name)
		}
	}
	if _, ok := rc.facts[factConfig]; ok {
		t.Error("the configuration was emitted as a fact: its placeholders change every run")
	}
	d, _ := collection.Lookup("net.ios.facts")
	declared := map[string]bool{}
	for _, r := range d.Manifest.Doc.Returns {
		declared[r.Name] = true
	}
	for name := range rc.stats {
		if !declared[name] {
			t.Errorf("stat %s is not declared in Doc.Returns", name)
		}
	}
	if !declared[sdk.StatWarnings] {
		t.Errorf("Doc.Returns does not declare %s", sdk.StatWarnings)
	}
}

// TestFacts_ConfigInUserMode: an account in user mode cannot show the
// running configuration. Under all, the default, the subset is left out
// with a warning naming the enable secret; named, the refusal fails the
// task.
func TestFacts_ConfigInUserMode(t *testing.T) {
	s := iolSession(t)
	s.outputs[showRunningConfig] = userModeRefusal
	rc, err := runSession(t, s, nil)
	if err != nil {
		t.Fatalf("Facts under all: %v", err)
	}
	if _, set := rc.stats[factConfig]; set {
		t.Error("a refused configuration was returned")
	}
	if got := rc.stats[factGatherSubset]; !reflect.DeepEqual(got, []string{subsetInterfaces, subsetMin}) {
		t.Errorf("%s = %v, want config left out", factGatherSubset, got)
	}
	if w := fmt.Sprint(rc.stats[sdk.StatWarnings]); !strings.Contains(w, "--enable-password-stdin") {
		t.Errorf("warnings = %v, want the enable secret named", w)
	}

	s = iolSession(t)
	s.outputs[showRunningConfig] = userModeRefusal
	if _, err := runSession(t, s, map[string]any{paramGatherSubset: []any{subsetConfig}}); err == nil || !strings.Contains(err.Error(), "device rejected") {
		t.Errorf("config named in user mode: err = %v, want the refusal", err)
	}
}

// TestFacts_ConfigNeverReturnsASecret feeds the config subset a running
// configuration holding each secret form a router carries (an enable
// secret, a local user's secret, IKEv1 and IKEv2 pre-shared keys, SNMP
// communities, a TACACS+ key, OSPF and BGP keys) beside R1's real one, and
// holds every value recorded, stat or fact, to holding none of them.
func TestFacts_ConfigNeverReturnsASecret(t *testing.T) {
	secrets := map[string]string{
		"enable secret 9 %s":                                             "$9$EnableHashValue01",
		"username netops privilege 15 secret 9 %s":                       "$9$UserHashValue0002",
		"crypto isakmp key %s address 10.0.0.2":                          "IsakmpPsk-Value03",
		"  pre-shared-key local %s":                                      "Ikev2LocalPsk04",
		"  pre-shared-key remote %s":                                     "Ikev2RemotePsk05",
		"snmp-server community %s RO":                                    "SnmpCommunity06",
		"snmp-server host 10.0.0.9 version 2c %s":                        "SnmpTrapComm07",
		"tacacs server t1\n key 7 %s":                                    "0822455D0A16544541",
		" ip ospf message-digest-key 1 md5 %s":                           "OspfMd5Key0009",
		" neighbor 10.0.0.2 password 7 %s":                               "BgpNeighbor0010",
		"line vty 0 4\n password %s":                                     "Sh0rtPw",
		"crypto ikev2 profile p\n authentication local pre-share key %s": "ProfileKey0012",
	}
	var config strings.Builder
	real := iolFixture(t, "show_running_config")
	config.WriteString(strings.TrimSuffix(strings.TrimRight(real, "\n"), "end"))
	for format, secret := range secrets {
		config.WriteString(fmt.Sprintf(format, secret) + "\n")
	}
	config.WriteString("end\n")

	s := iolSession(t)
	s.outputs[showRunningConfig] = config.String()
	rc, err := runSession(t, s, map[string]any{paramGatherSubset: []any{subsetConfig}})
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	got, _ := rc.stats[factConfig].(string)
	if !strings.Contains(got, "hostname R1") || !strings.Contains(got, netcfg.MaskPrefix) {
		t.Fatalf("the configuration is not the device's, masked: %d bytes", len(got))
	}
	recorded := fmt.Sprint(rc.stats) + fmt.Sprint(rc.facts)
	for _, secret := range secrets {
		if strings.Contains(recorded, secret) {
			t.Errorf("a secret of %d characters survived into what was recorded", len(secret))
		}
	}
	// What the capture replaced its own secrets with is masked as a secret.
	if strings.Contains(recorded, "REDACTED-AT-CAPTURE") {
		t.Error("a secret position in R1's own configuration was returned unmasked")
	}
}

// TestFacts_ConfigItCannotVouchForIsNotReturned: text cut short of its
// "end" line, and a line still holding a second secret after masking, each
// fail the subset, return none of the text, and name no secret.
func TestFacts_ConfigItCannotVouchForIsNotReturned(t *testing.T) {
	for _, tc := range []struct{ name, config, says string }{
		{name: "cut short", config: "hostname R1\nenable secret 9 $9$CutShortSecret\ninterface Ethernet0/0\n", says: `"end" line`},
		{name: "a second secret on one line", config: "hostname R1\nradius-server host 10.0.0.1 key 7 FirstSecret123 pac key 7 SecondSecret456\nend\n", says: "cannot vouch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := iolSession(t)
			s.outputs[showRunningConfig] = tc.config
			for _, params := range []map[string]any{nil, {paramGatherSubset: []any{subsetConfig}}} {
				rc, err := runSession(t, s, params)
				if err == nil || !strings.Contains(err.Error(), tc.says) {
					t.Fatalf("err = %v, want one saying %q", err, tc.says)
				}
				for _, secret := range []string{"CutShortSecret", "FirstSecret123", "SecondSecret456"} {
					if strings.Contains(err.Error()+fmt.Sprint(rc.stats)+fmt.Sprint(rc.facts), secret) {
						t.Error("a refusal carried a secret")
					}
				}
				if _, set := rc.stats[factConfig]; set {
					t.Error("text the subset refused was recorded")
				}
			}
		})
	}
}

// TestFacts_CheckIsTheSameRead: a check runs the method itself, since it
// only reads.
func TestFacts_CheckIsTheSameRead(t *testing.T) {
	d, ok := collection.Lookup("net.ios.facts")
	if !ok || d.Check == nil || !d.Manifest.SupportsCheck || !d.Manifest.Reversibility.ReadOnly {
		t.Fatalf("net.ios.facts must declare a read-only check: %+v", d.Manifest)
	}
	withSession(t, iolSession(t))
	if result, err := d.Check(context.Background(), newStubContext(), inventory.InventoryItem(nil), nil); err != nil || result.Changed {
		t.Errorf("check = %+v, %v", result, err)
	}
}

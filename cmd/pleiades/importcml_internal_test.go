// Tests of import-cml's decisions, each on values a controller or a person
// could hand it.
package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
)

// addr is a test address.
func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// TestCMLAddress_NeverGuessesFromAConfiguration: --address wins, one
// observed address is taken, and several, none or an unmanaged node are
// skipped saying why, with a configuration's addresses only as a hint.
func TestCMLAddress_NeverGuessesFromAConfiguration(t *testing.T) {
	given := addressFlags{"R1": addr("10.10.20.171")}
	for name, tc := range map[string]struct {
		node       cml.Node
		addr, skip string
	}{
		"--address":          {cml.Node{Label: "R1", NodeDefinition: "iol-xe", ConfiguredAddresses: []netip.Addr{addr("1.1.1.1")}}, "10.10.20.171", ""},
		"one observed":       {cml.Node{Label: "R2", Addresses: []netip.Addr{addr("10.10.20.172")}}, "10.10.20.172", ""},
		"several observed":   {cml.Node{Label: "R3", Addresses: []netip.Addr{addr("10.0.0.1"), addr("10.0.0.2")}}, "", "several addresses on it (10.0.0.1, 10.0.0.2); pass --address R3=<one>"},
		"none, with a hint":  {cml.Node{Label: "SW1", ConfiguredAddresses: []netip.Addr{addr("10.10.20.173")}}, "", "observed no address on it; its configuration names 10.10.20.173: pass --address SW1=<one>"},
		"none, and no hint":  {cml.Node{Label: "PC1"}, "", "observed no address on it"},
		"an external bridge": {cml.Node{Label: "ext-conn-0", NodeDefinition: "external_connector"}, "", "no management address"},
	} {
		a, _, skip := cmlAddress(tc.node, given)
		if (tc.addr != "" && a != addr(tc.addr)) || !strings.Contains(skip, tc.skip) || (tc.skip == "") != (skip == "") {
			t.Errorf("%s: cmlAddress = %v, %q; want %s, %q", name, a, skip, tc.addr, tc.skip)
		}
	}
}

// TestCMLSelect_RefusesWhatItCannotImport: sorted labels, --nodes, and a
// refusal for a label a device cannot take, a shared label, a node the lab
// lacks, and an --address for a node the lab lacks or that has no
// management side.
func TestCMLSelect_RefusesWhatItCannotImport(t *testing.T) {
	nodes := []cml.Node{{Label: "R2"}, {Label: "R1"}, {Label: "ext-conn-0", NodeDefinition: "external_connector"}}
	got, err := cmlSelect(nodes, "lab", "", addressFlags{})
	if err != nil || len(got) != 3 || got[0].Label != "R1" {
		t.Fatalf("every node = %+v, %v", got, err)
	}
	if got, err := cmlSelect(nodes, "lab", "R2", addressFlags{}); err != nil || len(got) != 1 || got[0].Label != "R2" {
		t.Errorf("--nodes R2 = %+v, %v", got, err)
	}
	for name, tc := range map[string]struct {
		nodes     []cml.Node
		only      string
		addresses addressFlags
		want      string
	}{
		"a node it lacks":             {nodes, "R9", addressFlags{}, `no node "R9"`},
		"a label with a space":        {[]cml.Node{{Label: "PC 1"}}, "", addressFlags{}, "not a name a device can take"},
		"a shared label":              {[]cml.Node{{Label: "R1"}, {Label: "R1"}}, "", addressFlags{}, "two nodes"},
		"an --address for no node":    {nodes, "", addressFlags{"R9": addr("10.0.0.9")}, "--address names R9"},
		"an --address for the bridge": {nodes, "", addressFlags{"ext-conn-0": addr("10.0.0.9")}, "has no management address"},
	} {
		if _, err := cmlSelect(tc.nodes, "lab", tc.only, tc.addresses); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// TestCMLSpec_ADeviceTypeForEachNodeAndCarrier: an IOS node definition is
// a Cisco device, reached over SSH or, with --via telnet, over Telnet; any
// other is generic_ssh or a console device.
func TestCMLSpec_ADeviceTypeForEachNodeAndCarrier(t *testing.T) {
	at := addr("10.10.20.171")
	for name, tc := range map[string]struct {
		def, via, wantType, wantKey string
	}{
		"a router over ssh":    {"iol-xe", "ssh", "cisco_router", "port"},
		"a router over telnet": {"cat8000v", "telnet", "cisco_router", "transport"},
		"a switch over telnet": {"ioll2-xe", "telnet", "cisco_switch", "transport"},
		"a PC over ssh":        {"desktop", "ssh", generic.TypeSSH, "port"},
		"a PC over telnet":     {"desktop", "telnet", gns3DeviceType, "telnet_port"},
	} {
		typ, props := cmlSpec(cml.Node{NodeDefinition: tc.def}, at, tc.via)
		if typ != tc.wantType || props[tc.wantKey] == nil {
			t.Errorf("%s: %s %v, want a %s with %s", name, typ, props, tc.wantType, tc.wantKey)
		}
	}
}

// TestAddressFlags_OneAddressPerNode: NODE=IP, a node once, and nothing
// else.
func TestAddressFlags_OneAddressPerNode(t *testing.T) {
	a := addressFlags{}
	if err := a.Set("R1=10.10.20.171"); err != nil || a["R1"] != addr("10.10.20.171") {
		t.Fatalf("Set = %v, %v", err, a)
	}
	for _, bad := range []string{"R1=10.10.20.172", "R2", "=10.0.0.1", "R2=router"} {
		if err := a.Set(bad); err == nil {
			t.Errorf("Set(%q) accepted", bad)
		}
	}
	if !strings.Contains(a.String(), "R1") {
		t.Errorf("String = %q", a.String())
	}
}

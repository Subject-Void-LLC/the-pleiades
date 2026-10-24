// Tests of import-cml's decisions, each on values a controller or a person
// could hand it.
package main

import (
	"context"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
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

// TestCMLSpec_AReplaceOverSSHUndoesTelnet: a Cisco node imported over
// Telnet and then again over SSH with --replace is reached over SSH.
func TestCMLSpec_AReplaceOverSSHUndoesTelnet(t *testing.T) {
	n := cml.Node{Label: "R1", NodeDefinition: "iol-xe"}
	at := addr("10.10.20.171")
	typ, props := cmlSpec(n, at, "telnet")
	hosts, err := labPlace(nil, inventory.HostSpec{ID: "r1", Name: "R1", Type: typ, Properties: props}, false)
	if err != nil {
		t.Fatal(err)
	}
	typ, props = cmlSpec(n, at, "ssh")
	hosts, err = labPlace(hosts, inventory.HostSpec{Name: "R1", Type: typ, Properties: props}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := hosts[0].Properties["transport"]; got != "ssh" {
		t.Errorf("transport after replacing over SSH = %v, want ssh", got)
	}
}

// TestImportCML_ThroughAControllerDevice: with --controller the import
// logs in to the controller a cml_controller device of the project stands
// for, with its stored login and its pinned certificate, and imports the
// lab's node as a person's flags would have; --controller beside a flag it
// replaces, a device that is not there, and one with no login stored are
// refused.
func TestImportCML_ThroughAControllerDevice(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := client.CreateLab(ctx, "cvd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	pin := devicetls.Fingerprint(ctl.Certificate())
	for _, step := range [][]string{
		{"init", "--dir", dir},
		{"add-host", "cml", "--dir", dir, "--type", "cml_controller", "--set", "api_base=" + ctl.URL, "--set", "tls_fingerprint=" + pin},
		{"add-host", "lonely", "--dir", dir, "--type", "cml_controller", "--set", "api_base=" + ctl.URL, "--set", "tls_fingerprint=" + pin},
		{"add-credential", "cml", "--dir", dir, "--username", "admin", "--password", "pw"},
	} {
		run := map[string]func([]string) error{"init": runInit, "add-host": runAddHost, "add-credential": runAddCredential}[step[0]]
		if err := run(step[1:]); err != nil {
			t.Fatalf("%s: %v", strings.Join(step, " "), err)
		}
	}
	if err := runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--address", "R1=10.10.20.171", "--via", "telnet"}); err != nil {
		t.Fatal(err)
	}
	hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hosts {
		found = found || h.Name == "R1" && h.Type == "cisco_router"
	}
	if !found {
		t.Errorf("R1 was not imported: %+v", hosts)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"beside --server":  {[]string{"--controller", "cml", "--server", ctl.URL, "--lab", "cvd"}, "give none of"},
		"no such device":   {[]string{"--controller", "nothing", "--lab", "cvd"}, "--controller nothing"},
		"no login stored":  {[]string{"--controller", "lonely", "--lab", "cvd"}, "no login stored"},
		"no lab":           {[]string{"--controller", "cml"}, "--lab is required"},
		"no server either": {[]string{"--lab", "cvd"}, "--server and --username are required"},
	} {
		if err := runImportCML(append([]string{"--dir", dir}, tc.args...)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}
}

// TestImportCML_ViaConsole: --via console makes an IOS node a device
// reached through the controller's console server, at the console UUID the
// controller gives, and skips a node that is not IOS, saying why; it needs
// --controller and takes no address; and trust-host reads a controller's
// console server address, so its host key can be trusted.
func TestImportCML_ViaConsole(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, _ := client.CreateLab(ctx, "cvd")
	r1, _ := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"})
	if _, err := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "SW1", Definition: "unmanaged_switch"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, step := range [][]string{
		{"init", "--dir", dir},
		{"add-host", "cml", "--dir", dir, "--type", "cml_controller", "--set", "api_base=" + ctl.URL, "--set", "tls_fingerprint=" + devicetls.Fingerprint(ctl.Certificate()), "--set", "console_port=2222"},
		{"add-credential", "cml", "--dir", dir, "--username", "admin", "--password", "pw"},
	} {
		run := map[string]func([]string) error{"init": runInit, "add-host": runAddHost, "add-credential": runAddCredential}[step[0]]
		if err := run(step[1:]); err != nil {
			t.Fatalf("%s: %v", strings.Join(step, " "), err)
		}
	}
	if err := runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--via", "console"}); err != nil {
		t.Fatal(err)
	}
	repo := inventory.NewFileRepository(filepath.Join(dir, inventory.DefaultInventoryFilename), inventory.NewItemFactory())
	item, err := repo.GetByName(ctx, "R1")
	if err != nil {
		t.Fatal(err)
	}
	served, ok := item.(capability.ConsoleServedCapable)
	if !ok || !item.HasCapability(capability.NameConsoleServed) || served.ConsoleServer() != "cml" || served.ConsoleLine() != "11111111-2222-4333-8444-"+r1[len(r1)-12:] {
		t.Errorf("R1 = %v", item.Properties())
	}
	if _, err := repo.GetByName(ctx, "SW1"); err == nil {
		t.Error("a node that is not IOS was imported to be reached through its console")
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no controller": {[]string{"--server", ctl.URL, "--username", "admin", "--lab", "cvd", "--via", "console"}, "needs --controller"},
		"an address":    {[]string{"--controller", "cml", "--lab", "cvd", "--via", "console", "--address", "R1=10.0.0.1"}, "no address of its own"},
		"a bad via":     {[]string{"--controller", "cml", "--lab", "cvd", "--via", "serial"}, "ssh, telnet or console"},
	} {
		if err := runImportCML(append([]string{"--dir", dir}, tc.args...)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}

	controller, err := repo.GetByName(ctx, "cml")
	if err != nil {
		t.Fatal(err)
	}
	if addr, err := sshAddress(controller); err != nil || !strings.HasSuffix(addr, ":2222") {
		t.Errorf("trust-host's address for a controller = %q, %v", addr, err)
	}

	// Imported again another way with --replace, R1 keeps nothing of the
	// way it was reached before.
	props := func() map[string]string {
		item, err := repo.GetByName(ctx, "R1")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for key, v := range item.Properties().Raw() {
			out[key] = fmt.Sprint(v)
		}
		return out
	}
	if err := runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--nodes", "R1", "--via", "telnet", "--address", "R1=10.0.0.1", "--replace"}); err != nil {
		t.Fatal(err)
	}
	if p := props(); p["host"] != "10.0.0.1" || p["console_server"] != "" || p["console_line"] != "" {
		t.Errorf("R1 over Telnet after the console = %v", p)
	}
	if err := runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--nodes", "R1", "--via", "console", "--replace"}); err != nil {
		t.Fatal(err)
	}
	if p := props(); p["host"] != "" || p["port"] != "" || p["console_server"] != "cml" {
		t.Errorf("R1 over the console after Telnet = %v", p)
	}
}

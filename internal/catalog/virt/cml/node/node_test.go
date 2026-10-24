// Tests of the virt.cml.node.* methods against cmltest's controller: a node
// seeded with an inventory device's login and address, a plain one, every
// refusal that keeps a value from becoming a configuration line of its own,
// each undo, each check, and removal.
package node_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/cml/node"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iossecret"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func init() { node.SetPollInterval(time.Millisecond) }

// fixture is a controller with one lab, and a logged-in client to read it.
type fixture struct {
	t      *testing.T
	ctl    *cmltest.Controller
	device *cmltest.Device
	client *cml.Client
	lab    cml.Lab
}

// newFixture starts a controller holding a lab titled cvd.
func newFixture(t *testing.T) fixture {
	t.Helper()
	ctl := cmltest.New(t, "admin", "pw")
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	lab, err := client.CreateLab(context.Background(), "cvd")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{t: t, ctl: ctl, device: cmltest.NewDevice(t, ctl), client: client, lab: lab}
}

// run calls fqcn (its check with check set) with params, the context also
// holding seed, as the engine hands a seeding method its device's login.
func (f fixture) run(fqcn string, check bool, params map[string]any, seed map[string]string) (collection.Result, *cmltest.Context, error) {
	f.t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		f.t.Fatalf("%s is not registered", fqcn)
	}
	call := d.Invoke
	if check {
		call = d.Check
	}
	rc := cmltest.NewContext(f.ctl)
	for k, v := range seed {
		rc.Secrets[k] = v
	}
	res, err := call(context.Background(), rc, f.device, params)
	return res, rc, err
}

// coreSeed is what the engine hands node.create for an inventory device
// CORE1 at 10.10.20.181 whose credential has an enable password.
func coreSeed(t *testing.T) map[string]string {
	t.Helper()
	secret, err := iossecret.Hash(iossecret.Type9, "login-pw")
	if err != nil {
		t.Fatal(err)
	}
	enable, err := iossecret.Hash(iossecret.Type9, "enable-pw")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{wire.SecretSeedUsername: "netops", wire.SecretSeedIOSSecret: secret, wire.SecretSeedIOSEnableSecret: enable, wire.SecretSeedHost: "10.10.20.181"}
}

// TestCreate_ASeededNodeBootsAsItsInventoryDevice: a node standing for an
// inventory device is made with a day-0 configuration naming it, its login
// as type 9 secrets, and its address in the management VRF with a route to
// the gateway, then the task's own lines; the undo removes exactly it; a
// second make of the label is refused; and a check makes nothing.
func TestCreate_ASeededNodeBootsAsItsInventoryDevice(t *testing.T) {
	f := newFixture(t)
	seed := coreSeed(t)
	params := map[string]any{"lab": "cvd", "label": "CORE1", "definition": "iol-xe", "device": "CORE1",
		"management_gateway": "10.10.20.254", "configuration": "ip routing", "x": 100, "y": 50}
	if res, _, err := f.run("virt.cml.node.create", true, params, seed); err != nil || !res.Changed || len(f.ctl.Labs[f.lab.ID].Nodes) != 0 {
		t.Fatalf("check: %+v %v", res, err)
	}
	res, rc, err := f.run("virt.cml.node.create", false, params, seed)
	if err != nil || !res.Changed {
		t.Fatalf("create: %+v %v", res, err)
	}
	id, _ := rc.Stats["id"].(string)
	n := f.ctl.Labs[f.lab.ID].Nodes[id]
	if n == nil || n.Label != "CORE1" || rc.Stats["management_address"] != "10.10.20.181" {
		t.Fatalf("node %+v, stats %v", n, rc.Stats)
	}
	for _, want := range []string{
		"hostname CORE1\n",
		"username netops privilege 15 secret 9 " + seed[wire.SecretSeedIOSSecret] + "\n",
		"enable secret 9 " + seed[wire.SecretSeedIOSEnableSecret] + "\n",
		"interface Ethernet0/0\n vrf forwarding Mgmt-intf\n ip address 10.10.20.181 255.255.255.0\n no shutdown\n",
		"ip route vrf Mgmt-intf 0.0.0.0 0.0.0.0 10.10.20.254\n",
		"line vty 0 4\n login local\n transport input ssh\n",
		"ip routing\nend\n",
	} {
		if !strings.Contains(n.Configuration, want) {
			t.Errorf("the day-0 configuration lacks %q:\n%s", want, n.Configuration)
		}
	}
	if strings.Contains(n.Configuration, "login-pw") || strings.Contains(n.Configuration, "enable-pw") {
		t.Error("a password reached the controller")
	}
	if got, _ := rc.Stats["interfaces"].([]string); len(got) != len(cmltest.Interfaces["iol-xe"]) {
		t.Errorf("interfaces %v", got)
	}
	inv, ok := rc.Inverse()
	if !ok || inv[sdk.InverseFQCNKey] != "virt.cml.node.delete" || inv["params"].(map[string]any)["node"] != id || inv["params"].(map[string]any)["lab"] != f.lab.ID {
		t.Errorf("the undo: %v", inv)
	}
	if _, _, err := f.run("virt.cml.node.create", false, params, seed); err == nil || !strings.Contains(err.Error(), "already has a node labelled") {
		t.Errorf("a second create: %v", err)
	}
}

// TestCreate_PlainAndConsoleNodes: a node standing for no device boots on
// the configuration as given; one standing for a device with no address of
// its own gets the login and no management interface.
func TestCreate_PlainAndConsoleNodes(t *testing.T) {
	f := newFixture(t)
	if _, rc, err := f.run("virt.cml.node.create", false, map[string]any{"lab": f.lab.ID, "label": "SW1", "definition": "unmanaged_switch", "configuration": "as given"}, nil); err != nil {
		t.Fatal(err)
	} else if n := f.ctl.Labs[f.lab.ID].Nodes[rc.Stats["id"].(string)]; n.Configuration != "as given" {
		t.Errorf("a plain node's configuration: %q", n.Configuration)
	}
	seed := coreSeed(t)
	delete(seed, wire.SecretSeedHost)
	delete(seed, wire.SecretSeedIOSEnableSecret)
	_, rc, err := f.run("virt.cml.node.create", false, map[string]any{"lab": f.lab.ID, "label": "BR1", "definition": "iol-xe", "device": "BR1"}, seed)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.ctl.Labs[f.lab.ID].Nodes[rc.Stats["id"].(string)].Configuration
	if !strings.Contains(cfg, "secret 9") || strings.Contains(cfg, "vrf forwarding") || strings.Contains(cfg, "enable secret") || rc.Stats["management_address"] != nil {
		t.Errorf("a console node's configuration:\n%s", cfg)
	}
}

// TestCreate_Refusals: each value that would not be one value in the
// configuration, a definition that is not IOS for a seed, one the
// controller lacks, and a lab that does not exist are refused, and nothing
// is made.
func TestCreate_Refusals(t *testing.T) {
	f := newFixture(t)
	seed := coreSeed(t)
	base := func(over map[string]any) map[string]any {
		p := map[string]any{"lab": "cvd", "label": "CORE1", "definition": "iol-xe", "device": "CORE1", "management_gateway": "10.10.20.254"}
		for k, v := range over {
			p[k] = v
		}
		return p
	}
	for name, tc := range map[string]struct {
		params map[string]any
		seed   map[string]string
		want   string
	}{
		"a label with a newline":       {base(map[string]any{"label": "R1\nusername x"}), seed, "label"},
		"a label that is no hostname":  {base(map[string]any{"label": "1-core"}), seed, "hostname"},
		"a definition that is not IOS": {base(map[string]any{"definition": "unmanaged_switch"}), seed, "not an IOS"},
		"an interface with a space":    {base(map[string]any{"management_interface": "Eth 0"}), seed, "interface name"},
		"no gateway":                   {base(map[string]any{"management_gateway": ""}), seed, "required"},
		"a gateway off the subnet":     {base(map[string]any{"management_gateway": "10.10.99.1"}), seed, "is not on"},
		"a gateway that is no address": {base(map[string]any{"management_gateway": "gw; reload"}), seed, "not an IPv4"},
		"a prefix out of range":        {base(map[string]any{"management_prefix": 31}), seed, "1 to 30"},
		"a host that is IPv6":          {base(nil), map[string]string{wire.SecretSeedUsername: "netops", wire.SecretSeedIOSSecret: seed[wire.SecretSeedIOSSecret], wire.SecretSeedHost: "2001:db8::1"}, "IPv4"},
		"no seed handed over":          {base(nil), nil, "no IOS login"},
		"a definition it lacks":        {map[string]any{"lab": "cvd", "label": "X", "definition": "nexus"}, nil, "no node definition"},
		"no such lab":                  {map[string]any{"lab": "nothing", "label": "X", "definition": "iosv"}, nil, "not found"},
	} {
		if _, _, err := f.run("virt.cml.node.create", false, tc.params, tc.seed); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}
	if n := len(f.ctl.Labs[f.lab.ID].Nodes); n != 0 {
		t.Errorf("%d nodes were made by refused tasks", n)
	}
}

// TestDelete_StopsWipesAndRemoves: a running node is stopped, wiped and
// removed with its links, records no undo, and a second removal, of a node
// or in a lab that does not exist, is unchanged.
func TestDelete_StopsWipesAndRemoves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, _ := f.client.CreateNode(ctx, f.lab.ID, cml.NodeSpec{Label: "BR3", Definition: "iol-xe"})
	if err := f.client.StartNode(ctx, f.lab.ID, id); err != nil {
		t.Fatal(err)
	}
	if res, _, err := f.run("virt.cml.node.delete", true, map[string]any{"lab": "cvd", "node": "BR3"}, nil); err != nil || !res.Changed || f.ctl.Labs[f.lab.ID].Nodes[id] == nil {
		t.Fatalf("check: %+v %v", res, err)
	}
	res, rc, err := f.run("virt.cml.node.delete", false, map[string]any{"lab": "cvd", "node": "BR3"}, nil)
	if err != nil || !res.Changed || f.ctl.Labs[f.lab.ID].Nodes[id] != nil || rc.Stats["node_id"] != id {
		t.Fatalf("delete: %+v %v", res, err)
	}
	if _, ok := rc.Inverse(); ok {
		t.Error("an irreversible delete recorded an undo")
	}
	for _, params := range []map[string]any{{"lab": "cvd", "node": "BR3"}, {"lab": "nothing", "node": "BR3"}} {
		if res, _, err := f.run("virt.cml.node.delete", false, params, nil); err != nil || res.Changed {
			t.Errorf("%v: %+v %v", params, res, err)
		}
	}
}

// TestNode_FailureWalk: each call a node method makes, failed by the
// controller in turn, fails the task, keeping the undo of a node already
// made, and so does a login the controller refuses.
func TestNode_FailureWalk(t *testing.T) {
	createParams := map[string]any{"lab": "cvd", "label": "SW1", "definition": "unmanaged_switch"}
	for _, call := range []string{"GET /labs", "/simplified_node_definitions", "GET /nodes", "POST /nodes", "/interfaces"} {
		f := newFixture(t)
		f.ctl.Fail = map[string]bool{call: true}
		_, rc, err := f.run("virt.cml.node.create", false, createParams, nil)
		if err == nil {
			t.Errorf("create with %s failing: no error", call)
		}
		// Once the node is made, its undo stands whatever fails after.
		inv, kept := rc.Inverse()
		if made := call == "/interfaces"; made != kept || made && inv[sdk.InverseKeepOnFailureKey] != true {
			t.Errorf("create with %s failing: undo %v", call, inv)
		}
	}
	for _, call := range []string{"GET /nodes", "/state/stop", "/wipe_disks", "DELETE /nodes/"} {
		f := newFixture(t)
		ctx := context.Background()
		id, _ := f.client.CreateNode(ctx, f.lab.ID, cml.NodeSpec{Label: "BR3", Definition: "iol-xe"})
		_ = f.client.StartNode(ctx, f.lab.ID, id)
		if call == "DELETE /nodes/" {
			call = "DELETE /labs/" + f.lab.ID + "/nodes/" + id
		}
		f.ctl.Fail = map[string]bool{call: true}
		if _, _, err := f.run("virt.cml.node.delete", false, map[string]any{"lab": "cvd", "node": "BR3"}, nil); err == nil {
			t.Errorf("delete with %s failing: no error", call)
		}
	}
	f := newFixture(t)
	for _, fqcn := range []string{"virt.cml.node.create", "virt.cml.node.delete"} {
		if _, _, err := f.run(fqcn, false, map[string]any{"lab": "cvd", "label": "X", "node": "X", "definition": "iosv"}, map[string]string{wire.SecretPassword: "wrong"}); err == nil || !strings.Contains(err.Error(), "refused the login") {
			t.Errorf("%s with the login refused: %v", fqcn, err)
		}
	}
}

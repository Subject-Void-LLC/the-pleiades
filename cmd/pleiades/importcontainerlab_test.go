// Tests of import-containerlab: what it writes from a lab's topology, each
// refusal, and a live import that trusts and onboards real routers.
package main

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// clabNodeJSON is one node of a topology file, published on port when
// port is not zero.
func clabNodeJSON(name, lab, mgmt string, hostIP string, port int) map[string]any {
	n := map[string]any{
		"shortname": name, "kind": "linux", "image": "frr", "mgmt-ipv4-address": mgmt,
		"labels": map[string]any{"containerlab": lab, "clab-node-name": name},
	}
	if port != 0 {
		n["port-bindings"] = []any{map[string]any{"host-ip": hostIP, "host-port": port, "port": 22, "protocol": "tcp"}}
	}
	return n
}

// writeTopology writes a topology file and returns its path.
func writeTopology(t *testing.T, lab string, nodes map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"name": lab, "type": "clab", "nodes": nodes, "links": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "topology-data.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestImportContainerlab_WritesEachNodeAndRefusesOtherLabs: published
// ports win over management addresses, a key becomes every node's
// credential, and a node, a file or a name of another lab is refused with
// nothing written.
func TestImportContainerlab_WritesEachNodeAndRefusesOtherLabs(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(keyPath, []byte("PRIVATE-KEY-PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	topo := writeTopology(t, "lab1", map[string]any{
		"r1": clabNodeJSON("r1", "lab1", "172.20.30.11", "0.0.0.0", 2211),
		"r2": clabNodeJSON("r2", "lab1", "172.20.30.12", "", 0),
	})
	var runErr error
	out := captureStdout(t, func() {
		runErr = runImportContainerlab([]string{"--topology", topo, "--lab", "lab1", "--username", "netops", "--key", keyPath, "--dir", dir, "--json"})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if strings.Contains(out, "PRIVATE-KEY-PEM") || !strings.Contains(out, `"address": "published SSH port"`) {
		t.Errorf("--json printed %s", out)
	}
	r1, ok := hostSpec(t, dir, "r1")
	if !ok || r1.Type != generic.TypeSSH || r1.Properties["host"] != "127.0.0.1" || r1.Properties["port"] != 2211 || !slices.Contains(r1.Tags, "lab-lab1") {
		t.Errorf("r1 = %+v", r1)
	}
	if r2, _ := hostSpec(t, dir, "r2"); r2.Properties["host"] != "172.20.30.12" || r2.Properties["port"] != 22 {
		t.Errorf("r2 = %+v", r2)
	}
	if c := lookupStored(t, dir, "r2"); c.Username != "netops" || string(c.PrivateKeyPEM) != "PRIVATE-KEY-PEM" {
		t.Errorf("r2's credential = %q", c.Username)
	}

	other := writeTopology(t, "lab2", map[string]any{
		"r9": clabNodeJSON("r9", "lab2", "172.20.30.19", "", 0),
		"x1": clabNodeJSON("x1", "elsewhere", "172.20.30.20", "", 0),
		"..": clabNodeJSON("..", "lab2", "172.20.30.21", "", 0),
	})
	for name, args := range map[string][]string{
		"another lab's file":         {"--topology", topo, "--lab", "lab2"},
		"a node labelled elsewhere":  {"--topology", other, "--lab", "lab2", "--nodes", "x1"},
		"a node that is not a name":  {"--topology", other, "--lab", "lab2", "--nodes", ".."},
		"a node the lab has not got": {"--topology", other, "--lab", "lab2", "--nodes", "r8"},
		"a node already imported":    {"--topology", topo, "--lab", "lab1", "--nodes", "r1"},
		"a key with no username":     {"--topology", topo, "--lab", "lab1", "--key", keyPath},
		"no lab":                     {"--topology", topo},
	} {
		if err := runImportContainerlab(append(args, "--dir", dir)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, ok := hostSpec(t, dir, "r9"); ok {
		t.Error("a refused import wrote a node")
	}
	if err := runImportContainerlab([]string{"--topology", topo, "--lab", "lab1", "--nodes", "r1", "--replace", "--dir", dir}); err != nil {
		t.Errorf("--replace: %v", err)
	}
}

// TestImportContainerlab_TrustsAndOnboardsRealRouters: a topology naming
// two real routers' published ports is imported with a key, trusted on
// first connect and onboarded, and each is granted FRRCapable.
func TestImportContainerlab_TrustsAndOnboardsRealRouters(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	link := testsupport.NetLabLink{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.211.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.211.0.2"), "r2": netip.MustParseAddr("10.211.0.3"),
	}}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2"}, nil, []testsupport.NetLabLink{link})
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, filepath.Join(dir, "known_hosts"))
	keyPath := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(keyPath, lab.PrivateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]any{}
	for _, name := range []string{"r1", "r2"} {
		n := lab.Nodes[name]
		host := n.Host
		if host == "localhost" {
			host = "127.0.0.1"
		}
		nodes[name] = clabNodeJSON(name, "live", "172.31.0.9", host, n.Port)
	}
	var runErr error
	onboardOut := captureStdout(t, func() {
		runErr = runImportContainerlab([]string{"--topology", writeTopology(t, "live", nodes), "--lab", "live",
			"--username", testsupport.NetLabUser, "--key", keyPath, "--trust-first-connect", "--onboard", "--dir", dir})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	for _, name := range []string{"r1", "r2"} {
		item := buildHost(t, dir, name)
		if !item.HasCapability(capability.NameFRR) {
			t.Errorf("%s was not granted FRRCapable: %v\n%s", name, item.Capabilities(), onboardOut)
		}
	}
}

// buildHost loads the device named name as a run does, through the
// project's file repository, discovery and grants included.
func buildHost(t *testing.T, dir, name string) pkginventory.InventoryItem {
	t.Helper()
	repo := inventory.NewFileRepository(filepath.Join(dir, inventory.DefaultInventoryFilename), inventory.NewItemFactory())
	item, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

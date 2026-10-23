// Tests that drive a real Batfish through the client: a snapshot of two
// FRR routers is parsed, its routes and OSPF sessions answered, and a fork
// with their link down routes differently.
package batfish_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/batfish"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// router returns an FRR configuration for a router with a loopback, one
// point-to-point link and OSPF on both.
func router(name, loopback, link, id string) string {
	return "frr version 10.1.1\nfrr defaults traditional\nhostname " + name + "\nservice integrated-vtysh-config\n!\n" +
		"interface eth1\n ip address " + link + "\n ip ospf area 0\n ip ospf network point-to-point\nexit\n!\n" +
		"interface lo\n ip address " + loopback + "\n ip ospf area 0\nexit\n!\n" +
		"router ospf\n ospf router-id " + id + "\nexit\n!\nend\n"
}

// TestClient_AgainstARealBatfish: parse, routes, OSPF sessions, a fork,
// and the refusals.
func TestClient_AgainstARealBatfish(t *testing.T) {
	if testing.Short() {
		t.Skip("starts containers")
	}
	ctx := context.Background()
	c, err := batfish.New(testsupport.StartBatfish(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Version(ctx); err != nil || v == "" {
		t.Fatalf("version %q, %v", v, err)
	}
	configs := map[string]string{}
	for name, cfg := range map[string]string{
		"r1": router("r1", "10.255.0.1/32", "10.0.12.0/31", "10.255.0.1"),
		"r2": router("r2", "10.255.0.2/32", "10.0.12.1/31", "10.255.0.2"),
	} {
		if configs[name], err = netcfg.FRRForBatfish(name, cfg); err != nil {
			t.Fatal(err)
		}
	}
	zip, err := batfish.Snapshot(configs)
	if err != nil {
		t.Fatal(err)
	}
	network, err := batfish.NewNetworkName()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CreateNetwork(ctx, network); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteNetwork(context.Background(), network) })
	if err := c.UploadSnapshot(ctx, network, "base", zip); err != nil {
		t.Fatal(err)
	}

	status, err := c.Ask(ctx, network, "base", "fileParseStatus", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Rows) != 2 {
		t.Errorf("fileParseStatus rows = %v", status.Rows)
	}
	for _, r := range status.Rows {
		if s, _ := r["Status"].(string); s == "FAILED" {
			t.Errorf("a file failed to parse: %v", r)
		}
	}
	sessions, err := c.Ask(ctx, network, "base", "ospfSessionCompatibility", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions.Rows) != 2 || sessions.Rows[0]["Session_Status"] != "ESTABLISHED" {
		t.Errorf("OSPF sessions = %v", sessions.Rows)
	}
	routes, err := c.Ask(ctx, network, "base", "routes", map[string]any{"nodes": "r1", "network": "10.255.0.2/32"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Rows) != 1 || routes.Rows[0]["Protocol"] != "ospf" {
		t.Errorf("r1's route to r2's loopback = %v", routes.Rows)
	}

	if err := c.Fork(ctx, network, "base", "down", []batfish.Interface{{Hostname: "r1", Interface: "eth1"}}, nil); err != nil {
		t.Fatal(err)
	}
	routes, err = c.Ask(ctx, network, "down", "routes", map[string]any{"nodes": "r1", "network": "10.255.0.2/32"})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Rows) != 0 {
		t.Errorf("with the link down, r1 still routes to r2's loopback: %v", routes.Rows)
	}

	if _, err := c.Ask(ctx, network, "base", "noSuchQuestion", nil); err == nil || !strings.Contains(err.Error(), "no question") {
		t.Errorf("an unknown question: %v", err)
	}
	if _, err := c.Ask(ctx, network, "base", "routes", map[string]any{"noSuchVariable": 1}); err == nil {
		t.Error("an unknown variable was accepted")
	}
	if _, err := c.Ask(ctx, network, "nosnapshot", "routes", nil); err == nil {
		t.Error("a snapshot that does not exist was answered")
	}
}

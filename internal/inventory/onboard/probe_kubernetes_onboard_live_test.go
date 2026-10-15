// Onboarding a real k3s cluster through Onboard itself, against a real
// inventory: each node found as its container on this machine's engine,
// the cluster reached through a real SSH bastion by its route, and what
// either refuses.
package onboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// clusterInventory is a file inventory holding records.
func clusterInventory(t *testing.T, records ...record.Record) inv.Repository {
	t.Helper()
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(filepath.Join(t.TempDir(), inv.DefaultInventoryFilename), factory)
	for _, rec := range records {
		rec.ID, rec.State = inventory.DeviceID(rec.Name+"-id"), record.InitialState(rec.Type)
		item, err := factory.Build(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// TestKubernetesOnboard_NodesAndRoutes onboards one k3s server, whose node
// is a container on this machine's Docker engine with a known machine ID,
// three ways through Onboard: with node_engine_host naming the engine (the
// node is found as its container), through a route of one SSH hop (the
// bastion logs the probe in), and with each piece missing (refused naming
// it).
func TestKubernetesOnboard_NodesAndRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	reachable := exec.Command("docker", "info").Run() == nil // #nosec G204 -- fixed arguments
	testsupport.Require(t, "docker", reachable, "this machine's Docker engine does not answer")
	// This test stands in for the command line, the one process that reaches
	// this machine's engine through its own socket, and so turns on what
	// cmd/pleiades's run does; onboarding reads the same switch.
	containerengine.AllowLocalCarrier()

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	k := testsupport.StartK3s(t, testsupport.K3sOptions{MachineID: hex.EncodeToString(raw)})
	insp, err := k.Container.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	bastion, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bastion.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{knownhosts.Normalize(bastion.Addr())}, bastion.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)

	cluster := func(name string, extra map[string]inventory.PropertyValue) record.Record {
		props := map[string]inventory.PropertyValue{kubernetes.APIServerProperty: k.Server, devicetls.CAPEMProperty: k.CAPEM}
		for key, v := range extra {
			props[key] = v
		}
		return record.Record{Name: name, Type: kubernetes.TypeCluster, Properties: props}
	}
	repo := clusterInventory(t,
		record.Record{Name: "engine", Type: container.TypeHost},
		record.Record{Name: "unprobed", Type: container.TypeHost},
		record.Record{Name: "bastion", Type: "linux_server", Properties: map[string]inventory.PropertyValue{"host": bastion.Host, "port": bastion.Port}},
		cluster("mapped", map[string]inventory.PropertyValue{kubernetes.NodeEngineHostProperty: "engine"}),
		cluster("routed", map[string]inventory.PropertyValue{"route": []any{"bastion"}}),
		cluster("lost-route", map[string]inventory.PropertyValue{"route": []any{"missing"}}),
		cluster("lost-engine", map[string]inventory.PropertyValue{kubernetes.NodeEngineHostProperty: "missing"}),
	)
	admin := map[string]string{wire.SecretCertificatePEM: k.ClientCert, wire.SecretPrivateKeyPEM: k.ClientKey}
	secrets := func(_ context.Context, device inventory.InventoryItem) (map[string]string, error) {
		if _, isCluster := device.(*kubernetes.Cluster); isCluster {
			return admin, nil
		}
		return nil, nil
	}
	hops := credential.NewStaticStore(bastion.Secrets())
	routes := WithRoutes(func(ctx context.Context, device inventory.InventoryItem) (sdk.RouteDial, error) {
		dial, _, err := engine.RouteDialerFor(ctx, repo, hops, device)
		return dial, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	if _, err := Onboard(ctx, repo, "engine", secrets, time.Now); err != nil {
		t.Fatalf("onboarding this machine's engine: %v", err)
	}
	res, err := Onboard(ctx, repo, "mapped", secrets, time.Now, routes)
	if err != nil {
		t.Fatalf("onboarding the cluster with its node engine host: %v", err)
	}
	nodes, _ := res.Facts[FactNodeContainers].([]any)
	if len(nodes) != 1 {
		t.Fatalf("node_containers = %v, want the one node's container", res.Facts[FactNodeContainers])
	}
	node, _ := nodes[0].(map[string]any)
	if node["container"] != insp.ID || node["engine"] != "docker" || node["engine_id"] == "" {
		t.Errorf("the node's container = %v, want %s on docker", node, insp.ID)
	}

	// What the search leaves out, through the engine itself: a node that
	// reports no machine ID, one no container is named after or hosts, one
	// whose name is not a container name, and the real node claiming a
	// machine ID its container does not hold. A name alone matches nothing.
	peers := repoPeers{repo: repo, secrets: secrets}
	hostname := insp.Config.Hostname
	left, err := mapNodes(ctx, []kubeapi.Node{
		{Name: hostname},
		{Name: "no-such-node", MachineID: "a1"},
		{Name: "not a container name!", MachineID: "a1"},
		{Name: hostname, MachineID: "not-its-machine-id"},
	}, "engine", peers)
	if err != nil || len(left) != 0 {
		t.Errorf("nodes that are not provably their containers: %v, %v; want none", left, err)
	}
	if _, err := mapNodes(ctx, []kubeapi.Node{{Name: hostname, MachineID: "a1"}}, "unprobed", peers); err == nil || !strings.Contains(err.Error(), `"unprobed"`) {
		t.Errorf("an engine host onboarding never reached: %v", err)
	}
	// A peer's session records nothing of its own.
	if (peerRC{}).SetStat("k", 1) != nil || (peerRC{}).EmitFact("k", 1) != nil || len((peerRC{secrets: admin}).InjectSecrets()) != 2 {
		t.Error("a peer's run context records or withholds what it should not")
	}

	before := bastion.Logins()
	if res, err := Onboard(ctx, repo, "routed", secrets, time.Now, routes); err != nil || res.State != "active" {
		t.Fatalf("onboarding the cluster through its route: %+v, %v", res, err)
	}
	if bastion.Logins() <= before {
		t.Error("the bastion saw no login, so the probe did not go through the route")
	}

	for name, want := range map[string]string{"lost-route": "resolving the cluster's route", "lost-engine": `node engine host "missing"`} {
		if _, err := Onboard(ctx, repo, name, secrets, time.Now, routes); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("onboarding %s: %v, want a refusal saying %q", name, err, want)
		}
	}

	// Probed alone, with no peers to consult: a record naming a node
	// engine host is refused, since nothing can reach that host; one
	// naming none is probed directly.
	item, err := repo.GetByName(ctx, "mapped")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (kubernetesProber{}).Probe(ctx, item, admin); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("a node engine host with no peers: %v", err)
	}
	if kubernetesProber.Protocol(kubernetesProber{}) != "kubernetes" {
		t.Error("the prober's protocol is not kubernetes")
	}
}

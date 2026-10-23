// Gate for examples/routing_lab through the real binary: the example's
// project, pointed at real FRR routers and an Open vSwitch switch, onboards
// as a user's would, and every runbook it ships validates with no issue and
// no warning. The change itself is proven against real routers and Batfish
// by TestCLI_NetworkChangeIsGatedAppliedAndUndone; the example's README
// records the runs against its containerlab lab.
package main_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// routingLabPorts are the SSH ports the example's inventory names, which
// containerlab publishes on loopback.
var routingLabPorts = map[string]string{"r1": "2201", "r2": "2202", "r3": "2203", "sw1": "2211"}

// TestCLI_RoutingLabExampleValidatesOnRealRouters is the gate.
func TestCLI_RoutingLabExampleValidatesOnRealRouters(t *testing.T) {
	if testing.Short() {
		t.Skip("starts routers and a switch")
	}
	_, err := os.Stat("/var/run/docker.sock")
	testsupport.Require(t, "docker", err == nil, "onboarding the example's container host reads this machine's engine socket")
	link := testsupport.NetLabLink{Name: "r1-sw1", Subnet: netip.MustParsePrefix("10.213.0.0/29"), Ends: map[string]netip.Addr{
		"r1": netip.MustParseAddr("10.213.0.2"), "sw1": netip.MustParseAddr("10.213.0.3"),
	}}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2", "r3"}, []string{"sw1"}, []testsupport.NetLabLink{link})

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "routing_lab", "pleiades"))); err != nil {
		t.Fatal(err)
	}
	inventoryPath := filepath.Join(dir, "inventory.yaml")
	data, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for name, port := range routingLabPorts {
		if !strings.Contains(text, "port: "+port+"\n") {
			t.Fatalf("the example's inventory no longer names %s on port %s", name, port)
		}
		text = strings.Replace(text, "port: "+port+"\n", "port: "+strconv.Itoa(lab.Nodes[name].Port)+"\n", 1)
	}
	if err := os.WriteFile(inventoryPath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "netops")
	if err := os.WriteFile(keyPath, lab.PrivateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	env := personEnv(map[string]string{"PLEIADES_KNOWN_HOSTS": filepath.Join(dir, "known_hosts")})
	must := func(args ...string) {
		t.Helper()
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	for _, name := range []string{"r1", "r2", "r3", "sw1"} {
		must("add-credential", name, "--username", testsupport.NetLabUser, "--key", keyPath)
		must("trust-host", name, "--first-connect")
		must("onboard", name)
	}
	must("onboard", "dockerhost")

	runbooks, err := filepath.Glob(filepath.Join(dir, "runbooks", "*.yaml"))
	if err != nil || len(runbooks) < 5 {
		t.Fatalf("the example's runbooks: %v, %v", runbooks, err)
	}
	for _, rb := range runbooks {
		rel := filepath.Join("runbooks", filepath.Base(rb))
		// change_r3 reads vars.candidate, which validate refuses unless the
		// run's variables give it, as the README's validate step does; a
		// variable a runbook does not read changes nothing for the others.
		out, err := runPleiadesWithEnv(t, dir, env, "validate", rel, "-e", "candidate=configs/r3.good.frr.conf")
		if err != nil || !strings.Contains(out, "no issues found") || strings.Contains(out, "[warning]") {
			t.Errorf("validate %s: %v\n%s", rel, err, out)
		}
	}
}

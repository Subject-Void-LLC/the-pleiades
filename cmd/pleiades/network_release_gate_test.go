// Release gate for Phase 127b: through the real binary, a routing change
// is held to Batfish's model before it is made. A bad candidate is refused
// with nothing on any router changed; a good one is applied in a block
// that undoes itself, and verified live; and a failure the model cannot
// see (a link taken down out of band) makes the block put the router back.
package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// networkGateRunbook checks r3's candidate (configs/r3.frr.conf) against
// the model, then applies it on r3 and verifies r3's route to r1's
// loopback over their direct link. The verification runs on r3 itself,
// so a failure there undoes r3's change.
const networkGateRunbook = `# What this does: checks r3's candidate against the model, applies it, and verifies r3 routes to r1 directly.
# Blast radius: r3's running configuration; r1 and r2 are read only.
# Changes: r3's running configuration.
# Undo: the block undoes the change if its verification fails; pleiades rollback undoes it later.
id: r3_change
hosts: r3
tasks:
  - name: Read r1
    net.frr.facts: {target: r1, include_config: true}
    register: r1
  - name: Read r2
    net.frr.facts: {target: r2, include_config: true}
    register: r2
  - name: The model routes every loopback with r3's candidate, and around a failed r1-r3 link
    net.batfish.check:
      target: batfish
      devices:
        - {name: r1, config: "{{ result.r1.running_config }}"}
        - {name: r2, config: "{{ result.r2.running_config }}"}
        - {name: r3, src: configs/r3.frr.conf}
      assertions:
        - ospf_established: true
        - route: {node: r1, prefix: 10.99.3.1/32, next_hops: [10.212.0.19]}
        - route: {node: r3, prefix: 10.99.1.1/32, next_hops: [10.212.0.18]}
      scenarios:
        - name: r1-r3 down
          down_interfaces: ["r1[%s]"]
          assertions:
            - reach: {from: r1, dst: 10.99.3.1, through: [r2]}
      accept_unrecognized:
        - {text: domainname localdomain, reason: "the DNS search domain, not routing"}
    register: gate
  - name: Apply on r3 and verify, undone if the verification fails
    rollback_on_failure: true
    block:
      - name: r3 runs the checked candidate
        net.frr.config:
          src: configs/r3.frr.conf
          expect_sha256: "{{ result.gate.sha256.r3 }}"
      - name: r3 routes r1's loopback over their direct link
        wait.routes:
          prefixes: [10.99.1.1/32]
          next_hops: {10.99.1.1/32: [10.212.0.18]}
          stable_for: 3
          timeout: 25
      - name: r1's loopback answers r3
        net.trace:
          dest: 10.99.1.1
          max_hops: 4
          expect: {reached: true}
`

// TestCLI_NetworkChangeIsGatedAppliedAndUndone is the release gate.
func TestCLI_NetworkChangeIsGatedAppliedAndUndone(t *testing.T) {
	if testing.Short() {
		t.Skip("starts routers and Batfish")
	}
	links := []testsupport.NetLabLink{
		{Name: "r1-r2", Subnet: netip.MustParsePrefix("10.212.0.0/29"), Ends: map[string]netip.Addr{"r1": netip.MustParseAddr("10.212.0.2"), "r2": netip.MustParseAddr("10.212.0.3")}},
		{Name: "r2-r3", Subnet: netip.MustParsePrefix("10.212.0.8/29"), Ends: map[string]netip.Addr{"r2": netip.MustParseAddr("10.212.0.10"), "r3": netip.MustParseAddr("10.212.0.11")}},
		{Name: "r1-r3", Subnet: netip.MustParsePrefix("10.212.0.16/29"), Ends: map[string]netip.Addr{"r1": netip.MustParseAddr("10.212.0.18"), "r3": netip.MustParseAddr("10.212.0.19")}},
	}
	lab := testsupport.StartNetLab(t, []string{"r1", "r2", "r3"}, nil, links)
	batfishURL := strings.Replace(testsupport.StartBatfish(t), "localhost", "127.0.0.1", 1)

	// The network as it stands: each router's links and loopback in OSPF,
	// configured as root, as an operator found it.
	iface := map[string]string{}
	for i, name := range []string{"r1", "r2", "r3"} {
		n := lab.Nodes[name]
		args := []string{"vtysh", "-c", "configure terminal", "-c", "hostname " + name}
		for _, l := range links {
			addr, ok := l.Ends[name]
			if !ok {
				continue
			}
			ifname := n.InterfaceWith(t, addr)
			iface[name+"/"+l.Name] = ifname
			args = append(args, "-c", "interface "+ifname, "-c", "ip address "+addr.String()+"/29",
				"-c", "ip ospf area 0", "-c", "ip ospf network point-to-point", "-c", "exit")
		}
		args = append(args, "-c", "interface lo", "-c", fmt.Sprintf("ip address 10.99.%d.1/32", i+1), "-c", "ip ospf area 0", "-c", "exit",
			"-c", "router ospf", "-c", fmt.Sprintf("ospf router-id 10.99.%d.1", i+1))
		n.Exec(t, args...)
	}

	dir := t.TempDir()
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": filepath.Join(dir, "known_hosts")}
	pleiades := func(args ...string) (string, error) { return runPleiadesWithEnv(t, dir, env, args...) }
	must := func(args ...string) string {
		t.Helper()
		out, err := pleiades(args...)
		if err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	must("init")
	keyPath := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(keyPath, lab.PrivateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"r1", "r2", "r3"} {
		n := lab.Nodes[name]
		host := n.Host
		if host == "localhost" {
			host = "127.0.0.1"
		}
		must("add-host", name, "--type", "generic_ssh", "--set", "host="+host, "--set", "port="+strconv.Itoa(n.Port), "--set", "ephemeral=true")
		must("add-credential", name, "--username", testsupport.NetLabUser, "--key", keyPath)
		must("trust-host", name, "--first-connect")
		must("onboard", name)
	}
	must("add-host", "batfish", "--type", "batfish", "--set", "api_base="+batfishURL)
	if err := os.MkdirAll(filepath.Join(dir, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	runbook := filepath.Join(dir, "runbooks", "r3_change.yaml")
	if err := os.WriteFile(runbook, fmt.Appendf(nil, networkGateRunbook, iface["r1/r1-r3"]), 0o600); err != nil {
		t.Fatal(err)
	}
	must("validate", "runbooks/r3_change.yaml")

	running := func(name string) string {
		out := lab.Nodes[name].Exec(t, "vtysh", "-c", "show running-config")
		return out[strings.Index(out, "frr version"):]
	}
	digest := func(name string) string {
		sum := sha256.Sum256([]byte(running(name)))
		return hex.EncodeToString(sum[:])
	}
	candidate := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "configs", "r3.frr.conf"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func() (map[string]any, string, error) {
		t.Helper()
		out, err := pleiades("run", "runbooks/r3_change.yaml", "--json")
		var report map[string]any
		start := strings.Index(out, "{")
		// Standard error follows the report in combined output, so only the
		// first JSON value is read.
		if start < 0 || json.NewDecoder(strings.NewReader(out[start:])).Decode(&report) != nil {
			t.Fatalf("run printed no report: %v\n%s", err, out)
		}
		return report, out, err
	}
	changed := func(report map[string]any) []string {
		var names []string
		tasks, _ := report["tasks"].([]any)
		for _, tk := range tasks {
			task, _ := tk.(map[string]any)
			if task["status"] == "changed" {
				names = append(names, fmt.Sprint(task["name"]))
			}
		}
		return names
	}
	r3Base := running("r3")
	lo := "interface lo\n ip address 10.99.3.1/32\n ip ospf area 0\n"
	if !strings.Contains(r3Base, lo) {
		t.Fatalf("r3's configuration is not as set up:\n%s", r3Base)
	}

	// A bad candidate: r3 stops advertising its loopback. The model
	// refuses it, and no router changes.
	before := map[string]string{"r1": digest("r1"), "r2": digest("r2"), "r3": digest("r3")}
	candidate(strings.Replace(r3Base, lo, "interface lo\n ip address 10.99.3.1/32\n", 1))
	report, out, err := run()
	if err == nil || !strings.Contains(out, "r1 has no route to 10.99.3.1/32") {
		t.Fatalf("the bad candidate was not refused by the model: %v\n%s", err, out)
	}
	if c := changed(report); len(c) > 0 {
		t.Errorf("a refused change changed %v", c)
	}
	for name, d := range before {
		if digest(name) != d {
			t.Errorf("%s's running configuration changed under a refused candidate", name)
		}
	}

	// A good candidate: a description on r3's link to r2. It passes,
	// is applied and verified live.
	good := strings.Replace(r3Base, "interface "+iface["r3/r2-r3"]+"\n", "interface "+iface["r3/r2-r3"]+"\n description gated-change\n", 1)
	candidate(good)
	report, out, err = run()
	if err != nil {
		t.Fatalf("the good candidate: %v\n%s", err, out)
	}
	if !strings.Contains(running("r3"), "description gated-change") {
		t.Fatal("the good candidate is not on r3")
	}
	if c := changed(report); len(c) != 1 {
		t.Errorf("changed tasks = %v, want only the configuration", c)
	}

	// A failure the model cannot see: r3's direct link to r1 goes down
	// out of band. The model passes, the live verification fails on r3, and
	// the block puts r3 back as it was before this run.
	applied := digest("r3")
	r3Link := iface["r3/r1-r3"]
	lab.Nodes["r3"].Exec(t, "ip", "link", "set", r3Link, "down")
	t.Cleanup(func() { lab.Nodes["r3"].Exec(t, "ip", "link", "set", r3Link, "up") })
	candidate(strings.Replace(good, "description gated-change", "description second-change", 1))
	_, out, err = run()
	if err == nil {
		t.Fatalf("the change with its link down succeeded:\n%s", out)
	}
	if !strings.Contains(out, "undoes") || !strings.Contains(out, "through 10.212.0.18") {
		t.Errorf("the report does not show the verification failing and the undo:\n%s", out)
	}
	if digest("r3") != applied {
		t.Errorf("r3 was not put back after the failed verification:\n%s", running("r3"))
	}
	if strings.Contains(running("r3"), "second-change") {
		t.Error("the undone change is still on r3")
	}
}

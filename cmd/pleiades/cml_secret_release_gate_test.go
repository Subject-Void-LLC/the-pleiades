// Phase 129a's live gate for named secrets: two IOL XE routers built in a
// throwaway CML lab, P1 reached over SSH and P2 through the controller's
// console, protect a GRE tunnel with IKEv2 whose pre-shared key comes from
// `pleiades add-secret` and is named only as {{ secret.gate.psk }}. It proves
// the routers negotiate with the key (an IKEv2 SA READY, authenticated by PSK,
// a ping across the tunnel) and that no report, journal file or text output
// of the runs holds it, a show of the configuration included. It skips
// without the CML sandbox, as every CML test does.
package main_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// gateTunnel is one router's side of the gate's tunnel, as configuration
// lines; the key is a secret reference.
func gateTunnel(local, peer, peerName, tunnelAddr string) string {
	lines := []string{
		"interface Ethernet0/1", " ip address " + local + " 255.255.255.252", " no shutdown",
		"crypto ikev2 proposal GATE", " encryption aes-gcm-256", " prf sha512", " group 19",
		"crypto ikev2 policy GATE", " proposal GATE",
		"crypto ikev2 keyring GATE", " peer " + peerName, "  address " + peer, "  pre-shared-key {{ secret.gate.psk }}",
		"crypto ikev2 profile GATE", " match identity remote address " + peer + " 255.255.255.255",
		" authentication remote pre-share", " authentication local pre-share", " keyring local GATE",
		"crypto ipsec transform-set GATE esp-gcm 256", " mode transport",
		"crypto ipsec profile GATE", " set transform-set GATE", " set ikev2-profile GATE",
		"interface Tunnel0", " ip address " + tunnelAddr + " 255.255.255.252",
		" tunnel source Ethernet0/1", " tunnel destination " + peer, " tunnel protection ipsec profile GATE",
	}
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "        - %q\n", l)
	}
	return b.String()
}

// cmlReport reads a run's --json report from out, which the CML gates
// print after any warning lines: its run id and its tasks.
func cmlReport(t *testing.T, out string) (string, []map[string]any) {
	t.Helper()
	var r struct {
		RunID string           `json:"run_id"`
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&r); err != nil {
		t.Fatalf("not a report: %v\n%s", err, out)
	}
	return r.RunID, r.Tasks
}

// TestCLI_ANamedSecretKeysAnIKEv2TunnelAndNeverShows is the gate.
func TestCLI_ANamedSecretKeysAnIKEv2TunnelAndNeverShows(t *testing.T) {
	env := map[string]string{}
	var missing []string
	for _, k := range cmlBuildSettings {
		if env[k] = os.Getenv(k); env[k] == "" {
			missing = append(missing, k)
		}
	}
	testsupport.Require(t, "a CML controller", len(missing) == 0,
		"set "+strings.Join(missing, ", ")+" (tests/cml/.env.example) and run on the CML sandbox's VPN (tests/cml/vpn.sh)")
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	withHosts := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}
	var outputs []string
	run := func(args ...string) string {
		t.Helper()
		out, err := runPleiadesWithEnv(t, dir, withHosts, args...)
		outputs = append(outputs, out)
		if err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	stdin := func(in string, args ...string) {
		t.Helper()
		if out, err := runPleiadesWithStdin(t, dir, in, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	random := func(prefix string) string {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		return prefix + hex.EncodeToString(b)
	}
	psk := random("Psk-")
	title := fmt.Sprintf("pleiades-secret-gate-%d", time.Now().Unix())

	run("init")
	run("add-host", "cml", "--type", "cml_controller", "--set", "api_base="+env["PLEIADES_CML_URL"], "--set", "tls_fingerprint="+env["PLEIADES_CML_TLS_FINGERPRINT"])
	stdin(env["PLEIADES_CML_PASSWORD"]+"\n", "add-credential", "cml", "--username", env["PLEIADES_CML_USERNAME"], "--password-stdin")
	// P2 is reached through the console, so its address is one no network here
	// carries (TEST-NET-1); it is seeded only because node.create seeds one.
	for name, addr := range map[string]string{"P1": env["PLEIADES_CML_FREE_ADDRESS"], "P2": "192.0.2.2"} {
		run("add-host", name, "--type", "cisco_router", "--set", "host="+addr, "--set", "transport=ssh")
		stdin(random("Gx-")+"\n"+random("Ex-")+"\n", "add-credential", name, "--username", "netops", "--password-stdin", "--enable-password-stdin")
	}
	stdin(psk+"\n", "add-secret", "gate", "--field", "psk", "--value-stdin")

	build := fmt.Sprintf(`# What this does: builds a two-router lab for the named secrets gate.
# Blast radius: the CML controller cml; one new lab, %[1]s.
# Changes: a lab, three nodes and two links are made, and the nodes started.
# Undo: pleiades rollback <run-id> removes everything it made.
id: secret-gate-build
hosts: cml
tasks:
  - name: make the lab
    virt.cml.lab.create:
      title: %[1]s
  - name: the bridge
    virt.cml.node.create:
      lab: %[1]s
      label: ext
      definition: external_connector
      configuration: System Bridge
  - name: P1
    virt.cml.node.create:
      lab: %[1]s
      label: P1
      definition: iol-xe
      device: P1
      management_gateway: %[2]s
  - name: P2
    virt.cml.node.create:
      lab: %[1]s
      label: P2
      definition: iol-xe
      device: P2
      management_gateway: 192.0.2.1
  - name: P1 management
    virt.cml.link.create:
      lab: %[1]s
      a: P1
      a_interface: Ethernet0/0
      b: ext
      b_interface: port
  - name: P1 to P2
    virt.cml.link.create:
      lab: %[1]s
      a: P1
      a_interface: Ethernet0/1
      b: P2
      b_interface: Ethernet0/1
  - name: start the lab
    virt.cml.lab.start:
      lab: %[1]s
      timeout: 1200
`, title, env["PLEIADES_CML_GATEWAY"])
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "build.yaml"), []byte(build), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Whatever failed, the lab does not outlive the gate.
		_, _ = runPleiadesWithEnv(t, dir, withHosts, "adhoc", "cml", "virt.cml.lab.delete", "lab="+title)
	})
	runID, _ := cmlReport(t, run("run", "runbooks/build.yaml", "--json"))

	run("trust-host", "P1", "--first-connect")
	run("trust-host", "cml", "--first-connect")
	run("import-cml", "--controller", "cml", "--lab", title, "--via", "console", "--nodes", "P2", "--replace")

	tunnel := "# What this does: keys a GRE tunnel between P1 and P2 with IKEv2 and a named secret.\n" +
		"# Blast radius: P1 and P2 in the gate's lab. Changes: their running configurations. Undo: the lab is deleted.\n" +
		"id: secret-gate-tunnel\ntasks:\n" +
		"  - name: P1 side\n    net.ios.config:\n      target: P1\n      checkpoint: false\n      lines:\n" + gateTunnel("10.0.12.1", "10.0.12.2", "P2", "172.31.0.1") +
		"  - name: P2 side\n    net.ios.config:\n      target: P2\n      checkpoint: false\n      lines:\n" + gateTunnel("10.0.12.2", "10.0.12.1", "P1", "172.31.0.2") +
		"  - name: across the tunnel\n    net.ios.ping:\n      target: P1\n      dest: 172.31.0.2\n      count: 20\n      state: present\n" +
		"  - name: the SA\n    net.ios.command:\n      target: P1\n      command: show crypto ikev2 sa\n" +
		"  - name: the keyring as configured\n    net.ios.command:\n      target: P1\n      command: show running-config | section keyring\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "tunnel.yaml"), []byte(tunnel), 0o600); err != nil {
		t.Fatal(err)
	}
	run("validate", "runbooks/tunnel.yaml")
	_, tasks := cmlReport(t, run("run", "runbooks/tunnel.yaml", "--json"))
	stdout := func(name string) string {
		for _, task := range tasks {
			if task["name"] == name {
				stats, _ := task["stats"].(map[string]any)
				return fmt.Sprint(stats["stdout"])
			}
		}
		t.Fatalf("no task named %q", name)
		return ""
	}
	if sa := stdout("the SA"); !strings.Contains(sa, "READY") || !strings.Contains(sa, "Auth sign: PSK") {
		t.Fatalf("the IKEv2 SA is not up on the key: %s", sa)
	}
	if keyring := stdout("the keyring as configured"); !strings.Contains(keyring, "pre-shared-key") || !strings.Contains(keyring, "pleiades-masked-") {
		t.Fatalf("the keyring's show is not masked: %s", keyring)
	}
	run("journal", "list")

	for _, out := range outputs {
		if strings.Contains(out, psk) {
			t.Fatal("a run's output holds the pre-shared key")
		}
	}
	if hits := projectHolds(t, dir, psk); len(hits) > 0 {
		t.Fatalf("the project holds the pre-shared key in the clear: %v", hits)
	}

	run("rollback", runID)
	_, tasks = cmlReport(t, run("adhoc", "cml", "virt.cml.lab.info", "lab="+title, "--json"))
	if stats, _ := tasks[0]["stats"].(map[string]any); stats["exists"] != false {
		t.Errorf("the lab is still there after the rollback: %v", tasks[0])
	}
}

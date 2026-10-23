// Release gate for import-cml and the Telnet back end: through the real
// binary, against a real CML 2 controller and a real IOS XE router over
// Telnet (the DevNet CML sandbox, reached over its VPN; tests/cml), a lab
// node is imported, logged in to, read, configured in privileged mode with
// its enable secret, and put back.
package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// cmlGateSettings are the settings the gate reads (tests/cml/.env.example).
var cmlGateSettings = []string{
	"PLEIADES_CML_URL", "PLEIADES_CML_USERNAME", "PLEIADES_CML_PASSWORD", "PLEIADES_CML_TLS_FINGERPRINT",
	"PLEIADES_CML_LAB", "PLEIADES_CML_NODE", "PLEIADES_CML_NODE_ADDRESS",
	"PLEIADES_CML_NODE_USERNAME", "PLEIADES_CML_NODE_PASSWORD", "PLEIADES_CML_NODE_ENABLE",
}

// TestCLI_ImportCMLReachesIOSOverTelnet is the release gate.
func TestCLI_ImportCMLReachesIOSOverTelnet(t *testing.T) {
	env := map[string]string{}
	missing := []string{}
	for _, k := range cmlGateSettings {
		if env[k] = os.Getenv(k); env[k] == "" {
			missing = append(missing, k)
		}
	}
	testsupport.Require(t, "a CML controller", len(missing) == 0,
		"set "+strings.Join(missing, ", ")+" (tests/cml/.env.example) and run on the CML sandbox's VPN (tests/cml/vpn.sh)")
	node := env["PLEIADES_CML_NODE"]
	dir := t.TempDir()
	run := func(stdin string, args ...string) (string, error) {
		if stdin != "" {
			return runPleiadesWithStdin(t, dir, stdin, args...)
		}
		return runPleiadesWithEnv(t, dir, map[string]string{"PLEIADES_CML_PASSWORD": env["PLEIADES_CML_PASSWORD"]}, args...)
	}
	task := func(args ...string) map[string]any {
		t.Helper()
		out, err := run("", append(args, "insecure_telnet=true", "--json")...)
		var report struct {
			Tasks []map[string]any `json:"tasks"`
		}
		if json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&report) != nil || len(report.Tasks) != 1 {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return report.Tasks[0]
	}
	if out, err := run("", "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := run("", "import-cml", "--server", env["PLEIADES_CML_URL"], "--lab", env["PLEIADES_CML_LAB"], "--username", env["PLEIADES_CML_USERNAME"],
		"--tls-fingerprint", env["PLEIADES_CML_TLS_FINGERPRINT"], "--via", "telnet", "--nodes", node, "--address", node+"="+env["PLEIADES_CML_NODE_ADDRESS"], "--json")
	if err != nil || !strings.Contains(out, `"type": "cisco_router"`) || !strings.Contains(out, `"via": "telnet"`) {
		t.Fatalf("import-cml: %v\n%s", err, out)
	}

	// User mode: facts and a command, and configuration refused by name.
	if out, err := run(env["PLEIADES_CML_NODE_PASSWORD"]+"\n", "add-credential", node, "--username", env["PLEIADES_CML_NODE_USERNAME"], "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	facts := task("adhoc", node, "net.ios.facts")
	if stats, _ := facts["stats"].(map[string]any); facts["status"] != "ok" || stats["ansible_net_hostname"] == "" {
		t.Fatalf("net.ios.facts over Telnet = %v", facts)
	}
	brief := task("adhoc", node, "net.ios.command", "command=show ip interface brief")
	if stats, _ := brief["stats"].(map[string]any); !strings.Contains(stats["stdout"].(string), env["PLEIADES_CML_NODE_ADDRESS"]) {
		t.Errorf("show ip interface brief does not list the address the node was imported at: %v", stats["stdout"])
	}
	// A show command reads only, so a second run of a runbook built on one
	// can prove it changes nothing.
	if brief["status"] != "ok" {
		t.Errorf("a show command reported %v, want ok (unchanged)", brief["status"])
	}
	loopback := `lines:=["interface Loopback199","description pleiades-release-gate"]`
	if refused := task("adhoc", node, "net.ios.config", loopback); refused["status"] != "failed" || !strings.Contains(refused["error"].(string), "--enable-password") {
		t.Fatalf("net.ios.config in user mode = %v, want refused naming the enable secret", refused)
	}

	// With the enable secret: the change is made, read back and removed.
	if out, err := run(env["PLEIADES_CML_NODE_PASSWORD"]+"\n"+env["PLEIADES_CML_NODE_ENABLE"]+"\n", "add-credential", node, "--username", env["PLEIADES_CML_NODE_USERNAME"],
		"--password-stdin", "--enable-password-stdin"); err != nil {
		t.Fatalf("add-credential with the enable secret: %v\n%s", err, out)
	}
	t.Cleanup(func() { task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback199"]`) })
	if changed := task("adhoc", node, "net.ios.config", loopback); changed["status"] != "changed" {
		t.Fatalf("net.ios.config with the enable secret = %v", changed)
	}
	read := task("adhoc", node, "net.ios.command", "command=show running-config interface Loopback199")
	if stats, _ := read["stats"].(map[string]any); !strings.Contains(stats["stdout"].(string), "description pleiades-release-gate") {
		t.Errorf("the change is not on the router: %v", stats["stdout"])
	}
	if removed := task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback199"]`); removed["status"] != "changed" {
		t.Errorf("removing it = %v", removed)
	}

	// The same change from a runbook whose task carries a rollback: list,
	// which pleiades rollback replays, since net.ios.config records no
	// undo of its own.
	if err := os.MkdirAll(filepath.Join(dir, "runbooks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "loopback.yaml"), []byte(`# What this does: adds Loopback199 with a description.
# Blast radius: the one CML node.
# Changes: one loopback interface.
# Undo: pleiades rollback <run-id>, through the task's rollback: list.
id: cml_loopback
hosts: `+node+`
tasks:
  - name: Add Loopback199
    net.ios.config:
      insecure_telnet: true
      lines:
        - interface Loopback199
        - description pleiades-release-gate
    rollback:
      - name: Remove Loopback199
        net.ios.config:
          insecure_telnet: true
          lines:
            - no interface Loopback199
`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = run("", "run", "runbooks/loopback.yaml", "--json")
	var ran struct {
		RunID string `json:"run_id"`
	}
	if err != nil || json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&ran) != nil || ran.RunID == "" {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if out, err := run("", "rollback", ran.RunID); err != nil {
		t.Fatalf("rollback through the task's rollback: list: %v\n%s", err, out)
	}
	gone := task("adhoc", node, "net.ios.command", "command=show ip interface brief | include Loopback199")
	if stats, _ := gone["stats"].(map[string]any); strings.TrimSpace(stats["stdout"].(string)) != "" {
		t.Errorf("Loopback199 is still on the router: %v", stats["stdout"])
	}
}

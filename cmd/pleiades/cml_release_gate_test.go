// Release gate for import-cml and the Telnet back end: through the real
// binary, against a real CML 2 controller and a real IOS XE router over
// Telnet (the DevNet CML sandbox, reached over its VPN; tests/cml), a lab
// node is imported, logged in to, read, configured in privileged mode with
// its enable secret, and put back.
package main_test

import (
	"encoding/json"
	"fmt"
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

// cmlGateEnv returns the gate's settings, skipping the test with a "needs"
// line naming each one missing, so a run off the sandbox's VPN (CI) skips.
func cmlGateEnv(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{}
	missing := []string{}
	for _, k := range cmlGateSettings {
		if env[k] = os.Getenv(k); env[k] == "" {
			missing = append(missing, k)
		}
	}
	testsupport.Require(t, "a CML controller", len(missing) == 0,
		"set "+strings.Join(missing, ", ")+" (tests/cml/.env.example) and run on the CML sandbox's VPN (tests/cml/vpn.sh)")
	return env
}

// cmlRunner returns how a gate runs the binary in its project at dir: with
// stdin when there is any (a secret goes in on a pipe, never a flag), and
// otherwise with the controller's password in the environment import-cml
// reads it from and a known_hosts file of the project's own, so trusting
// the controller's console server never writes to the user's.
func cmlRunner(t *testing.T, env map[string]string, dir string) func(string, ...string) (string, error) {
	t.Helper()
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return func(stdin string, args ...string) (string, error) {
		if stdin != "" {
			return runPleiadesWithStdin(t, dir, stdin, args...)
		}
		return runPleiadesWithEnv(t, dir, map[string]string{"PLEIADES_CML_PASSWORD": env["PLEIADES_CML_PASSWORD"], "PLEIADES_KNOWN_HOSTS": knownHosts}, args...)
	}
}

// cmlImportNode makes the project run works in and imports the gate's node
// into it from the CML controller, reached as via says: "telnet", at the
// node's own address, or "console", through the controller's console
// server, which needs no route to the node (a cml_controller device, cml,
// holds the controller's login).
func cmlImportNode(t *testing.T, env map[string]string, run func(string, ...string) (string, error), via string) {
	t.Helper()
	must := func(stdin string, args ...string) string {
		t.Helper()
		out, err := run(stdin, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	must("", "init")
	node := env["PLEIADES_CML_NODE"]
	var out string
	if via == "console" {
		must("", "add-host", "cml", "--type", "cml_controller", "--set", "api_base="+env["PLEIADES_CML_URL"], "--set", "tls_fingerprint="+env["PLEIADES_CML_TLS_FINGERPRINT"])
		must(env["PLEIADES_CML_PASSWORD"]+"\n", "add-credential", "cml", "--username", env["PLEIADES_CML_USERNAME"], "--password-stdin")
		must("", "trust-host", "cml", "--first-connect", "--reason", "the lab controller, trusted by the release gate")
		out = must("", "import-cml", "--controller", "cml", "--lab", env["PLEIADES_CML_LAB"], "--via", "console", "--nodes", node, "--json")
	} else {
		out = must("", "import-cml", "--server", env["PLEIADES_CML_URL"], "--lab", env["PLEIADES_CML_LAB"], "--username", env["PLEIADES_CML_USERNAME"],
			"--tls-fingerprint", env["PLEIADES_CML_TLS_FINGERPRINT"], "--via", "telnet", "--nodes", node, "--address", node+"="+env["PLEIADES_CML_NODE_ADDRESS"], "--json")
	}
	if !strings.Contains(out, `"type": "cisco_router"`) || !strings.Contains(out, `"via": "`+via+`"`) {
		t.Fatalf("import-cml --via %s:\n%s", via, out)
	}
}

// cmlAddEnableCredential stores the node's login with its enable secret,
// which privileged commands (show running-config) need.
func cmlAddEnableCredential(t *testing.T, env map[string]string, run func(string, ...string) (string, error)) {
	t.Helper()
	if out, err := run(env["PLEIADES_CML_NODE_PASSWORD"]+"\n"+env["PLEIADES_CML_NODE_ENABLE"]+"\n", "add-credential", env["PLEIADES_CML_NODE"],
		"--username", env["PLEIADES_CML_NODE_USERNAME"], "--password-stdin", "--enable-password-stdin"); err != nil {
		t.Fatalf("add-credential with the enable secret: %v\n%s", err, out)
	}
}

// TestCLI_ImportCMLReachesIOSOverTelnet is the release gate.
func TestCLI_ImportCMLReachesIOSOverTelnet(t *testing.T) {
	env := cmlGateEnv(t)
	node := env["PLEIADES_CML_NODE"]
	dir := t.TempDir()
	run := cmlRunner(t, env, dir)
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
	cmlImportNode(t, env, run, "telnet")

	// User mode: facts and a command, and configuration refused by name.
	if out, err := run(env["PLEIADES_CML_NODE_PASSWORD"]+"\n", "add-credential", node, "--username", env["PLEIADES_CML_NODE_USERNAME"], "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	facts := task("adhoc", node, "net.ios.facts")
	if stats, _ := facts["stats"].(map[string]any); facts["status"] != "ok" || stats["host.name"] == "" {
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
	if refused := task("adhoc", node, "net.ios.config", loopback, "checkpoint:=false"); refused["status"] != "failed" || !strings.Contains(refused["error"].(string), "--enable-password") {
		t.Fatalf("net.ios.config in user mode = %v, want refused naming the enable secret", refused)
	}

	// With the enable secret: the change is made, read back and removed.
	cmlAddEnableCredential(t, env, run)
	t.Cleanup(func() {
		task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback199"]`, "checkpoint:=false")
	})
	if changed := task("adhoc", node, "net.ios.config", loopback, "checkpoint:=false"); changed["status"] != "changed" {
		t.Fatalf("net.ios.config with the enable secret = %v", changed)
	}
	read := task("adhoc", node, "net.ios.command", "command=show running-config interface Loopback199")
	if stats, _ := read["stats"].(map[string]any); !strings.Contains(stats["stdout"].(string), "description pleiades-release-gate") {
		t.Errorf("the change is not on the router: %v", stats["stdout"])
	}
	if removed := task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback199"]`, "checkpoint:=false"); removed["status"] != "changed" {
		t.Errorf("removing it = %v", removed)
	}

	// The same change from a runbook whose task carries a rollback: list,
	// which pleiades rollback replays over the undo the change recorded.
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
      checkpoint: false
      lines:
        - interface Loopback199
        - description pleiades-release-gate
    rollback:
      - name: Remove Loopback199
        net.ios.config:
          insecure_telnet: true
          checkpoint: false
          lines:
            - no interface Loopback199
`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run("", "run", "runbooks/loopback.yaml", "--json")
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

	cmlCheckpointRoundTrip(t, node, dir, run, task)
	cmlLinesUndoInRun(t, node, dir, run, task)
}

// cmlRun runs a runbook with --json and returns its run ID and tasks.
func cmlRun(t *testing.T, run func(string, ...string) (string, error), path string) (string, []map[string]any) {
	t.Helper()
	out, err := run("", "run", path, "--json")
	var report struct {
		RunID string           `json:"run_id"`
		Tasks []map[string]any `json:"tasks"`
	}
	if json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&report) != nil || report.RunID == "" {
		t.Fatalf("run %s: %v\n%s", path, err, out)
	}
	return report.RunID, report.Tasks
}

// cmlCheckpointRoundTrip: a change keeps a checkpoint on the router, a
// second run of the same lines changes nothing, and pleiades rollback
// returns the router to the checkpoint through net.ios.replace, with no
// rollback: list, leaving no checkpoint file behind.
func cmlCheckpointRoundTrip(t *testing.T, node, dir string, run func(string, ...string) (string, error), task func(...string) map[string]any) {
	t.Helper()
	t.Cleanup(func() {
		task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback197"]`, "checkpoint:=false")
	})
	path := filepath.Join("runbooks", "checkpoint.yaml")
	writeCMLRunbook(t, dir, path, `# What this does: adds Loopback197 with a description, keeping a checkpoint.
# Blast radius: the one CML node.
# Changes: one loopback interface, and two checkpoint files on the router.
# Undo: pleiades rollback <run-id>, which returns the router to the checkpoint.
id: cml_checkpoint
hosts: `+node+`
tasks:
  - name: Add Loopback197
    net.ios.config:
      insecure_telnet: true
      lines:
        - interface Loopback197
        - description pleiades-checkpoint
`)
	readInterfaces := func() string {
		t.Helper()
		read := task("adhoc", node, "net.ios.parse", "command=show ip interface brief")
		stats, _ := read["stats"].(map[string]any)
		parsed, err := json.Marshal(stats["parsed"])
		if read["status"] != "ok" || stats["parser"] != "ShowIpInterfaceBrief" || err != nil || !strings.Contains(string(parsed), `"interface"`) {
			t.Fatalf("net.ios.parse show ip interface brief = %v", read)
		}
		return string(parsed)
	}
	before := readInterfaces()
	first, tasks := cmlRun(t, run, path)
	stats, _ := tasks[0]["stats"].(map[string]any)
	if tasks[0]["status"] != "changed" || !strings.HasPrefix(fmt.Sprint(stats["checkpoint"]), "unix:pleiades-") && !strings.HasPrefix(fmt.Sprint(stats["checkpoint"]), "flash:pleiades-") {
		t.Fatalf("the change = %v", tasks[0])
	}
	if !strings.Contains(fmt.Sprint(stats["differences"]), "+interface Loopback197") {
		t.Errorf("the router's own differences do not name the change: %v", stats["differences"])
	}
	if _, again := cmlRun(t, run, path); again[0]["status"] != "ok" {
		t.Errorf("the same lines a second time = %v, want ok (unchanged)", again[0])
	}
	if out, err := run("", "rollback", first); err != nil {
		t.Fatalf("rollback through the checkpoint: %v\n%s", err, out)
	}
	gone := task("adhoc", node, "net.ios.command", "command=show ip interface brief | include Loopback197")
	if stats, _ := gone["stats"].(map[string]any); strings.TrimSpace(fmt.Sprint(stats["stdout"])) != "" {
		t.Errorf("Loopback197 is still on the router: %v", stats["stdout"])
	}
	// The router's interfaces, parsed, are as they were before the change.
	if after := readInterfaces(); after != before {
		t.Errorf("the interfaces after the rollback differ from before the change:\nbefore %s\nafter  %s", before, after)
	}
	_, name, _ := strings.Cut(fmt.Sprint(stats["checkpoint"]), ":")
	files := task("adhoc", node, "net.ios.command", "command=dir | include "+strings.TrimSuffix(name, ".cfg"))
	if stats, _ := files["stats"].(map[string]any); files["status"] != "ok" || strings.TrimSpace(fmt.Sprint(stats["stdout"])) != "" {
		t.Errorf("the rolled back change's checkpoint files were left on the router: %v", files)
	}
}

// cmlLinesUndoInRun: a block's rollback_on_failure returns the router to
// the checkpoint its change kept when a later task fails, inside the run;
// and with checkpoint: false a change records no undo and shows the lines
// that would undo it.
func cmlLinesUndoInRun(t *testing.T, node, dir string, run func(string, ...string) (string, error), task func(...string) map[string]any) {
	t.Helper()
	t.Cleanup(func() {
		task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback196"]`, "checkpoint:=false")
		task("adhoc", node, "net.ios.config", `lines:=["no interface Loopback195"]`, "checkpoint:=false")
	})
	path := filepath.Join("runbooks", "in_run.yaml")
	writeCMLRunbook(t, dir, path, `# What this does: adds Loopback196, then fails on purpose, so the block undoes it.
# Blast radius: the one CML node.
# Changes: none once the block has undone itself.
# Undo: the block returns the router to the change's checkpoint when the failing task fails.
id: cml_in_run_undo
hosts: `+node+`
tasks:
  - name: Change, then fail
    rollback_on_failure: true
    block:
      - name: Add Loopback196
        net.ios.config:
          insecure_telnet: true
          lines:
            - interface Loopback196
            - description pleiades-in-run-undo
      - name: Fail on purpose
        net.ios.command:
          insecure_telnet: true
          command: show pleiades-no-such-command
        rollback: []
`)
	_, tasks := cmlRun(t, run, path)
	undone := false
	for _, tk := range tasks {
		if strings.Contains(fmt.Sprint(tk["id"]), ".undo[") && tk["status"] == "changed" {
			undone = true
		}
	}
	if !undone {
		t.Errorf("no undo step ran in the run: %v", tasks)
	}
	gone := task("adhoc", node, "net.ios.command", "command=show ip interface brief | include Loopback196")
	if stats, _ := gone["stats"].(map[string]any); strings.TrimSpace(fmt.Sprint(stats["stdout"])) != "" {
		t.Errorf("the block did not undo its change: Loopback196 is on the router: %v", stats["stdout"])
	}

	lines := task("adhoc", node, "net.ios.config", `lines:=["interface Loopback195","description pleiades-no-checkpoint"]`, "checkpoint:=false")
	stats, _ := lines["stats"].(map[string]any)
	if lines["status"] != "changed" || !strings.Contains(fmt.Sprint(stats["undo_lines"]), "no interface Loopback195") || !strings.Contains(fmt.Sprint(stats["warnings"]), "rollback: list") {
		t.Errorf("a change with no checkpoint = %v", lines)
	}
}

// writeCMLRunbook writes a runbook into the gate's project at dir.
func writeCMLRunbook(t *testing.T, dir, path, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

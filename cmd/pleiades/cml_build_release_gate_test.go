// Release gate for virt.cml and the console transport: through the real
// binary, against a real CML 2 controller (the DevNet CML sandbox, reached
// over its VPN; tests/cml), a lab is built by a runbook with a router
// seeded from an inventory device's login and address, the router is
// logged in to over SSH with that login and then through the controller's
// console server, and the build is rolled back until the lab is gone.
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

// cmlBuildSettings are the settings this gate reads (tests/cml/.env.example).
var cmlBuildSettings = []string{
	"PLEIADES_CML_URL", "PLEIADES_CML_USERNAME", "PLEIADES_CML_PASSWORD", "PLEIADES_CML_TLS_FINGERPRINT",
	"PLEIADES_CML_FREE_ADDRESS", "PLEIADES_CML_GATEWAY",
}

// TestCLI_BuildsACMLLabAndReachesItsNode is the release gate.
func TestCLI_BuildsACMLLabAndReachesItsNode(t *testing.T) {
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
	run := func(args ...string) string {
		t.Helper()
		out, err := runPleiadesWithEnv(t, dir, withHosts, args...)
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
	report := func(out string) (string, []map[string]any) {
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
	secret := func() string {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		return "Gx-" + hex.EncodeToString(b)
	}
	title := fmt.Sprintf("pleiades-gate-%d", time.Now().Unix())

	run("init")
	run("add-host", "cml", "--type", "cml_controller", "--set", "api_base="+env["PLEIADES_CML_URL"], "--set", "tls_fingerprint="+env["PLEIADES_CML_TLS_FINGERPRINT"])
	stdin(env["PLEIADES_CML_PASSWORD"]+"\n", "add-credential", "cml", "--username", env["PLEIADES_CML_USERNAME"], "--password-stdin")
	run("add-host", "P1", "--type", "cisco_router", "--set", "host="+env["PLEIADES_CML_FREE_ADDRESS"], "--set", "transport=ssh")
	stdin(secret()+"\n"+secret()+"\n", "add-credential", "P1", "--username", "netops", "--password-stdin", "--enable-password-stdin")
	runbook := fmt.Sprintf(`# What this does: builds a one-router lab for the release gate.
# Blast radius: the CML controller cml; one new lab, %[1]s.
# Changes: a lab, two nodes and a link are made, and both nodes started.
# Undo: pleiades rollback <run-id> removes everything it made.
id: gate-build
hosts: cml
tasks:
  - name: make the lab
    virt.cml.lab.create:
      title: %[1]s
  - name: the bridge to the management network
    virt.cml.node.create:
      lab: %[1]s
      label: ext
      definition: external_connector
      configuration: System Bridge
  - name: the router, standing for P1
    virt.cml.node.create:
      lab: %[1]s
      label: P1
      definition: iol-xe
      device: P1
      management_gateway: %[2]s
  - name: link its management port to the bridge
    virt.cml.link.create:
      lab: %[1]s
      a: P1
      a_interface: Ethernet0/0
      b: ext
      b_interface: port
  - name: start the lab
    virt.cml.lab.start:
      lab: %[1]s
      timeout: 1200
`, title, env["PLEIADES_CML_GATEWAY"])
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "build.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Whatever failed, the lab does not outlive the gate.
		_, _ = runPleiadesWithEnv(t, dir, withHosts, "adhoc", "cml", "virt.cml.lab.delete", "lab="+title)
	})
	run("validate", "runbooks/build.yaml")
	runID, tasks := report(run("run", "runbooks/build.yaml", "--json"))
	for _, task := range tasks {
		if task["status"] != "changed" {
			t.Fatalf("build task %v = %v", task["name"], task)
		}
	}

	// The seeded login, over SSH at the seeded address.
	run("trust-host", "P1", "--first-connect")
	_, tasks = report(run("adhoc", "P1", "net.ios.command", "command=show privilege", "--json"))
	if stats, _ := tasks[0]["stats"].(map[string]any); !strings.Contains(fmt.Sprint(stats["stdout"]), "level is 15") {
		t.Fatalf("show privilege over SSH = %v", tasks[0])
	}

	// The same login, through the controller's console server.
	run("trust-host", "cml", "--first-connect")
	run("import-cml", "--controller", "cml", "--lab", title, "--via", "console", "--nodes", "P1", "--replace")
	_, tasks = report(run("adhoc", "P1", "net.ios.command", "command=show users", "--json"))
	if stats, _ := tasks[0]["stats"].(map[string]any); !strings.Contains(fmt.Sprint(stats["stdout"]), "con 0") {
		t.Fatalf("show users over the console = %v", tasks[0])
	}

	// The build's undo removes everything it made.
	run("rollback", runID)
	_, tasks = report(run("adhoc", "cml", "virt.cml.lab.info", "lab="+title, "--json"))
	if stats, _ := tasks[0]["stats"].(map[string]any); stats["exists"] != false {
		t.Errorf("the lab is still there after the rollback: %v", tasks[0])
	}
}

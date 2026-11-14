// Release gate for NETCONF and RESTCONF from Day 0 to Day N: through the real
// binary, against a real CML 2 controller (the DevNet CML sandbox, reached over
// its VPN; tests/cml), a Catalyst 8000V is built with NETCONF and RESTCONF on
// from its first boot, its listeners trusted and its API onboarded the way a
// user does it, configured, changed, refused, switched to its candidate
// datastore and back, read over RESTCONF, saved, rolled back, and torn down.
// Every session discovers the router's YANG modules and datastores, and the
// datastore each task writes follows what the router offers that session
// (owner decision 2026-11-14).
package main_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// cmlNetconfSettings are the settings this gate reads (tests/cml/.env.example).
var cmlNetconfSettings = []string{
	"PLEIADES_CML_URL", "PLEIADES_CML_USERNAME", "PLEIADES_CML_PASSWORD", "PLEIADES_CML_TLS_FINGERPRINT",
	"PLEIADES_CML_FREE_ADDRESS", "PLEIADES_CML_GATEWAY",
}

// nativeNS is the IOS XE native model's namespace.
const nativeNS = "http://cisco.com/ns/yang/Cisco-IOS-XE-native"

// netconfGate is the gate's project, its binary and its devices.
type netconfGate struct {
	t     *testing.T
	dir   string
	hosts map[string]string
}

// run runs the binary, failing the test on an error.
func (g *netconfGate) run(args ...string) string {
	g.t.Helper()
	out, err := runPleiadesWithEnv(g.t, g.dir, g.hosts, args...)
	if err != nil {
		g.t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// try runs the binary and returns its output and error.
func (g *netconfGate) try(args ...string) (string, error) {
	return runPleiadesWithEnv(g.t, g.dir, g.hosts, args...)
}

// task runs one method with adhoc and returns its one task, refusing a status
// other than want.
func (g *netconfGate) task(want string, args ...string) map[string]any {
	g.t.Helper()
	out, _ := g.try(append([]string{"adhoc"}, append(args, "--json")...)...)
	_, tasks := cmlReport(g.t, out)
	if len(tasks) != 1 || tasks[0]["status"] != want {
		g.t.Fatalf("adhoc %s = %v, want status %s\n%s", strings.Join(args, " "), tasks, want, out)
	}
	return tasks[0]
}

// stats is a task's stats.
func stats(task map[string]any) map[string]any {
	s, _ := task["stats"].(map[string]any)
	return s
}

// show is the output of one exec command on the router, over its CLI.
func (g *netconfGate) show(command string) string {
	g.t.Helper()
	return fmt.Sprint(stats(g.task("ok", "R", "net.ios.command", "command="+command))["stdout"])
}

// loopbacks are the loopback interfaces the router's running config holds.
func (g *netconfGate) loopbacks() []string {
	g.t.Helper()
	return regexp.MustCompile(`(?m)^interface (Loopback\d+)`).FindAllString(g.show("show running-config | include ^interface Loopback"), -1)
}

// waitFor retries check until it reports true, failing after limit.
func (g *netconfGate) waitFor(what string, limit time.Duration, check func() bool) {
	g.t.Helper()
	deadline := time.Now().Add(limit)
	for !check() {
		if time.Now().After(deadline) {
			g.t.Fatalf("waited %s for %s", limit, what)
		}
		time.Sleep(15 * time.Second)
	}
}

// runbook writes a runbook into the project and runs it, returning the run id
// and its tasks.
func (g *netconfGate) runbook(name, body string, wantFail bool) (string, []map[string]any) {
	g.t.Helper()
	path := filepath.Join("runbooks", name+".yaml")
	if err := os.WriteFile(filepath.Join(g.dir, path), []byte(body), 0o600); err != nil {
		g.t.Fatal(err)
	}
	out, err := g.try("run", path, "--json")
	if (err != nil) != wantFail {
		g.t.Fatalf("run %s: %v (want failure %v)\n%s", name, err, wantFail, out)
	}
	return cmlReport(g.t, out)
}

// netconfTask is a runbook task applying doc over NETCONF with extra params.
func netconfTask(name, doc string, extra ...string) string {
	b := "  - name: " + name + "\n    net.netconf.config:\n"
	for _, e := range extra {
		b += "      " + e + "\n"
	}
	return b + "      content: '" + doc + "'\n"
}

// header is a runbook's opening: what it does, its blast radius, its changes
// and its undo, then its id and hosts.
func header(id, does, hosts string) string {
	return "# What this does: " + does + "\n# Blast radius: the gate's lab router.\n# Changes: what the tasks name.\n# Undo: the gate's later steps and its teardown.\nid: " + id + "\nhosts: " + hosts + "\ntasks:\n"
}

// TestCLI_NETCONFAndRESTCONFFromDayZeroToDayN is the release gate.
func TestCLI_NETCONFAndRESTCONFFromDayZeroToDayN(t *testing.T) {
	env := map[string]string{}
	var missing []string
	for _, k := range cmlNetconfSettings {
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
	g := &netconfGate{t: t, dir: dir, hosts: map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}}
	secret := func() string {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		return "Gx-" + hex.EncodeToString(b)
	}
	stdin := func(in string, args ...string) {
		t.Helper()
		if out, err := runPleiadesWithStdin(t, dir, in, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	addr, title := env["PLEIADES_CML_FREE_ADDRESS"], fmt.Sprintf("pleiades-netconf-gate-%d", time.Now().Unix())
	login := secret()

	// Day 0: the router, built with NETCONF, RESTCONF and local AAA on from
	// its first boot, and its API device beside it.
	g.run("init")
	g.run("add-host", "cml", "--type", "cml_controller", "--set", "api_base="+env["PLEIADES_CML_URL"], "--set", "tls_fingerprint="+env["PLEIADES_CML_TLS_FINGERPRINT"])
	stdin(env["PLEIADES_CML_PASSWORD"]+"\n", "add-credential", "cml", "--username", env["PLEIADES_CML_USERNAME"], "--password-stdin")
	g.run("add-host", "R", "--type", "cisco_router", "--set", "host="+addr, "--set", "transport=ssh", "--set", "ephemeral=true", "--set", "netconf_enabled=true")
	stdin(login+"\n"+secret()+"\n", "add-credential", "R", "--username", "netops", "--password-stdin", "--enable-password-stdin")
	g.run("add-host", "RAPI", "--type", "generic_http", "--set", "base_url=https://"+addr+"/restconf", "--set", "http_auth=basic")
	stdin(login+"\n", "add-credential", "RAPI", "--username", "netops", "--password-stdin")
	buildID, tasks := g.runbook("build", fmt.Sprintf(`# What this does: Day 0 for a Catalyst 8000V with NETCONF, RESTCONF and local AAA on from its first boot.
# Blast radius: the CML controller cml; one new lab, %[1]s.
# Changes: a lab, two nodes and a link are made, and both nodes started.
# Undo: pleiades rollback <run-id> removes everything it made.
id: netconf-gate-build
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
  - name: the router, standing for R
    virt.cml.node.create:
      lab: %[1]s
      label: R
      definition: cat8000v
      device: R
      management_interface: GigabitEthernet1
      management_gateway: %[2]s
      configuration: |
        aaa new-model
        aaa authentication login default local
        aaa authorization exec default local
        netconf-yang
        restconf
        ip http secure-server
        ip http authentication local
  - name: link its management port to the bridge
    virt.cml.link.create:
      lab: %[1]s
      a: R
      a_interface: GigabitEthernet1
      b: ext
      b_interface: port
  - name: start the lab
    virt.cml.lab.start:
      lab: %[1]s
      timeout: 1800
`, title, env["PLEIADES_CML_GATEWAY"]), false)
	t.Cleanup(func() {
		// Whatever failed, the lab does not outlive the gate.
		_, _ = g.try("adhoc", "cml", "virt.cml.lab.delete", "lab="+title)
	})
	for _, task := range tasks {
		if task["status"] != "changed" {
			t.Fatalf("build task %v = %v", task["name"], task)
		}
	}
	g.run("trust-host", "R", "--first-connect")
	g.waitFor("the router's YANG management to start", 15*time.Minute, func() bool {
		out, _ := g.try("adhoc", "R", "net.ios.command", "command=show platform software yang-management process state", "--json")
		return strings.Contains(out, "Confd Status: Started")
	})
	// Its NETCONF listener has a key of its own, which the first session is
	// refused for, naming the command that trusts it.
	if out, _ := g.try("adhoc", "R", "net.netconf.facts", "--json"); !strings.Contains(out, "trust-host R --first-connect --netconf") {
		t.Fatalf("an untrusted NETCONF listener's refusal does not name the command:\n%s", out)
	}
	g.run("trust-host", "R", "--first-connect", "--netconf")
	// The API's self-signed certificate is refused, naming its fingerprint,
	// which is then pinned.
	refused, err := g.try("onboard", "RAPI")
	pin := regexp.MustCompile(`fingerprint (sha256:[0-9a-f]{64})`).FindStringSubmatch(refused)
	if err == nil || pin == nil {
		t.Fatalf("onboarding an API whose certificate nothing trusts: %v\n%s", err, refused)
	}
	g.run("set-host", "RAPI", "--set", "tls_fingerprint="+pin[1])
	if out := g.run("onboard", "RAPI"); !strings.Contains(out, "status: 200") {
		t.Fatalf("onboard RAPI:\n%s", out)
	}

	// Day 1 over NETCONF, no datastore named: the session chooses running,
	// which this router offers until its candidate is turned on.
	_, tasks = g.runbook("day1", header("day1", "Day 1 over NETCONF.", "R")+netconfTask("loopback 100", `<native xmlns="`+nativeNS+`"><interface><Loopback><name>100</name><description>day1</description></Loopback></interface></native>`), false)
	s := stats(tasks[0])
	if ds := fmt.Sprint(s["netconf.datastores"]); strings.Contains(ds, "candidate") || !strings.Contains(ds, "running") {
		t.Errorf("netconf.datastores = %s, want running and no candidate", ds)
	}
	if s["yang.completeness"] != "complete" || toInt(s["yang.module_count"]) < 100 {
		t.Errorf("yang.completeness = %v, yang.module_count = %v; want the router's whole library", s["yang.completeness"], s["yang.module_count"])
	}
	if mods, _ := s["yang.modules"].([]any); !slices.ContainsFunc(mods, func(m any) bool { return strings.HasPrefix(fmt.Sprint(m), "Cisco-IOS-XE-native@") }) {
		t.Errorf("yang.modules has no Cisco-IOS-XE-native: %d entries", len(mods))
	}
	if got := g.loopbacks(); !slices.Contains(got, "interface Loopback100") {
		t.Fatalf("loopbacks after Day 1 = %v", got)
	}

	// Day 2: a change, a refused document left unapplied, a namespace the
	// router does not serve refused before it is sent, and a delete.
	g.runbook("day2", header("day2", "Day 2 over NETCONF.", "R")+
		netconfTask("change it", `<native xmlns="`+nativeNS+`"><interface><Loopback><name>100</name><description>day2</description></Loopback></interface></native>`), false)
	if out := g.show("show running-config interface Loopback100"); !strings.Contains(out, "description day2") {
		t.Errorf("Loopback100 after the change:\n%s", out)
	}
	// Loopback100 takes an address; a document whose second loopback claims
	// the same one passes the schema, and the router refuses it partway,
	// after applying the first, which rollback-on-error takes back.
	g.run("adhoc", "R", "net.netconf.config", `content=<native xmlns="`+nativeNS+`"><interface><Loopback><name>100</name><ip><address><primary><address>192.0.2.1</address><mask>255.255.255.255</mask></primary></address></ip></Loopback></interface></native>`)
	g.task("failed", "R", "net.netconf.config", `content=<native xmlns="`+nativeNS+`"><interface><Loopback><name>101</name></Loopback><Loopback><name>102</name><ip><address><primary><address>192.0.2.1</address><mask>255.255.255.255</mask></primary></address></ip></Loopback></interface></native>`)
	if got := g.loopbacks(); slices.Contains(got, "interface Loopback101") {
		t.Errorf("a document the router refused partway was left half applied: %v", got)
	}
	unserved := g.task("failed", "R", "net.netconf.config", `content=<widgets xmlns="urn:example:not-on-this-router"/>`)
	if !strings.Contains(fmt.Sprint(unserved["error"]), "nothing was sent") {
		t.Errorf("an unserved namespace: %v", unserved["error"])
	}
	g.run("adhoc", "R", "net.netconf.config", "default_operation=none", "lock=always",
		`content=<native xmlns="`+nativeNS+`" xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0"><interface><Loopback nc:operation="delete"><name>100</name></Loopback></interface></native>`)
	if got := g.loopbacks(); slices.Contains(got, "interface Loopback100") {
		t.Errorf("Loopback100 survived its delete: %v", got)
	}

	// The candidate datastore turned on: IOS XE withdraws :writable-running,
	// and the next session writes the candidate, locked, validated and
	// committed, with no change to the task.
	g.task("changed", "R", "net.ios.config", "lines:=[\"netconf-yang feature candidate-datastore\"]", "checkpoint=false")
	g.waitFor("the candidate datastore to be offered", 10*time.Minute, func() bool {
		out, _ := g.try("adhoc", "R", "net.netconf.facts", "--json")
		return strings.Contains(out, `"candidate"`)
	})
	g.task("changed", "R", "net.netconf.config", `content=<native xmlns="`+nativeNS+`"><interface><Loopback><name>200</name></Loopback></interface></native>`)
	if got := g.loopbacks(); !slices.Contains(got, "interface Loopback200") {
		t.Errorf("an edit through the candidate was not committed: %v", got)
	}
	validated := g.task("ok", "R", "net.netconf.config", "commit=false", `content=<native xmlns="`+nativeNS+`"><interface><Loopback><name>201</name></Loopback></interface></native>`)
	if stats(validated)["validated"] != true || stats(validated)["applied"] != false || slices.Contains(g.loopbacks(), "interface Loopback201") {
		t.Errorf("commit: false = %v, loopbacks %v; want validated, discarded and not applied", stats(validated), g.loopbacks())
	}
	if history := g.run("facts", "R", "netconf.datastores"); strings.Count(history, "netconf.datastores =") < 2 {
		t.Errorf("the fact history does not hold the datastores before and after:\n%s", history)
	}

	// RESTCONF over the onboarded API: create, a duplicate refused, patch,
	// replace, read back, delete, gone, and the configuration saved.
	headers := `headers:={"Content-Type":"application/yang-data+json","Accept":"application/yang-data+json"}`
	lo := "/data/Cisco-IOS-XE-native:native/interface/Loopback=300"
	g.task("changed", "RAPI", "http.request", "url=/data/Cisco-IOS-XE-native:native/interface", "method=POST", headers, `body={"Cisco-IOS-XE-native:Loopback":[{"name":300,"description":"over restconf"}]}`, "status_code=201")
	g.task("changed", "RAPI", "http.request", "url=/data/Cisco-IOS-XE-native:native/interface", "method=POST", headers, `body={"Cisco-IOS-XE-native:Loopback":[{"name":300}]}`, "status_code=409")
	g.task("changed", "RAPI", "http.request", "url="+lo, "method=PATCH", headers, `body={"Cisco-IOS-XE-native:Loopback":[{"name":300,"description":"patched"}]}`, "status_code=204")
	g.task("changed", "RAPI", "http.request", "url="+lo, "method=PUT", headers, `body={"Cisco-IOS-XE-native:Loopback":[{"name":300,"description":"replaced"}]}`, "status_code=204")
	if body := fmt.Sprint(stats(g.task("ok", "RAPI", "http.request", "url="+lo, headers, "status_code=200"))["content"]); !strings.Contains(body, "replaced") {
		t.Errorf("the replaced loopback read back as %s", body)
	}
	g.task("changed", "RAPI", "http.request", "url="+lo, "method=DELETE", "status_code=204")
	g.task("ok", "RAPI", "http.request", "url="+lo, headers, "status_code=404")
	g.task("changed", "RAPI", "http.request", "url=/operations/cisco-ia:save-config", "method=POST", headers, "status_code:=[200,204]")

	// Day N: a NETCONF change undone by its rollback: list, after a CLI
	// checkpoint the run's rollback returns the router to.
	undoRun, _ := g.runbook("dayn", header("dayn", "Day N: a checkpoint, then a NETCONF change with its undo.", "R")+
		"  - name: checkpoint\n    net.ios.config:\n      checkpoint: true\n      lines:\n        - interface Loopback200\n        - description checkpointed\n"+
		netconfTask("the change", `<native xmlns="`+nativeNS+`"><interface><Loopback><name>301</name></Loopback></interface></native>`)+
		"    rollback:\n      - name: remove Loopback301\n        net.netconf.config:\n          default_operation: none\n          content: '<native xmlns=\""+nativeNS+"\" xmlns:nc=\"urn:ietf:params:xml:ns:netconf:base:1.0\"><interface><Loopback nc:operation=\"delete\"><name>301</name></Loopback></interface></native>'\n", false)
	if got := g.loopbacks(); !slices.Contains(got, "interface Loopback301") {
		t.Fatalf("Day N's change did not land: %v", got)
	}
	g.run("rollback", undoRun)
	if got := g.loopbacks(); slices.Contains(got, "interface Loopback301") {
		t.Errorf("Loopback301 survived the rollback: %v", got)
	}
	if out := g.show("show running-config interface Loopback200"); strings.Contains(out, "checkpointed") {
		t.Errorf("the checkpoint's change survived the rollback:\n%s", out)
	}

	// Teardown: the build's undo removes everything it made.
	g.run("rollback", buildID)
	_, tasks = cmlReport(t, g.run("adhoc", "cml", "virt.cml.lab.info", "lab="+title, "--json"))
	if s := stats(tasks[0]); s["exists"] != false {
		t.Errorf("the lab is still there after the rollback: %v", tasks[0])
	}
}

// toInt is a JSON number as an int, 0 for anything else.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// Release gate for Phase 117a's IOS XE half: the ticket runbook's shape
// with a real IOS XE router as the configuration item. Through the real
// binary, a critical incident names the CML sandbox's iol-xe node (reached
// over Telnet, its only login service); the run
// collects that router's facts with net.ios.facts, renders the first
// interface's name from them through | cli_token into a net.ios.command,
// and writes the command's output to the incident's work notes. The ticket
// system is ServiceNo! (tests/serviceno), or a real instance when
// PLEIADES_SNOW_INSTANCE is set, exactly as TestServiceNowRunbookReleaseGate.
package main_test

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// snowIOSXERunbook reads the incident, learns the router its configuration
// item names (bounded by within:), reads that router's first interface's
// configuration with the interface's name rendered from the facts, and
// writes what it read to the work notes when the incident is critical.
const snowIOSXERunbook = `# What this does: collects from the router a ServiceNow incident names and writes it to the incident.
# Blast radius: one router, only read; the incident's work notes.
# Changes: a work note on a critical incident.
# Undo: nothing on the router to undo; a work note stays in the incident's journal.
id: snow_iosxe
tasks:
  - name: read the incident
    http.request:
      target: snow
      url: "/api/now/table/incident?sysparm_query=number%3D{{ vars.ticket | urlencode }}&sysparm_limit=1&sysparm_display_value=true&sysparm_exclude_reference_link=true"
    register: incident
  - name: learn the router
    net.ios.facts:
      target: "{{ result.incident.json.result[0].cmdb_ci }}"
      insecure_telnet: true
      gather_subset:
        - min
        - interfaces
    within: routers
    register: facts
  - name: read the first interface's configuration
    net.ios.command:
      target: "{{ result.incident.json.result[0].cmdb_ci }}"
      insecure_telnet: true
      command: "show running-config interface {{ result.facts['network.interfaces'][0].name | cli_token }}"
    within: routers
    register: collect
  - name: write the finding to the work notes
    http.request:
      target: snow
      url: "/api/now/table/incident/{{ result.incident.json.result[0].sys_id | urlencode }}"
      method: PATCH
      headers:
        Content-Type: application/json
      body: '{"work_notes": {{ result.collect.stdout | to_json }}}'
    when_cel: "result.incident.json.result[0].priority == '1 - Critical'"
`

// TestServiceNowRunbookCollectsFromIOSXE is the release gate.
func TestServiceNowRunbookCollectsFromIOSXE(t *testing.T) {
	env := cmlGateEnv(t)
	if testing.Short() {
		t.Skip("starts ServiceNo! in a container")
	}
	node := env["PLEIADES_CML_NODE"]
	snow := snowUnderTest(t)
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	ciID, _ := snow.create(t, "cmdb_ci", map[string]any{"name": node})

	dir := t.TempDir()
	run := cmlRunner(t, env, dir)
	cmlImportNode(t, env, run, "telnet")
	cmlAddEnableCredential(t, env, run)
	snowEnv := map[string]string{}
	if snow.caFile != "" {
		snowEnv["SSL_CERT_FILE"], snowEnv["SSL_CERT_DIR"] = snow.caFile, filepath.Dir(snow.caFile)
	}
	for _, args := range [][]string{
		{"set-host", node, "--tags", "routers"},
		{"add-host", "snow", "--type", "generic_http", "--set", "base_url=" + snow.base, "--set", "http_auth=basic"},
		{"add-credential", "snow", "--username", snow.user, "--password", snow.password},
	} {
		if out, err := runPleiadesWithEnv(t, dir, snowEnv, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, snowEnv, "snow")
	path := filepath.Join("runbooks", "snow_iosxe.yaml")
	writeCMLRunbook(t, dir, path, snowIOSXERunbook)

	// The interface's name is data the router returned, so the command
	// renders it only through | cli_token: without it, validate refuses.
	unquoted := filepath.Join("runbooks", "snow_iosxe_unquoted.yaml")
	writeCMLRunbook(t, dir, unquoted, strings.Replace(snowIOSXERunbook, " | cli_token }}", " }}", 1))
	if out, err := runPleiadesWithEnv(t, dir, snowEnv, "validate", unquoted, "-e", "ticket=INC0000000"); err == nil || !strings.Contains(out, "parameter command") {
		t.Fatalf("validate accepted an interface name rendered into a command without | cli_token: %v\n%s", err, out)
	}

	critical, number := snow.create(t, "incident", map[string]any{"short_description": "Interface check on " + node + " " + hex.EncodeToString(suffix), "cmdb_ci": ciID, "impact": "1", "urgency": "1"})
	out, err := runPleiadesWithEnv(t, dir, snowEnv, "run", path, "-e", "ticket="+number, "--json")
	if err != nil {
		t.Fatalf("the runbook failed on a critical incident: %v\n%s", err, out)
	}
	tasks := reportTasks(decodeReport(t, out, err))
	if len(tasks) != 4 {
		t.Fatalf("the run reports %d tasks, want 4:\n%s", len(tasks), out)
	}

	// The facts are the router's own: its hostname, IOS XE, and the address
	// it was imported at on one of its interfaces.
	facts, _ := tasks[1]["stats"].(map[string]any)
	if facts["host.name"] != node || facts["os.distribution"] != "ios_xe" {
		t.Errorf("net.ios.facts = host.name %v, os.distribution %v, want %s and ios_xe", facts["host.name"], facts["os.distribution"], node)
	}
	ifaces, _ := facts["network.interfaces"].([]any)
	if len(ifaces) == 0 {
		t.Fatalf("net.ios.facts returned no interfaces: %v", facts)
	}
	imported := false
	for _, i := range ifaces {
		if m, _ := i.(map[string]any); m["ip_address"] == env["PLEIADES_CML_NODE_ADDRESS"] {
			imported = true
		}
	}
	if !imported {
		t.Errorf("no interface holds the address %s was imported at: %v", node, ifaces)
	}

	// The command rendered the first interface's name, and its output is
	// that interface's configuration, which is what reached the incident.
	first, _ := ifaces[0].(map[string]any)
	name, _ := first["name"].(string)
	collect, _ := tasks[2]["stats"].(map[string]any)
	stdout, _ := collect["stdout"].(string)
	want := []string{"interface " + name}
	if addr, ok := first["ip_address"].(string); ok {
		want = append(want, "ip address "+addr+" ")
	}
	for _, w := range want {
		if !strings.Contains(stdout, w) {
			t.Errorf("show running-config interface %s does not hold %q:\n%s", name, w, stdout)
		}
	}
	notes := snow.workNotes(t, critical)
	if len(notes) != 1 || !strings.Contains(notes[0], "interface "+name) {
		t.Fatalf("the critical incident's work notes are %q, want %s's configuration of %s\n%s", notes, node, name, out)
	}
	if strings.Contains(out, snow.password) {
		t.Error("the run printed the ticket system's password")
	}
	t.Logf("rendered %q from %s's facts; the work note holds %d bytes", "show running-config interface "+name, node, len(notes[0]))
}

// Release gate for RESTCONF from Day 0 to Day N: through the real binary,
// against a real CML 2 controller (the DevNet CML sandbox, reached over its
// VPN; tests/cml), a Catalyst 8000V is built with RESTCONF on from its first
// boot, its certificate pinned and its RESTCONF server onboarded the way a
// user does it (generic_restconf), read, configured, changed, refused, given
// a named secret, restarted and waited for, rolled back, and torn down. It is
// the RESTCONF sibling of TestCLI_NETCONFAndRESTCONFFromDayZeroToDayN, whose
// helpers it shares.
package main_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// restconfTask is a runbook task calling net.restconf.config with params.
func restconfTask(name string, params ...string) string {
	b := "  - name: " + name + "\n    net.restconf.config:\n"
	for _, p := range params {
		b += "      " + p + "\n"
	}
	return b
}

// gateSecret is a random lab secret, never printed.
func gateSecret() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "Gx-" + hex.EncodeToString(b)
}

// loopbackPath is the RESTCONF data path of the native model's interface list.
const loopbackPath = "path: Cisco-IOS-XE-native:native/interface"

// TestCLI_RESTCONFFromDayZeroToDayN is the release gate.
func TestCLI_RESTCONFFromDayZeroToDayN(t *testing.T) {
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
	stdin := func(in string, args ...string) {
		t.Helper()
		if out, err := runPleiadesWithStdin(t, dir, in, args...); err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	addr, title := env["PLEIADES_CML_FREE_ADDRESS"], fmt.Sprintf("pleiades-restconf-gate-%d", time.Now().Unix())
	login := gateSecret()

	// Day 0: the router with RESTCONF and local AAA on from its first boot, a
	// cisco_router record for reading its CLI, and its RESTCONF server as a
	// generic_restconf device at the router's HTTPS origin.
	g.run("init")
	g.run("add-host", "cml", "--type", "cml_controller", "--set", "api_base="+env["PLEIADES_CML_URL"], "--set", "tls_fingerprint="+env["PLEIADES_CML_TLS_FINGERPRINT"])
	stdin(env["PLEIADES_CML_PASSWORD"]+"\n", "add-credential", "cml", "--username", env["PLEIADES_CML_USERNAME"], "--password-stdin")
	g.run("add-host", "R", "--type", "cisco_router", "--set", "host="+addr, "--set", "transport=ssh", "--set", "ephemeral=true")
	stdin(login+"\n"+gateSecret()+"\n", "add-credential", "R", "--username", "netops", "--password-stdin", "--enable-password-stdin")
	g.run("add-host", "RC", "--type", "generic_restconf", "--set", "base_url=https://"+addr, "--set", "http_auth=basic")
	stdin(login+"\n", "add-credential", "RC", "--username", "netops", "--password-stdin")
	_, tasks := g.runbook("build", fmt.Sprintf(`# What this does: Day 0 for a Catalyst 8000V with RESTCONF and local AAA on from its first boot.
# Blast radius: the CML controller cml; one new lab, %[1]s.
# Changes: a lab, two nodes and a link are made, and both nodes started.
# Undo: pleiades rollback <run-id> removes everything it made.
id: restconf-gate-build
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
        license boot level network-advantage addon dna-advantage
        aaa new-model
        aaa authentication login default local
        aaa authorization exec default local
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

	// The router's self-signed certificate is refused at onboarding, naming
	// its fingerprint, which is then pinned. Until RESTCONF serves (it
	// answers 502 for minutes after boot), onboarding proves nothing and is
	// tried again; a generic device runs no method before it is onboarded,
	// so wait.restconf cannot stand in here.
	var pin []string
	g.waitFor("the router's certificate to be offered", 20*time.Minute, func() bool {
		out, _ := g.try("onboard", "RC")
		pin = regexp.MustCompile(`fingerprint (sha256:[0-9a-f]{64})`).FindStringSubmatch(out)
		return pin != nil
	})
	g.run("set-host", "RC", "--set", "tls_fingerprint="+pin[1])
	g.waitFor("RESTCONF to serve an authenticated read", 25*time.Minute, func() bool {
		out, err := g.try("onboard", "RC")
		if strings.Contains(out, "certificate") && strings.Contains(out, "not trusted") {
			t.Fatalf("the pinned certificate was refused:\n%s", out)
		}
		return err == nil && strings.Contains(out, "RestconfCapable")
	})
	ready := stats(g.task("ok", "RC", "wait.restconf", "timeout=300"))
	if ready["root"] != "/restconf" || toInt(ready["tries"]) != 1 {
		t.Errorf("wait.restconf on a serving router: %v", ready)
	}

	// Discovery: the router's whole library, its NMDA datastores and its root.
	facts := stats(g.task("ok", "RC", "net.restconf.facts"))
	if facts["yang.completeness"] != "complete" || toInt(facts["yang.module_count"]) < 500 {
		t.Errorf("yang.completeness = %v, yang.module_count = %v; want the router's whole library", facts["yang.completeness"], facts["yang.module_count"])
	}
	if ds := fmt.Sprint(facts["restconf.datastores"]); !strings.Contains(ds, "running") || !strings.Contains(ds, "operational") {
		t.Errorf("restconf.datastores = %s, want running and operational", ds)
	}
	if facts["restconf.root"] != "/restconf" {
		t.Errorf("restconf.root = %v", facts["restconf.root"])
	}

	// Day 1: a create, then the same create again, which changes nothing.
	create := restconfTask("loopback 300", loopbackPath, "method: post",
		`content: '{"Cisco-IOS-XE-native:Loopback": {"name": 300, "description": "day1"}}'`)
	_, tasks = g.runbook("day1", header("day1", "Day 1 over RESTCONF.", "RC")+create, false)
	if tasks[0]["status"] != "changed" {
		t.Fatalf("day1 create = %v", tasks[0])
	}
	if out := g.show("show running-config interface Loopback300"); !strings.Contains(out, "description day1") {
		t.Fatalf("Loopback300 after Day 1:\n%s", out)
	}
	_, tasks = g.runbook("day1-again", header("day1-again", "Day 1 again: nothing to change.", "RC")+create, false)
	if tasks[0]["status"] != "ok" {
		t.Errorf("a second identical create = %v, want ok (unchanged)", tasks[0])
	}

	// Day 2: a merge and a replace, each run twice, the second unchanged.
	entry := "path: Cisco-IOS-XE-native:native/interface/Loopback=300"
	patch := restconfTask("describe it", entry, "method: patch",
		`content: '{"Cisco-IOS-XE-native:Loopback": {"name": 300, "description": "day2"}}'`)
	replace := restconfTask("replace it", entry, "method: put",
		`content: '{"Cisco-IOS-XE-native:Loopback": {"name": 300, "description": "day2 replaced"}}'`)
	for _, step := range []struct{ id, body, show string }{
		{"day2-patch", patch, "description day2"},
		{"day2-put", replace, "description day2 replaced"},
	} {
		_, tasks = g.runbook(step.id, header(step.id, "Day 2 over RESTCONF.", "RC")+step.body, false)
		if tasks[0]["status"] != "changed" {
			t.Errorf("%s = %v, want changed", step.id, tasks[0])
		}
		_, tasks = g.runbook(step.id+"-again", header(step.id+"-again", "Day 2 again: nothing to change.", "RC")+step.body, false)
		if tasks[0]["status"] != "ok" {
			t.Errorf("%s run again = %v, want ok (unchanged)", step.id, tasks[0])
		}
		if out := g.show("show running-config interface Loopback300"); !strings.Contains(out, step.show) {
			t.Errorf("Loopback300 after %s:\n%s", step.id, out)
		}
	}

	// Refusals: a value the router rejects comes back structured; a module
	// the router does not implement and a patch of an absent entry are
	// refused before anything is sent.
	bad := g.task("failed", "RC", "net.restconf.config", "path=Cisco-IOS-XE-native:native/interface/Loopback=300", "method=patch",
		`content={"Cisco-IOS-XE-native:Loopback": {"name": 300, "ip": {"address": {"primary": {"address": "999.1.1.1", "mask": "255.255.255.255"}}}}}`)
	if !strings.Contains(fmt.Sprint(bad["error"]), "malformed-message") {
		t.Errorf("a bad value: %v", bad["error"])
	}
	unknown := g.task("failed", "RC", "net.restconf.config", "path=no-such-module:thing", "method=patch", `content={"no-such-module:thing": {}}`)
	if e := fmt.Sprint(unknown["error"]); !strings.Contains(e, "does not implement module no-such-module") || !strings.Contains(e, "nothing was sent") {
		t.Errorf("a module the router does not implement: %v, want it named and nothing sent", unknown["error"])
	}
	g.task("failed", "RC", "net.restconf.config", "path=Cisco-IOS-XE-native:native/interface/Loopback=399", "method=patch",
		`content={"Cisco-IOS-XE-native:Loopback": {"name": 399}}`)
	if got := g.loopbacks(); strings.Contains(strings.Join(got, " "), "Loopback399") {
		t.Errorf("a refused patch created Loopback399: %v", got)
	}

	// An NMDA datastore the library lists: running through /ds.
	_, tasks = g.runbook("ds-running", header("ds-running", "A change through the running datastore's /ds resource.", "RC")+
		restconfTask("describe it through /ds", entry, "method: patch", "datastore: running",
			`content: '{"Cisco-IOS-XE-native:Loopback": {"name": 300, "description": "via ds"}}'`), false)
	if tasks[0]["status"] != "changed" || !strings.Contains(g.show("show running-config interface Loopback300"), "description via ds") {
		t.Errorf("a change through /ds/ietf-datastores:running: %v", tasks[0])
	}

	// A named secret over RESTCONF: an IKEv2 keyring's pre-shared key named by
	// a secret reference, resolved by the method and never written out.
	psk := gateSecret()
	stdin(psk+"\n", "add-secret", "gatepsk", "--field", "key", "--value-stdin")
	g.runbook("keyring", header("keyring", "a keyring whose key is a named secret, over RESTCONF.", "RC")+
		restconfTask("the keyring", "path: Cisco-IOS-XE-native:native/crypto/Cisco-IOS-XE-crypto:ikev2", "method: post",
			`content: '{"Cisco-IOS-XE-crypto:keyring": {"name": "KR-RGATE", "peer": [{"name": "ALL", "address": {"ipv4": {"ipv4-address": "0.0.0.0", "ipv4-mask": "0.0.0.0"}}, "pre-shared-key": {"key": "{{ secret.gatepsk.key }}"}}]}}'`), false)
	if out := g.show("show running-config | section crypto ikev2 keyring KR-RGATE"); !strings.Contains(out, "pre-shared-key pleiades-masked-") || strings.Contains(out, psk) {
		t.Errorf("the keyring's key reads back as %q, want it present and masked", out)
	}
	_ = filepath.WalkDir(filepath.Join(dir, ".pleiades"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(path, "credentials") && !strings.Contains(path, "secrets") {
			if b, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(b), psk) {
				t.Errorf("the key is written in %s", path)
			}
		}
		return nil
	})
	ring := "path: Cisco-IOS-XE-native:native/crypto/Cisco-IOS-XE-crypto:ikev2/keyring=KR-RGATE"
	_, tasks = g.runbook("keyring-delete", header("keyring-delete", "remove the keyring.", "RC")+restconfTask("remove it", ring, "method: delete"), false)
	if tasks[0]["status"] != "changed" {
		t.Errorf("deleting the keyring = %v", tasks[0])
	}
	_, tasks = g.runbook("keyring-delete-again", header("keyring-delete-again", "remove it again: nothing to do.", "RC")+restconfTask("remove it", ring, "method: delete"), false)
	if tasks[0]["status"] != "ok" {
		t.Errorf("deleting an absent keyring = %v, want ok (nothing sent)", tasks[0])
	}

	// A restart of RESTCONF, waited for through its 502s.
	g.run("adhoc", "R", "net.ios.config", "lines:=[no restconf]")
	g.run("adhoc", "R", "net.ios.config", "lines:=[restconf]")
	waited := stats(g.task("ok", "RC", "wait.restconf", "timeout=900", "sleep=10"))
	// Measured 2026-11-15: 9 tries, 8 of them failed, while RESTCONF came
	// back. One try would mean the restart was never seen.
	if toInt(waited["tries"]) < 2 || toInt(waited["failures"]) < 1 {
		t.Errorf("wait.restconf after a restart: %v, want the restart's failed tries before one answered", waited)
	}

	// Day N: a create undone by pleiades rollback through its recorded undo.
	undoID, tasks := g.runbook("dayn", header("dayn", "Day N: a create whose undo is recorded.", "RC")+
		restconfTask("loopback 301", loopbackPath, "method: post", `content: '{"Cisco-IOS-XE-native:Loopback": {"name": 301, "description": "dayn"}}'`), false)
	if tasks[0]["status"] != "changed" {
		t.Fatalf("dayn create = %v", tasks[0])
	}
	if !strings.Contains(strings.Join(g.loopbacks(), " "), "Loopback301") {
		t.Fatal("Loopback301 was not created")
	}
	if out, err := g.try("rollback", undoID); err != nil {
		t.Fatalf("rollback %s: %v\n%s", undoID, err, out)
	}
	if got := strings.Join(g.loopbacks(), " "); strings.Contains(got, "Loopback301") || !strings.Contains(got, "Loopback300") {
		t.Errorf("after the rollback the loopbacks are %s, want Loopback300 and no Loopback301", got)
	}
}

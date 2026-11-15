// Release gate for generic_restconf and net.restconf.facts: through the real
// binary, a device is added, onboarded against a RESTCONF server over TLS
// that answers in RFC 8040's shapes with a real Catalyst 8000V's YANG
// library, read by a runbook, and its discovery recorded in the project's
// fact store.
package main_test

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
)

// restconfGateRunbook reads one device's RESTCONF discovery.
const restconfGateRunbook = `# Reads what rc1's RESTCONF server says about it: its root, capabilities,
# datastores and YANG modules, recorded as facts.
# Blast radius: rc1 alone. Changes: nothing on the device; the project's fact
# store gains the read. Undo: none needed.
id: restconf-facts
hosts: rc1
tasks:
  - name: Discover the device's YANG modules over RESTCONF
    net.restconf.facts:
    register: discovered
`

func TestGenericReleaseGate_RESTCONF(t *testing.T) {
	s := restconftest.New(t, restconftest.Options{Username: "admin", Password: "gate-Restconf-7"})
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "rc1", "--type", "generic_restconf", "--set", "base_url=" + s.URL, "--set", "tls_fingerprint=" + devicetls.Fingerprint(s.Certificate())},
		{"add-credential", "rc1", "--username", "admin", "--password", "gate-Restconf-7"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	res := onboardJSON(t, dir, nil, "rc1")
	if !slices.Equal(res.Capabilities, []string{"RestconfCapable"}) || res.Facts["restconf_root"] != "/restconf" || res.Facts["yang_module_count"] != "556" {
		t.Fatalf("probed %+v", res)
	}

	writeFile(t, dir, "runbooks/restconf.yaml", restconfGateRunbook)
	out, err := runPleiadesWithEnv(t, dir, nil, "run", "runbooks/restconf.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(out, "gate-Restconf-7") {
		t.Error("the run printed the device's credential")
	}
	if s.Unauthenticated() != 0 {
		t.Errorf("%d requests reached the device with no credential", s.Unauthenticated())
	}

	// A second device holding a wrong password: the server's refusal echoes
	// the Authorization header and the password, and neither may reach what
	// onboarding prints.
	for _, args := range [][]string{
		{"add-host", "rc2", "--type", "generic_restconf", "--set", "base_url=" + s.URL, "--set", "tls_fingerprint=" + devicetls.Fingerprint(s.Certificate())},
		{"add-credential", "rc2", "--username", "admin", "--password", "wrong-Restconf-8"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	refused, err := runPleiadesWithEnv(t, dir, nil, "onboard", "rc2", "--json", "--timeout", "90s")
	token := base64.StdEncoding.EncodeToString([]byte("admin:wrong-Restconf-8"))
	if err == nil || !strings.Contains(refused, "refused the credential") || !strings.Contains(refused, "request carried") {
		t.Fatalf("onboarding with a wrong password: %v\n%s", err, refused)
	}
	if strings.Contains(refused, "wrong-Restconf-8") || strings.Contains(refused, token) {
		t.Errorf("onboarding printed the credential:\n%s", refused)
	}

	shown, err := runPleiadesWithEnv(t, dir, nil, "facts", "rc1", "--json")
	if err != nil {
		t.Fatalf("facts: %v\n%s", err, shown)
	}
	var stored struct {
		Facts []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(shown), &stored); err != nil {
		t.Fatalf("facts --json printed %q: %v", shown, err)
	}
	got := map[string]any{}
	for _, f := range stored.Facts {
		got[f.Key] = f.Value
	}
	if got["restconf.root"] != "/restconf" || got["yang.completeness"] != "complete" || got["yang.module_count"] != float64(556) {
		t.Errorf("stored facts %v, want the root, a complete library and its 556 data models", got)
	}
	modules, _ := got["yang.modules"].([]any)
	if !slices.Contains(modules, any("Cisco-IOS-XE-native@2024-07-01")) {
		t.Errorf("yang.modules holds %d entries without Cisco-IOS-XE-native, want the router's own", len(modules))
	}
}

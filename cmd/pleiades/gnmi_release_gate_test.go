// Release gate for gNMI on the Crawl tier, from day 0 to day N, through the
// real binary against a real Nokia SR Linux: a generic_gnmi device is added
// and its self-signed certificate refused until pinned from the refusal that
// names it, it is onboarded from its Capabilities, a runbook waits for its
// gNMI server, records what it says and creates a loopback, a second run
// changes nothing, the facts are in the project's store, and pleiades
// rollback deletes the loopback from the undo the first run recorded. Each
// change is read back over SR Linux's own command line (sr_cli), never
// through the gNMI session that made it.
package main_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// gnmiGateRunbook is the day-one runbook.
const gnmiGateRunbook = `# Configures srl1's loopback lo21 over gNMI: waits for its gNMI server,
# records what it says it supports, and creates the loopback.
# Blast radius: srl1, one loopback interface. Changes: interface lo21 is
# created. Undo: pleiades rollback deletes it, from the undo the run records.
id: gnmi-day-one
hosts: srl1
tasks:
  - name: Wait for the gNMI server to answer
    wait.gnmi:
      timeout: 300
      sleep: 2
  - name: Record what the server supports
    net.gnmi.facts:
  - name: Create the gate loopback
    net.gnmi.config:
      update:
        - path: /interface[name=lo21]
          val:
            admin-state: enable
            description: pleiades gnmi gate
`

// fingerprintInRefusal finds the certificate a refusal names.
var fingerprintInRefusal = regexp.MustCompile(`fingerprint (sha256:[0-9a-f]{64})`)

// srlLoopback reads lo21 from SR Linux's own command line.
func srlLoopback(t *testing.T, srl *testsupport.SRLinux) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := srl.CLI(ctx, "info from running /interface lo21", false)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// onboardUntil runs `pleiades onboard name --json` until its output holds
// want or the deadline passes, returning the last output: a booting device
// refuses connections first.
func onboardUntil(t *testing.T, dir, name, want string) (string, error) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		out, err := runPleiadesWithEnv(t, dir, nil, "onboard", name, "--json", "--timeout", "30s")
		if strings.Contains(out, want) || time.Now().After(deadline) {
			return out, err
		}
		time.Sleep(3 * time.Second)
	}
}

func TestCLI_GNMIFromDayZeroToDayN(t *testing.T) {
	srl := testsupport.StartSRLinux(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "srl1", "--type", "generic_gnmi", "--set", "target=" + srl.Target},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// The password on standard input, never argv.
	if out, err := runPleiadesWithStdin(t, dir, testsupport.SRLinuxPassword+"\n", "add-credential", "srl1", "--username", testsupport.SRLinuxUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}

	// Day 0: SR Linux's certificate is its own, self-signed, so the system's
	// roots refuse it, and the refusal names what to pin.
	refused, err := onboardUntil(t, dir, "srl1", "fingerprint sha256:")
	match := fingerprintInRefusal.FindStringSubmatch(refused)
	if err == nil || match == nil || !strings.Contains(refused, "not trusted") {
		t.Fatalf("onboarding an unpinned SR Linux: %v\n%s", err, refused)
	}
	if out, err := runPleiadesWithEnv(t, dir, nil, "set-host", "srl1", "--set", "tls_fingerprint="+match[1]); err != nil {
		t.Fatalf("set-host: %v\n%s", err, out)
	}
	onboarded, err := onboardUntil(t, dir, "srl1", `"active"`)
	var res genericOnboarded
	if err != nil || json.Unmarshal([]byte(onboarded), &res) != nil || res.State != "active" || len(res.Capabilities) != 1 || res.Capabilities[0] != "GNMICapable" {
		t.Fatalf("onboarding the pinned SR Linux: %v\n%s", err, onboarded)
	}
	if res.Facts["gnmi_version"] == "" || res.Facts["gnmi_model_count"] == "0" {
		t.Errorf("onboarded facts %v", res.Facts)
	}

	// Day 1: the runbook creates the loopback, read back over sr_cli.
	writeFile(t, dir, "runbooks/gnmi.yaml", gnmiGateRunbook)
	first := restconfReport(t, dir, "run", "runbooks/gnmi.yaml")
	if first.Outcome.ExitCode != 0 || first.RunID == "" {
		t.Fatalf("the day-one run: %+v", first)
	}
	if out := srlLoopback(t, srl); !strings.Contains(out, "pleiades gnmi gate") || !strings.Contains(out, "enable") {
		t.Fatalf("sr_cli after the run:\n%s", out)
	}

	// Day N: the same runbook changes nothing.
	again, err := runPleiadesWithEnv(t, dir, nil, "run", "runbooks/gnmi.yaml")
	if err != nil || strings.Contains(again, testsupport.SRLinuxPassword) {
		t.Fatalf("the second run: %v\n%s", err, again)
	}
	shown, err := runPleiadesWithEnv(t, dir, nil, "facts", "srl1", "--json")
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
	if got["gnmi.version"] == nil || got["yang.completeness"] != "partial" || got["gnmi.models_digest"] != res.Facts["gnmi_models_digest"] {
		t.Errorf("stored facts: version %v, completeness %v, digest %v (onboarding's %v)", got["gnmi.version"], got["yang.completeness"], got["gnmi.models_digest"], res.Facts["gnmi_models_digest"])
	}

	// The undo the first run recorded deletes the loopback.
	rollback := restconfReport(t, dir, "rollback", first.RunID)
	if rollback.Outcome.ExitCode != 0 || len(rollback.methods()) != 1 || !strings.HasPrefix(rollback.methods()[0], "net.gnmi.config ") {
		var problems any
		if rollback.Rollback != nil {
			problems = rollback.Rollback.Problems
		}
		t.Fatalf("the rollback %+v (problems %+v), want one net.gnmi.config step", rollback, problems)
	}
	if out := srlLoopback(t, srl); strings.Contains(out, "pleiades gnmi gate") {
		t.Errorf("sr_cli still shows the loopback after the rollback:\n%s", out)
	}

	// A device holding a wrong password is refused once, and nothing printed
	// holds the password.
	for _, args := range [][]string{
		{"add-host", "srl2", "--type", "generic_gnmi", "--set", "target=" + srl.Target, "--set", "tls_fingerprint=" + match[1]},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, "wrong-Gnmi-Gate-5\n", "add-credential", "srl2", "--username", testsupport.SRLinuxUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential srl2: %v\n%s", err, out)
	}
	wrong, err := runPleiadesWithEnv(t, dir, nil, "onboard", "srl2", "--json", "--timeout", "60s")
	if err == nil || !strings.Contains(wrong, "refused the login") || strings.Contains(wrong, "wrong-Gnmi-Gate-5") {
		t.Errorf("onboarding with a wrong password: %v\n%s", err, wrong)
	}
}

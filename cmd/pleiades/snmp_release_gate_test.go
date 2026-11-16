// Release gate for SNMP on the Crawl tier, from day 0 to day N, through the
// real binary against a real net-snmp agent (testsupport.StartSNMPD): a
// generic_snmp device is added and onboarded over SNMPv3 authPriv, a runbook
// waits for its agent, records its facts, reads and writes sysLocation.0, a
// second run changes nothing, and pleiades rollback writes the value read
// before back. A second device asking for SNMPv2c is refused until its record
// opts in, then onboards and runs with a warning on every step, and its
// community never reaches the output.
package main_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// snmpGateRunbook waits for agent1's agent, records its facts, reads its
// name and writes its location.
const snmpGateRunbook = `# Waits for agent1's SNMP agent, records what it says about itself, and
# names where it is.
# Blast radius: agent1 alone. Changes: sysLocation.0, and the project's fact
# store gains the read. Undo: pleiades rollback writes the location read
# before back, from the undo the run records.
id: snmp-day-n
hosts: agent1
tasks:
  - name: Wait for the agent to answer its SNMPv3 user
    wait.snmp:
      timeout: 120
      sleep: 2
  - name: Record the agent's system group, engine and interfaces
    net.snmp.facts:
  - name: Read the agent's name
    net.snmp.get:
      oids:
        - sysName.0
    register: named
  - name: Say where the device is
    net.snmp.set:
      oid: sysLocation.0
      type: octet-string
      value: rack 7, row C, set by the release gate
`

// snmpGateFacts reads one device's community-read facts.
const snmpGateFacts = `# Records what agent2's SNMP agent says about itself over SNMPv2c.
# Blast radius: agent2 alone. Changes: nothing on the device. Undo: none needed.
id: snmp-v2c-facts
hosts: agent2
tasks:
  - name: Record the agent's system group over SNMPv2c
    net.snmp.facts:
`

// sysLocation reads the agent's sysLocation.0 over SNMPv3 as user, first
// writing set to it when set is not empty.
func sysLocation(t *testing.T, agent *testsupport.SNMPD, user wire.SNMPCredential, set string) string {
	t.Helper()
	s := snmp.Defaults()
	s.Port = agent.Port
	c, err := snmp.Dial(agent.Host, s, user)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if set != "" {
		v, err := snmp.Value("sysLocation.0", snmp.TypeOctetString, set)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Set(ctx, []snmp.Varbind{v}); err != nil {
			t.Fatalf("writing sysLocation.0: %v", err)
		}
	}
	got, err := c.Get(ctx, []string{snmp.OIDSysLocation})
	if err != nil || len(got) != 1 {
		t.Fatalf("reading sysLocation.0: %v", err)
	}
	return got[0].Text
}

// heldLocation is the location the agent holds before the run: every
// template marker the engine renders, as data a device can hold, which the
// rollback must write back exactly rather than render.
const heldLocation = "noc {{ vars.site }} {% if true %}x{% endif %} {# a comment #}"

// rawReport runs the binary with --json in dir and returns what it printed
// and its report.
func rawReport(t *testing.T, dir string, args ...string) (string, gateReport) {
	t.Helper()
	cmd := exec.Command(binPath, append(args, "--json")...) // #nosec G204 -- the test's own binary
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	var rep gateReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("pleiades %v --json printed no report: %v\nstdout: %s\nstderr: %s", args, err, out, stderr.String())
	}
	return string(out) + stderr.String(), rep
}

// filesHolding names every file under dir holding any of secrets in the
// clear: the run's journal, its facts, its logs and everything else the
// project keeps on disk.
func filesHolding(t *testing.T, dir string, secrets []string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a file of the test's own project
		if err != nil {
			return err
		}
		for _, s := range secrets {
			if strings.Contains(string(data), s) {
				found = append(found, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestCLI_SNMPFromDayZeroToDayN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the SNMP release gate, which runs a real net-snmp agent, in short mode")
	}
	agent := testsupport.StartSNMPD(t)
	user := agent.Users[testsupport.SNMPDUserName(wire.SNMPAuthSHA512, wire.SNMPPrivAES256)].Cred
	secrets := []string{user.AuthPassphrase, user.PrivPassphrase, agent.ReadCommunity, agent.WriteCommunity}
	leaks := func(t *testing.T, what, out string) {
		t.Helper()
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("%s printed a secret:\n%s", what, out)
			}
		}
	}
	dir := t.TempDir()
	port := strconv.Itoa(agent.Port)
	for _, args := range [][]string{
		{"init"},
		{"add-host", "agent1", "--type", "generic_snmp", "--set", "host=" + agent.Host, "--set", "snmp_port=" + port},
	} {
		if out, err := runPleiadesWithEnv(t, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// The passphrases on standard input, never argv: authentication first.
	if out, err := runPleiadesWithStdin(t, dir, user.AuthPassphrase+"\n"+user.PrivPassphrase+"\n", "add-credential", "agent1",
		"--snmp-user", user.User, "--snmp-auth", "sha512", "--snmp-priv", "aes256", "--snmp-passphrase-stdin"); err != nil {
		t.Fatalf("add-credential agent1: %v\n%s", err, out)
	}
	res := onboardJSON(t, dir, nil, "agent1")
	if len(res.Capabilities) != 1 || res.Capabilities[0] != "SNMPCapable" || res.Facts["snmp_security_level"] != "authPriv" || res.Facts["snmp_engine_id"] == nil {
		t.Fatalf("onboarded %+v", res)
	}

	original := sysLocation(t, agent, user, heldLocation)
	if original != heldLocation {
		t.Fatalf("the agent holds %q, want %q", original, heldLocation)
	}
	writeFile(t, dir, "runbooks/snmp.yaml", snmpGateRunbook)
	printed, run := rawReport(t, dir, "run", "runbooks/snmp.yaml")
	if run.Outcome.ExitCode != 0 || run.RunID == "" {
		t.Fatalf("the run %+v", run)
	}
	leaks(t, "the first run's report", printed)
	if got := sysLocation(t, agent, user, ""); got != "rack 7, row C, set by the release gate" {
		t.Fatalf("sysLocation.0 after the run = %q", got)
	}
	out, err := runPleiadesWithEnv(t, dir, nil, "run", "runbooks/snmp.yaml")
	if err != nil {
		t.Fatalf("a second run: %v\n%s", err, out)
	}
	leaks(t, "the second run", out)

	shown, err := runPleiadesWithEnv(t, dir, nil, "facts", "agent1", "--json")
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
	if got["snmp.security_level"] != "authPriv" || !strings.HasPrefix(got["snmp.sys_object_id"].(string), "1.3.6.1.4.1.8072") || got["snmp.engine_id"] != res.Facts["snmp_engine_id"] {
		t.Errorf("stored facts %v", got)
	}

	rollback := restconfReport(t, dir, "rollback", run.RunID)
	if rollback.Outcome.ExitCode != 0 || len(rollback.methods()) != 1 || !strings.HasPrefix(rollback.methods()[0], "net.snmp.set ") {
		t.Fatalf("the rollback %+v, want one net.snmp.set step", rollback)
	}
	if got := sysLocation(t, agent, user, ""); got != original {
		t.Errorf("sysLocation.0 after the rollback = %q, want %q written back exactly", got, original)
	}

	// SNMPv2c is refused until the record opts in, then works and warns.
	refused, err := runPleiadesWithEnv(t, dir, nil, "add-host", "agent2", "--type", "generic_snmp", "--set", "host="+agent.Host, "--set", "snmp_port="+port, "--set", "snmp_version=2c")
	if err == nil || !strings.Contains(refused, "snmp_allow_cleartext_community") {
		t.Fatalf("an SNMPv2c device without its opt-in was added: %v\n%s", err, refused)
	}
	if out, err := runPleiadesWithEnv(t, dir, nil, "add-host", "agent2", "--type", "generic_snmp", "--set", "host="+agent.Host, "--set", "snmp_port="+port,
		"--set", "snmp_version=2c", "--set", "snmp_allow_cleartext_community=true"); err != nil {
		t.Fatalf("add-host agent2: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, agent.ReadCommunity+"\n", "add-credential", "agent2", "--snmp-community-stdin"); err != nil {
		t.Fatalf("add-credential agent2: %v\n%s", err, out)
	}
	onboarded, err := runPleiadesWithEnv(t, dir, nil, "onboard", "agent2", "--timeout", "90s")
	if err != nil || !strings.Contains(onboarded, "in the clear") {
		t.Fatalf("onboarding agent2: %v\n%s", err, onboarded)
	}
	leaks(t, "onboarding agent2", onboarded)
	writeFile(t, dir, "runbooks/v2c.yaml", snmpGateFacts)
	out, err = runPleiadesWithEnv(t, dir, nil, "run", "runbooks/v2c.yaml")
	if err != nil || !strings.Contains(out, "in the clear") {
		t.Fatalf("the SNMPv2c run: %v\n%s", err, out)
	}
	leaks(t, "the SNMPv2c run", out)
	if held := filesHolding(t, dir, secrets); len(held) != 0 {
		t.Errorf("the project keeps a secret in the clear in %v", held)
	}
}

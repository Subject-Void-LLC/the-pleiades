// Tests for onboard running each loaded program's probe.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// probingProgram defines clitest.widget_api with the read-only probe
// clitest.widget.probe, which answers with facts.
func probingProgram(facts string) string {
	return probingProgramInvoking("  cat >/dev/null\n  printf '%s' '" + facts + "' >&3\n")
}

// probingProgramInvoking is probingProgram whose method calls run invoke,
// a shell fragment: the request arrives on standard input and the response
// goes to file descriptor 3.
func probingProgramInvoking(invoke string) string {
	return `#!/bin/sh
case "$1" in
describe)
  printf '%s' '{"protocol":1,
    "methods":[
      {"name":"clitest.widget.read","manifest":{"status":"implemented",
        "requiredCapabilities":["clitest.widget_api"],"reversibility":{"reversible":false,"notes":"a fixture"}}},
      {"name":"clitest.widget.probe","manifest":{"status":"implemented",
        "reversibility":{"readOnly":true,"notes":"asks the widget controller who it is"}}}],
    "capabilities":[{"name":"clitest.widget_api","probe":"clitest.widget.probe",
      "properties":[{"name":"endpoint","type":"string","required":true}]}]}'
  ;;
invoke)
` + invoke + `  ;;
esac
`
}

// probeProject is a project holding the cisco_router r1 (a type with no
// probe of its own), with a program whose probe answers facts loaded and
// approved.
func probeProject(t *testing.T, facts string) string {
	t.Helper()
	return probeProjectWith(t, probingProgram(facts))
}

// probeProjectWith is probeProject with program as the approved program.
func probeProjectWith(t *testing.T, program string) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	resetLoadedCollections()
	t.Cleanup(resetLoadedCollections)
	programs := t.TempDir()
	if err := os.Chmod(programs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(programs, "widgets"), []byte(program), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	t.Setenv(collectionsDirEnv, programs)
	var out string
	if err := captureRun(t, &out, func() error { return runCollection([]string{"approve", "widgets", "--yes"}) }); err != nil {
		t.Fatalf("approving the program: %v\n%s", err, out)
	}
	dir := t.TempDir()
	if err := captureRun(t, &out, func() error { return runInit([]string{"--dir", dir}) }); err != nil {
		t.Fatal(err)
	}
	if err := captureRun(t, &out, func() error {
		return runAddHost([]string{"r1", "--dir", dir, "--type", "cisco_router", "--set", "host=127.0.0.1"})
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// grantsOf reads the device r1's grants back from dir.
func grantsOf(t *testing.T, dir string) []inventory.Grant {
	t.Helper()
	item, err := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory()).GetByName(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	grants, _, err := inventory.GrantsFrom(item.Properties())
	if err != nil {
		t.Fatal(err)
	}
	return grants
}

// TestOnboard_AProgramsProbeGrantsItsCapability: the probe answers, so the
// device holds the capability as a probed grant naming the program, and
// --json reports it.
func TestOnboard_AProgramsProbeGrantsItsCapability(t *testing.T) {
	dir := probeProject(t, `{"facts":{"granted":true,"properties":{"endpoint":"https://w"}}}`)
	var out string
	err := captureRun(t, &out, func() error { return runOnboard([]string{"r1", "--dir", dir, "--json"}) })
	if err != nil && strings.Contains(err.Error(), "Landlock") {
		t.Skipf("needs Landlock to load a program: %v", err)
	}
	if err != nil {
		t.Fatalf("onboard = %v\n%s", err, out)
	}
	var res struct {
		Grants []struct {
			Capability string `json:"capability"`
			Granted    bool   `json:"granted"`
			Error      string `json:"error"`
		} `json:"grants"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Grants) != 1 || !res.Grants[0].Granted {
		t.Fatalf("onboard --json = %s (%v)", out, err)
	}
	grants := grantsOf(t, dir)
	if len(grants) != 1 || grants[0].Source != inventory.GrantProbed || grants[0].Program != "widgets" || grants[0].Properties["endpoint"] != "https://w" {
		t.Fatalf("grants = %+v, want the probe's grant", grants)
	}
	// Bound to the definition the program holds, and naming the build that
	// probed: the program file's own SHA-256, as the loader checked it.
	def, ok := capability.DataOf("clitest.widget_api")
	if !ok || grants[0].Definition != capability.DefinitionDigest(def) {
		t.Errorf("the grant's definition digest is %q, want the loaded definition's (%v)", grants[0].Definition, ok)
	}
	program, err := os.ReadFile(filepath.Join(os.Getenv(collectionsDirEnv), "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(program); grants[0].Build != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("the grant names the build %q, want the program's digest", grants[0].Build)
	}
}

// TestOnboard_AProbeThatHangsIsStoppedAtTheTimeout: a probe that never
// answers is stopped once onboard's --timeout passes, reported as not
// granted, and grants nothing. Before the probe ran under that timeout, it
// ran under the loader's 30 minute call limit alone.
func TestOnboard_AProbeThatHangsIsStoppedAtTheTimeout(t *testing.T) {
	dir := probeProjectWith(t, probingProgramInvoking("  cat >/dev/null\n  exec sleep 120\n"))
	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	start := time.Now()
	go func() {
		var out string
		err := captureRun(t, &out, func() error { return runOnboard([]string{"r1", "--dir", dir, "--timeout", "2s"}) })
		done <- outcome{out, err}
	}()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("onboard was still running 45 seconds after a 2 second --timeout")
	}
	if got.err != nil && strings.Contains(got.err.Error(), "Landlock") {
		t.Skipf("needs Landlock to load a program: %v", got.err)
	}
	if got.err != nil {
		t.Fatalf("onboard = %v\n%s", got.err, got.out)
	}
	if !strings.Contains(got.out, "not granted") || !strings.Contains(got.out, "did not finish within 2s") {
		t.Errorf("onboard printed %q, want the probe stopped at the timeout", got.out)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("onboard took %s with a 2 second --timeout", elapsed)
	}
	if grants := grantsOf(t, dir); len(grants) != 0 {
		t.Errorf("grants = %+v, want none", grants)
	}
}

// probeSecret is the device credential's password the secret-printing
// probe below prints. It is masked by value, so it never needs to look like
// a secret.
const probeSecret = "lanyardorchid"

// The test above was proved able to fail by registering another password
// as the credential: the probe's value then reached the inventory's state
// file and was reported. The "on a failing probe" case cannot be shown the
// same way, since a failed run's error never quotes the program's stderr.

// TestOnboard_AProbeThatPrintsASecretKeepsNone: a probe that puts the
// device's password in a declared property, and one that fails printing it
// to its standard error, leave it neither in what onboard prints nor in
// the inventory; what is kept is the masked value.
func TestOnboard_AProbeThatPrintsASecretKeepsNone(t *testing.T) {
	for name, invoke := range map[string]string{
		"in a granted property": "  cat >/dev/null\n  printf '%s' '{\"facts\":{\"granted\":true,\"properties\":{\"endpoint\":\"" + probeSecret + "\"},\"note\":\"" + probeSecret + "\"}}' >&3\n",
		"on a failing probe":    "  cat >/dev/null\n  printf 'the password is %s\\n' '" + probeSecret + "' >&2\n  exit 3\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := probeProjectWith(t, probingProgramInvoking(invoke))
			var out string
			if err := captureRun(t, &out, func() error {
				return runAddCredential([]string{"r1", "--dir", dir, "--username", "ops", "--password", probeSecret})
			}); err != nil {
				t.Fatalf("add-credential = %v\n%s", err, out)
			}
			for _, args := range [][]string{{"r1", "--dir", dir}, {"r1", "--dir", dir, "--json"}} {
				err := captureRun(t, &out, func() error { return runOnboard(args) })
				if err != nil && strings.Contains(err.Error(), "Landlock") {
					t.Skipf("needs Landlock to load a program: %v", err)
				}
				if err != nil {
					t.Fatalf("onboard = %v\n%s", err, out)
				}
				if strings.Contains(out, probeSecret) || (err != nil && strings.Contains(err.Error(), probeSecret)) {
					t.Errorf("onboard %v printed the device's password:\n%s", args, out)
				}
			}
			// No file of the project holds it in the clear: not the inventory,
			// its state file (where grants live), nor the run journal. The
			// credential store holds it sealed.
			if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := os.ReadFile(path) // #nosec G304 -- a file of this test's own project directory
				if err != nil {
					return err
				}
				if strings.Contains(string(data), probeSecret) {
					t.Errorf("%s holds the device's password", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// Not vacuous: the probe that granted did grant, keeping the
			// masked value in place of the password, and the one that failed
			// was reported as not granted.
			grants := grantsOf(t, dir)
			if strings.HasPrefix(name, "in a granted") {
				if len(grants) != 1 || grants[0].Properties["endpoint"] == "" {
					t.Fatalf("grants = %+v, want the probe's grant with its masked endpoint", grants)
				}
				return
			}
			if len(grants) != 0 || !strings.Contains(out, `"granted": false`) {
				t.Errorf("a failing probe: grants %+v, printed %q", grants, out)
			}
		})
	}
}

// TestOnboard_AProbeThatDoesNotGrantOrLiesGrantsNothing: a probe answering
// no grants nothing, and one granting properties its own definition
// refuses is reported and writes nothing.
func TestOnboard_AProbeThatDoesNotGrantOrLiesGrantsNothing(t *testing.T) {
	for name, facts := range map[string]string{
		"not granted":        `{"facts":{"granted":false}}`,
		"refused properties": `{"facts":{"granted":true,"properties":{"zone":"x"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := probeProject(t, facts)
			var out string
			err := captureRun(t, &out, func() error { return runOnboard([]string{"r1", "--dir", dir}) })
			if err != nil && strings.Contains(err.Error(), "Landlock") {
				t.Skipf("needs Landlock to load a program: %v", err)
			}
			if err != nil {
				t.Fatalf("onboard = %v\n%s", err, out)
			}
			if !strings.Contains(out, "not granted") {
				t.Errorf("onboard printed %q, want it to say the capability was not granted", out)
			}
			if grants := grantsOf(t, dir); len(grants) != 0 {
				t.Errorf("grants = %+v, want none", grants)
			}
		})
	}
}

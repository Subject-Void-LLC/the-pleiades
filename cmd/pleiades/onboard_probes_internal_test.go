// Tests for onboard running each loaded program's probe.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// probingProgram defines clitest.widget_api with the read-only probe
// clitest.widget.probe, which answers with facts.
func probingProgram(facts string) string {
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
  cat >/dev/null
  printf '%s' '` + facts + `' >&3
  ;;
esac
`
}

// probeProject is a project holding the cisco_router r1 (a type with no
// probe of its own), with a program whose probe answers facts loaded and
// approved.
func probeProject(t *testing.T, facts string) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	resetLoadedCollections()
	t.Cleanup(resetLoadedCollections)
	programs := t.TempDir()
	if err := os.Chmod(programs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(programs, "widgets"), []byte(probingProgram(facts)), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
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

// Tests for grant-capability and revoke-capability, against a real program
// that defines the capability, loaded as `pleiades run` loads it.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// capabilityProgram defines clitest.widget_api, which requires SSH and
// carries a required endpoint, and one method requiring it.
const capabilityProgram = `#!/bin/sh
if [ "$1" = describe ]; then
  printf '%s' '{"protocol":1,
    "methods":[{"name":"clitest.widget.read","manifest":{"status":"implemented",
      "requiredCapabilities":["clitest.widget_api"],"reversibility":{"reversible":false,"notes":"a fixture"}}}],
    "capabilities":[{"name":"clitest.widget_api","requires":["SSHTransportCapable"],
      "properties":[{"name":"endpoint","type":"string","required":true}]}]}'
fi
`

// grantProject is a project holding the linux_server w1, with the
// capability program loaded and approved, as an operator would set it up.
func grantProject(t *testing.T) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	programs := t.TempDir()
	if err := os.Chmod(programs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(programs, "widgets"), []byte(capabilityProgram), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
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
		return runAddHost([]string{"w1", "--dir", dir, "--type", "linux_server", "--set", "host=10.0.0.1"})
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGrantCapability_AnAssertionIsRecordedWithItsReason: a grant of a
// capability the loaded program defines is saved as an assertion, with who
// made it and why, and the device holds the capability; revoking it takes
// it back, and revoking it again fails.
func TestGrantCapability_AnAssertionIsRecordedWithItsReason(t *testing.T) {
	dir := grantProject(t)
	var out string
	err := captureRun(t, &out, func() error {
		return runGrantCapability([]string{"w1", "clitest.widget_api", "--dir", dir, "--set", "endpoint=https://w", "--reason", "the lab widget controller"})
	})
	if err != nil && strings.Contains(err.Error(), "Landlock") {
		t.Skipf("needs Landlock to load a program: %v", err)
	}
	if err != nil || !strings.Contains(out, "asserted by") {
		t.Fatalf("grant-capability = %v:\n%s", err, out)
	}
	item, err := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory()).GetByName(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	grants, _, err := inventory.GrantsFrom(item.Properties())
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants = %+v, %v", grants, err)
	}
	g := grants[0]
	if g.Source != inventory.GrantAsserted || g.Reason != "the lab widget controller" || g.By == "" || g.Properties["endpoint"] != "https://w" {
		t.Errorf("grant = %+v, want an assertion with its reason, its author and its endpoint", g)
	}
	if !item.HasCapability("clitest.widget_api") {
		t.Error("the device does not hold the capability it was granted")
	}

	if err := captureRun(t, &out, func() error {
		return runRevokeCapability([]string{"w1", "clitest.widget_api", "--dir", dir})
	}); err != nil {
		t.Fatalf("revoke-capability = %v", err)
	}
	if err := captureRun(t, &out, func() error {
		return runRevokeCapability([]string{"w1", "clitest.widget_api", "--dir", dir})
	}); err == nil || !strings.Contains(err.Error(), "has no grant") {
		t.Errorf("revoking twice = %v, want a refusal", err)
	}
}

// TestGrantCapability_RefusesWhatItCannotRecordHonestly: no reason, a
// compiled capability, one no loaded program defines, and properties that
// do not fit the definition are each refused, and nothing is written.
func TestGrantCapability_RefusesWhatItCannotRecordHonestly(t *testing.T) {
	dir := grantProject(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no reason", []string{"w1", "clitest.widget_api", "--set", "endpoint=https://w"}, "--reason is required"},
		{"a compiled capability", []string{"w1", string(capability.NameSSHTransport), "--reason", "x"}, "only onboarding grants"},
		{"an undefined capability", []string{"w1", "clitest.nothing", "--reason", "x"}, "no loaded program defines"},
		{"a missing property", []string{"w1", "clitest.widget_api", "--reason", "x"}, "needs the property"},
		{"an undeclared property", []string{"w1", "clitest.widget_api", "--set", "endpoint=a", "--set", "zone=b", "--reason", "x"}, "declares no property"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each run loads the program afresh, as each command does in a
			// process of its own.
			t.Cleanup(collection.SnapshotForTest())
			t.Cleanup(capability.SnapshotForTest())
			var out string
			err := captureRun(t, &out, func() error { return runGrantCapability(append(tc.args, "--dir", dir)) })
			if err != nil && strings.Contains(err.Error(), "Landlock") {
				t.Skipf("needs Landlock to load a program: %v", err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("grant-capability = %v, want a refusal saying %q", err, tc.want)
			}
		})
	}
	item, err := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory()).GetByName(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := item.Properties().Raw()[inventory.GrantedProperty]; present {
		t.Error("a refused grant was written")
	}
}

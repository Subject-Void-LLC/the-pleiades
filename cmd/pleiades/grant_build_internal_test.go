// Tests for revoke-capability --build: every grant one program build
// probed, taken back by the build's digest, and nothing else.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// programDigest is the digest the loader records for the widgets program
// as it is on disk now: its SHA-256.
func programDigest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv(collectionsDirEnv), "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// onboardOnce onboards device in a process of its own, as far as the
// registries go.
func onboardOnce(t *testing.T, dir, device string) {
	t.Helper()
	restoreCaps, restoreMethods := capability.SnapshotForTest(), collection.SnapshotForTest()
	defer func() {
		restoreCaps()
		restoreMethods()
		resetLoadedCollections()
	}()
	var out string
	err := captureRun(t, &out, func() error { return runOnboard([]string{device, "--dir", dir}) })
	if err != nil && strings.Contains(err.Error(), "Landlock") {
		t.Skipf("needs Landlock to load a program: %v", err)
	}
	if err != nil || !strings.Contains(out, "granted by its probe") {
		t.Fatalf("onboard %s = %v\n%s", device, err, out)
	}
}

// grantsOn reads device's grants back from dir.
func grantsOn(t *testing.T, dir, device string) []inventory.Grant {
	t.Helper()
	items, err := loadInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name() == device {
			grants, _, err := inventory.GrantsFrom(item.Properties())
			if err != nil {
				t.Fatal(err)
			}
			return grants
		}
	}
	t.Fatalf("no device %s", device)
	return nil
}

// TestRevokeCapability_ByBuildKeepsEveryOtherGrant: r1 probed by one
// build, r2 by a later one, and r3 asserted by hand; revoking by the first
// build's digest takes back r1's grant alone, and asking again, or with a
// digest nothing recorded, fails rather than reporting nothing done.
func TestRevokeCapability_ByBuildKeepsEveryOtherGrant(t *testing.T) {
	granting := `{"facts":{"granted":true,"properties":{"endpoint":"https://w"}}}`
	dir := probeProject(t, granting)
	var out string
	for _, name := range []string{"r2", "r3"} {
		if err := captureRun(t, &out, func() error {
			return runAddHost([]string{name, "--dir", dir, "--type", "cisco_router", "--set", "host=127.0.0.1"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	first := programDigest(t)
	onboardOnce(t, dir, "r1")

	// A later build of the same program: a new digest, the same definition.
	replaceProgram(t, probingProgram(granting)+"# the next build\n")
	second := programDigest(t)
	if second == first {
		t.Fatal("the two builds have one digest, so this test proves nothing")
	}
	onboardOnce(t, dir, "r2")
	restoreCaps, restoreMethods := capability.SnapshotForTest(), collection.SnapshotForTest()
	err := captureRun(t, &out, func() error {
		return runGrantCapability([]string{"r3", "clitest.widget_api", "--dir", dir, "--set", "endpoint=https://w", "--reason", "vouched for by hand"})
	})
	restoreCaps()
	restoreMethods()
	resetLoadedCollections()
	if err != nil {
		t.Fatalf("grant-capability = %v\n%s", err, out)
	}
	if g := grantsOn(t, dir, "r1"); len(g) != 1 || g[0].Build != first {
		t.Fatalf("r1's grants = %+v, want one probed by %s", g, first)
	}

	if err := captureRun(t, &out, func() error { return runRevokeCapability([]string{"--build", first, "--dir", dir}) }); err != nil {
		t.Fatalf("revoke-capability --build = %v\n%s", err, out)
	}
	if !strings.Contains(out, `revoked clitest.widget_api from host "r1", probed by build `+first) {
		t.Errorf("revoke-capability --build printed %q", out)
	}
	if g := grantsOn(t, dir, "r1"); len(g) != 0 {
		t.Errorf("r1 keeps %+v, a grant the revoked build probed", g)
	}
	if g := grantsOn(t, dir, "r2"); len(g) != 1 || g[0].Build != second {
		t.Errorf("r2's grants = %+v, want the later build's kept", g)
	}
	if g := grantsOn(t, dir, "r3"); len(g) != 1 || g[0].Source != inventory.GrantAsserted {
		t.Errorf("r3's grants = %+v, want the assertion kept", g)
	}

	for name, args := range map[string][]string{
		"the same build again": {"--build", first, "--dir", dir},
		"a build nobody used":  {"--build", "sha256:" + strings.Repeat("0", 64), "--dir", dir},
	} {
		if err := captureRun(t, &out, func() error { return runRevokeCapability(args) }); err == nil || !strings.Contains(err.Error(), "no device holds a grant probed by build") {
			t.Errorf("%s: %v, want a refusal saying nothing matched", name, err)
		}
	}
	for name, args := range map[string][]string{
		"not a digest":            {"--build", "abc", "--dir", dir},
		"a device beside a build": {"r2", "--build", second, "--dir", dir},
	} {
		if err := captureRun(t, &out, func() error { return runRevokeCapability(args) }); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if g := grantsOn(t, dir, "r2"); len(g) != 1 {
		t.Errorf("a refused revoke changed r2: %+v", g)
	}

	// --inventory: the same revoke reads and writes the file it names (and
	// the state file beside it, where grants live), not --dir's
	// inventory.yaml, which no longer exists.
	elsewhere := t.TempDir()
	moved := filepath.Join(elsewhere, "hosts.yaml")
	if err := os.Rename(filepath.Join(dir, "inventory.yaml"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, ".inventory-state.generated.yaml"), filepath.Join(elsewhere, ".inventory-state.generated.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inventoryOverride = "" })
	if err := captureRun(t, &out, func() error {
		return runRevokeCapability([]string{"--build", second, "--dir", dir, "--inventory", moved})
	}); err != nil {
		t.Fatalf("revoke-capability --build --inventory = %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "inventory.yaml")); err == nil {
		t.Error("revoke-capability --build --inventory wrote --dir's inventory.yaml")
	}
	if g := grantsOn(t, dir, "r2"); len(g) != 0 {
		t.Errorf("r2 in the --inventory file keeps %+v, a grant the revoked build probed", g)
	}
	if g := grantsOn(t, dir, "r3"); len(g) != 1 {
		t.Errorf("r3 in the --inventory file lost its assertion: %+v", g)
	}
}

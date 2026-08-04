package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_AddHostClassify is Phase 6's own representative test (RULE 0)
// for the hierarchical policy resolver's first real consumer, run against
// the real built binary rather than only internal/classification's or
// internal/inventory's own package tests: `add-host --classify` resolves a
// classification path eagerly, at write time, and persists both the
// resolved type and the original path as provenance.
func TestCLI_AddHostClassify(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	out, err := runPleiades(t, dir, "add-host", "web1", "--classify", "linux_server,debian_family,ubuntu")
	if err != nil {
		t.Fatalf("add-host --classify failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "linux_server") {
		t.Errorf("expected add-host's own output to report the resolved type; got:\n%s", out)
	}

	after, err := os.ReadFile(filepath.Join(dir, "inventory.yaml"))
	if err != nil {
		t.Fatalf("failed to read inventory: %v", err)
	}
	got := string(after)
	if !strings.Contains(got, "type: linux_server") {
		t.Errorf("expected the resolved type to be persisted; got:\n%s", got)
	}
	if !strings.Contains(got, "classify:") {
		t.Errorf("expected the original classify path to survive as provenance; got:\n%s", got)
	}

	if out, err := runPleiades(t, dir, "validate"); err != nil {
		t.Fatalf("validate failed against a classify-resolved host: %v\n%s", err, out)
	}
}

// TestCLI_AddHostClassifyUnresolvable proves an unresolvable classify path
// fails add-host loudly (type safety moving left, per Architecture
// Principle 5) and never writes a partial host.
func TestCLI_AddHostClassifyUnresolvable(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	if out, err := runPleiades(t, dir, "add-host", "mystery", "--classify", "totally_unresolvable_path"); err == nil {
		t.Fatalf("expected add-host to fail on an unresolvable classify path; output:\n%s", out)
	}

	after, err := os.ReadFile(filepath.Join(dir, "inventory.yaml"))
	if err != nil {
		t.Fatalf("failed to read inventory: %v", err)
	}
	if strings.Contains(string(after), "mystery") {
		t.Errorf("expected no host to be written after a failed classify resolution; got:\n%s", after)
	}
}

// TestCLI_AddHostRequiresExactlyOneOfTypeOrClassify covers both the
// neither-given and both-given usage errors.
func TestCLI_AddHostRequiresExactlyOneOfTypeOrClassify(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	if out, err := runPleiades(t, dir, "add-host", "neither-host"); err == nil {
		t.Fatalf("expected add-host with neither --type nor --classify to fail; output:\n%s", out)
	}
	if out, err := runPleiades(t, dir, "add-host", "both-host", "--type", "linux_server", "--classify", "linux_server"); err == nil {
		t.Fatalf("expected add-host with both --type and --classify to fail; output:\n%s", out)
	}
}

// Tests for the waiver-expiry rule, run against a source tree built here
// rather than against the repository's own.
//
// The rule's whole job is to fire on a source tree that does not exist yet, so
// a test that only ran it against this repository would prove exactly the half
// that is already proved by `make ci` passing: that it stays quiet today.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// waiverTree writes a fake repository root holding one package with the given
// files, and returns the root.
func waiverTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	pkg := filepath.Join(root, dir)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pkg, err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(pkg, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func TestCheckProbeWaiversStillHold(t *testing.T) {
	// The waiver under test is supplied here rather than read out of the real
	// table, and that changed when the table emptied.
	//
	// It used to read probeWaivers["runner"], which was the only entry and
	// which is gone: its stated condition (cmd/runner has no healthcheck
	// subcommand) ENDED, this rule is what would have failed `make ci` on the
	// day it did, and the chart gained real probes instead. A test that kept
	// reading the live table would now be a test of an empty map, so the rule
	// would stop being exercised at precisely the moment it succeeded.
	//
	// sourceDir points at a real package so the "waiver holds" and "waiver
	// ends" cases below both operate on a tree this test writes itself.
	waiver := probeWaiver{
		reason:    "a test-only waiver, so this rule is exercised whether or not the real table has entries",
		sourceDir: "cmd/runner",
		endedBy:   "aSubcommandNoBinaryHas",
		remedy:    "Give the container a livenessProbe and delete this waiver.",
	}
	withWaiver(t, "test-only", waiver)

	t.Run("the waiver holds while the binary has no such subcommand", func(t *testing.T) {
		root := waiverTree(t, waiver.sourceDir, map[string]string{
			"main.go": "package main\n\nfunc main() {}\n",
		})
		findings, err := checkProbeWaiversStillHold(root)
		if err != nil {
			t.Fatalf("checkProbeWaiversStillHold: %v", err)
		}
		if len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("the waiver ends the moment the subcommand lands", func(t *testing.T) {
		// THE CASE THE RULE EXISTS FOR. Without it, this source tree and the
		// one above produce an identical chart: a runner container with no
		// probes at all, and nothing anywhere saying the reason for that has
		// expired.
		root := waiverTree(t, waiver.sourceDir, map[string]string{
			"main.go":        "package main\n\nfunc main() {}\n",
			"healthcheck.go": "package main\n\nconst healthcheckCommand = \"" + waiver.endedBy + "\"\n",
		})
		findings, err := checkProbeWaiversStillHold(root)
		if err != nil {
			t.Fatalf("checkProbeWaiversStillHold: %v", err)
		}
		if len(findings) != 1 {
			t.Fatalf("reported %v, want exactly one finding", findings)
		}
		// The finding has to carry the remedy, not just the fact. The person
		// who lands that subcommand is not the person who wrote the waiver.
		if !strings.Contains(findings[0].message, waiver.remedy) {
			t.Fatalf("the finding does not carry the remedy: %s", findings[0].message)
		}
	})

	t.Run("a test file naming the subcommand does not end the waiver", func(t *testing.T) {
		// A test that names the subcommand it wants can be written before the
		// subcommand exists, and ending the waiver then would demand a chart
		// probe for a binary that still cannot answer one.
		root := waiverTree(t, waiver.sourceDir, map[string]string{
			"main.go":      "package main\n\nfunc main() {}\n",
			"main_test.go": "package main\n\n// TODO: a " + waiver.endedBy + " subcommand.\n",
		})
		findings, err := checkProbeWaiversStillHold(root)
		if err != nil {
			t.Fatalf("checkProbeWaiversStillHold: %v", err)
		}
		if len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a missing package is an error rather than a silent pass", func(t *testing.T) {
		// A waiver pointed at a directory that is not there proves nothing,
		// and reporting "the waiver still holds" would be this tool claiming
		// a check it did not perform.
		if _, err := checkProbeWaiversStillHold(t.TempDir()); err == nil {
			t.Fatal("reported no error for a waiver whose package does not exist")
		}
	})
}

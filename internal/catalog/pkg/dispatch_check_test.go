// Package pkg_test: tests of the generic package methods' checks.
package pkg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestChecksReachTheConcreteCheckAndNeverRunAptGet is the check half of
// the dispatch tests above, through the same real SSH server and fake
// apt-get and dpkg-query (curl not installed). Each generic check must
// resolve the device's manager exactly as the real run does, predict the
// real run's decision, and never call apt-get, the one command here that
// changes anything. The real run from a fresh harness is the control.
//
// A check of an absent package reads apt-cache for the version it would
// install, so each harness gets a fake apt-cache too. Without it the test
// would read the host's own, and pass or fail with whatever repositories
// the machine running it happens to have.
func TestChecksReachTheConcreteCheckAndNeverRunAptGet(t *testing.T) {
	tests := []struct {
		name   string
		check  collection.Method
		invoke collection.Method
	}{
		{name: "install", check: pkg.CheckInstall, invoke: pkg.Install},
		{name: "remove", check: pkg.CheckRemove, invoke: pkg.Remove},
		{name: "upgrade", check: pkg.CheckUpgrade, invoke: pkg.Upgrade},
	}
	params := func() map[string]any {
		return map[string]any{"name": "curl", "insecure_skip_host_key_verify": true}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev, rc, record := newAptHarness(t)
			writeFakeAptCache(t, filepath.Dir(record))
			checked, err := tt.check(context.Background(), rc, dev, params())
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if sent := verbs(t, record); len(sent) != 0 {
				t.Errorf("the check ran apt-get %v; a check may only read", sent)
			}

			controlDev, controlRC, controlRecord := newAptHarness(t)
			writeFakeAptCache(t, filepath.Dir(controlRecord))
			ran, err := tt.invoke(context.Background(), controlRC, controlDev, params())
			if err != nil {
				t.Fatalf("the real run: %v", err)
			}
			if checked.Changed != ran.Changed {
				t.Errorf("the check predicts changed %v, the real run reported %v", checked.Changed, ran.Changed)
			}
		})
	}
}

// writeFakeAptCache puts an apt-cache on dir, which newAptHarness has
// already put first on PATH, answering policy with one fixed candidate.
func writeFakeAptCache(t *testing.T, dir string) {
	t.Helper()
	script := "#!/bin/sh\nprintf 'curl:\\n  Installed: (none)\\n  Candidate: 8.5.0-2\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "apt-cache"), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake apt-cache: %v", err)
	}
}

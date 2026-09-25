// Tests for `pleiades run --forks`, through the real binary.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_RunForks proves --forks is refused outside 1 to 1000 before
// anything is planned or run, and accepted inside it. That the executor
// honors the number is TestExecutor_ConcurrencyBound's to prove.
func TestCLI_RunForks(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "noop.yaml"), []byte("id: noop\ntasks:\n  - name: step\n    fqcn: noop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0", "-1", "1001"} {
		out, err := runPleiades(t, dir, "run", "runbooks/noop.yaml", "--forks", bad)
		if err == nil || !strings.Contains(out, "--forks must be between 1 and 1000") {
			t.Errorf("--forks %s: err %v, output:\n%s", bad, err, out)
		}
		if strings.Contains(out, "plan for") {
			t.Errorf("--forks %s printed a plan before refusing:\n%s", bad, out)
		}
	}
	if out, err := runPleiades(t, dir, "run", "runbooks/noop.yaml", "--forks", "five"); err == nil {
		t.Errorf("--forks five was accepted:\n%s", out)
	}
	for _, good := range []string{"1", "1000"} {
		if out, err := runPleiades(t, dir, "run", "runbooks/noop.yaml", "--forks", good); err != nil || !strings.Contains(out, "run complete") {
			t.Errorf("--forks %s: err %v, output:\n%s", good, err, out)
		}
	}
}

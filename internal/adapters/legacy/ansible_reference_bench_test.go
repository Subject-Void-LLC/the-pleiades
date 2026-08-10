package legacy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// BenchmarkAnsiblePlaybookDirect_SingleHost is the reference-platform
// comparison AGENTS.md's Performance Benchmarking rule asks for: the
// identical playbook shape this package's own Release Gate runs inside a
// container, run directly against the host's own real ansible-playbook
// instead, with no container boundary at all. The gap between this
// number and BenchmarkContainerOrchestrator_SpawnLatency is the real,
// measured cost this phase's ephemeral-container-per-device design pays
// for isolation, the same comparison
// internal/adapters/native.BenchmarkInvokeChild_InProcess draws against
// its own subprocess-boundary benchmark. Skipped, not failed, when
// ansible-playbook is not on PATH, matching this repository's other
// reference-platform benchmarks.
func BenchmarkAnsiblePlaybookDirect_SingleHost(b *testing.B) {
	path, err := exec.LookPath("ansible-playbook")
	if err != nil {
		b.Skip("ansible-playbook not found on PATH")
	}

	dir := b.TempDir()
	inventory := filepath.Join(dir, "inventory.ini")
	if err := os.WriteFile(inventory, []byte("localhost ansible_connection=local\n"), 0o644); err != nil {
		b.Fatalf("failed to write inventory fixture: %v", err)
	}
	playbook := filepath.Join(dir, "probe.yml")
	content := "---\n- hosts: all\n  gather_facts: false\n  tasks:\n    - name: say hello\n      debug:\n        msg: hello\n"
	if err := os.WriteFile(playbook, []byte(content), 0o644); err != nil {
		b.Fatalf("failed to write playbook fixture: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(path, "-v", "-i", inventory, playbook)
		cmd.Env = append(os.Environ(), "ANSIBLE_FORCE_COLOR=false", "ANSIBLE_NOCOLOR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("ansible-playbook failed: %v\n%s", err, out)
		}
	}
}

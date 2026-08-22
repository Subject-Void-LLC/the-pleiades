package ssh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
)

// BenchmarkSSHExec measures this package's real per-call overhead
// (dial, SSH handshake, one command, teardown) against the shared
// openssh-server container (ssh_container_test.go's
// requireSSHContainer), the Go-native number
// BenchmarkAnsiblePlaybookComparableSSH below is meant to sit next to.
func BenchmarkSSHExec(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping container-backed benchmark in short mode")
	}
	target := containerTarget(b)
	tr := New(Options{InsecureSkipHostKeyVerify: true, DialTimeout: 5 * time.Second})
	cred := containerCred()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := tr.Exec(context.Background(), target, cred, "echo hello")
		if err != nil {
			b.Fatalf("Exec failed: %v", err)
		}
		if result.ExitCode != 0 {
			b.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}
	}
}

// BenchmarkAnsiblePlaybookComparableSSH runs a real ansible-playbook
// process against the SAME shared container BenchmarkSSHExec targets,
// the honest per-run comparison this phase's Fuzz/Stress checklist item
// asks for ("Benchmark against ansible-playbook on an identical task and
// record the comparison"), following
// internal/engine/executor_bench_test.go's BenchmarkAnsiblePlaybookComparable's
// own established precedent of skipping rather than fabricating a
// number when a required binary is not on PATH. ansible's ssh
// connection plugin needs the sshpass binary to supply a password
// non-interactively, so this also skips (with a clear message) when
// sshpass is unavailable, same honest-skip philosophy, not just for
// ansible-playbook itself.
func BenchmarkAnsiblePlaybookComparableSSH(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping container-backed benchmark in short mode")
	}
	ansibleBinary, err := exec.LookPath("ansible-playbook")
	if err != nil {
		b.Skip("ansible-playbook not found on PATH; skipping the comparison rather than fabricating a number")
	}
	if _, err := exec.LookPath("sshpass"); err != nil {
		b.Skip("sshpass not found on PATH (required for ansible's ssh connection plugin to use password auth); skipping the comparison rather than fabricating a number")
	}

	target := containerTarget(b)

	const playbook = `- hosts: target
  gather_facts: no
  tasks:
    - debug: {msg: "task 1"}
`
	playbookPath := filepath.Join(b.TempDir(), "bench.yaml")
	if err := os.WriteFile(playbookPath, []byte(playbook), 0o644); err != nil {
		b.Fatalf("failed to write comparison playbook: %v", err)
	}

	// StrictHostKeyChecking=no and a discarded known_hosts file keep this
	// comparison focused on raw connect+exec cost, the same scope
	// BenchmarkSSHExec above measures (InsecureSkipHostKeyVerify: true);
	// neither benchmark is measuring host key verification cost here.
	targetEndpoint := target.Endpoint.(transport.NetworkEndpoint)
	inventory := fmt.Sprintf(
		"target ansible_host=%s ansible_port=%d ansible_user=%s ansible_ssh_pass=%s ansible_connection=ssh ansible_ssh_common_args='-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'\n",
		targetEndpoint.Host, targetEndpoint.Port, containerSSHUser, containerSSHPassword,
	)
	inventoryPath := filepath.Join(b.TempDir(), "inventory.ini")
	if err := os.WriteFile(inventoryPath, []byte(inventory), 0o644); err != nil {
		b.Fatalf("failed to write comparison inventory: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(ansibleBinary, playbookPath, "-i", inventoryPath)
		cmd.Env = append(os.Environ(), "ANSIBLE_STDOUT_CALLBACK=minimal")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("ansible-playbook failed: %v\n%s", err, out)
		}
	}
}

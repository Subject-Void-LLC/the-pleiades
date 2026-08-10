//go:build integration

// This file measures what a launch actually costs through the whole real
// mesh, and puts that number beside a real ansible-playbook run on the
// same hardware in the same process.
//
// The comparison follows this repository's established rule: the
// "industry alternative" is a sibling benchmark measured here and now,
// never a published figure for a system nobody in this room ran. See
// internal/dispatch/worker_bench_test.go for the same shape.
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
)

// BenchmarkGrandIntegrationDispatch measures accept-to-completion latency
// for one runbook launch across the full mesh: a real authenticated HTTP
// request to the real controller, a durable job row in real PostgreSQL,
// one NATS publish per admitted device, execution by the real runner
// process, and the terminal state read back through the real API.
//
// The harness is provisioned once and torn down through b.Cleanup rather
// than a naked defer, per LESSONS_LEARNED.md #41: a benchmark that
// provisions external infrastructure inside the benchmarked function must
// register teardown the way the framework expects, or the container
// outlives the run.
//
// No fixed message identifier is reused across iterations. The controller
// derives each job's idempotency key from a freshly generated job id, so
// every iteration is a genuinely distinct message. That matters more than
// it sounds: LESSONS_LEARNED.md #33 records that reusing an identifier
// engages JetStream producer-side dedup, and for anything that then waits
// on a resulting delivery it does not understate a number, it hangs.
func BenchmarkGrandIntegrationDispatch(b *testing.B) {
	h := startHarness(b)

	issuer := authtest.NewWithSecret(b, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	token := issuer.Token(b, &auth.Identity{Subject: "e2e-bench", Role: auth.RoleAdmin})

	// Warm the path once outside the timed region, so the first iteration
	// does not absorb connection setup and JetStream consumer creation
	// that no later iteration pays.
	status, body := h.dispatch(b, token, targetGroup, harnessRunbookID)
	if status != http.StatusAccepted {
		b.Fatalf("warmup dispatch returned %d, want 202. Body: %s", status, body)
	}
	h.pollJobUntilTerminal(b, token, requireStringField(b, body, "job_id"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		status, body := h.dispatch(b, token, targetGroup, harnessRunbookID)
		if status != http.StatusAccepted {
			b.Fatalf("dispatch returned %d, want 202. Body: %s", status, body)
		}
		job := h.pollJobUntilTerminal(b, token, requireStringField(b, body, "job_id"))
		if job.State != "completed" {
			b.Fatalf("job state = %q, want completed", job.State)
		}
	}
	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.Logf("[MEASURED] pleiades accept-to-completion, 2 devices per launch, real HTTP + PostgreSQL + NATS + a separate runner process: %.0f ns/op (%.1f ms/launch)",
		nsPerOp, nsPerOp/1e6)
}

// BenchmarkAnsiblePlaybookDispatchComparable is the reference-platform
// sibling, measured on the same hardware in the same run.
//
// It runs a real ansible-playbook over the same device count executing a
// single no-op task, which is the closest honest local stand-in: AWX and
// Ansible Automation Platform have no license-free local equivalent to
// shell out to, so the engine underneath them is what gets measured.
//
// The caveat belongs in the number's own company, not in a footnote
// somebody will skip. These two benchmarks do not measure the same work.
// The Pleiades figure includes a real HTTP round trip, an authenticated
// and authorized request, a durable PostgreSQL write per device, a NATS
// publish per device, and a second operating system process picking the
// work up and executing it. The ansible-playbook figure includes a Python
// interpreter start and a local-connection task per host, with no durable
// persistence, no queue, no API, and no second process. A smaller number
// here is not a defeat and a smaller number there is not a victory; the
// pair exists so the cost of the durability and multi-user guarantees is
// visible rather than assumed.
func BenchmarkAnsiblePlaybookDispatchComparable(b *testing.B) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		b.Skip("ansible-playbook not found on PATH")
	}

	dir := b.TempDir()

	// Two hosts, matching the two devices a targeted dispatch admits in
	// the benchmark above.
	inventory := filepath.Join(dir, "inventory.ini")
	if err := os.WriteFile(inventory, []byte(
		"[edge]\nrtr1 ansible_connection=local\nrtr2 ansible_connection=local\n"), 0o600); err != nil {
		b.Fatalf("writing the inventory: %v", err)
	}

	// One task that does nothing, which is what the "noop" runbook the
	// Pleiades benchmark dispatches also does.
	playbook := filepath.Join(dir, "play.yml")
	if err := os.WriteFile(playbook, []byte(
		"- hosts: edge\n  gather_facts: false\n  tasks:\n    - name: step\n      ansible.builtin.meta: noop\n"), 0o600); err != nil {
		b.Fatalf("writing the playbook: %v", err)
	}

	env := append(os.Environ(),
		"ANSIBLE_LOCALHOST_WARNING=false",
		"ANSIBLE_INVENTORY_UNPARSED_WARNING=false",
		"ANSIBLE_RETRY_FILES_ENABLED=false",
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("ansible-playbook", "-i", inventory, playbook)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("ansible-playbook failed: %v\n%s", err, out)
		}
	}
	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.Logf("[MEASURED] ansible-playbook, 2 local hosts, one no-op task, no persistence and no queue: %.0f ns/op (%.1f ms/run)",
		nsPerOp, nsPerOp/1e6)
	fmt.Fprintln(os.Stderr) // keep the two [MEASURED] lines visually separated in -v output
}

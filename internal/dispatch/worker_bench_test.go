// Package dispatch_test: BenchmarkWorker_DeviceFanOut, the Crawl-tier
// counterpart to internal/engine's own BenchmarkExecutorRun_DeviceFanOut
// (internal/engine/executor_bench_test.go). This file follows that file's
// exact benchmarking idiom rather than inventing a new one for this
// package: a plain b.N loop over the real production call being
// measured, built once before b.ResetTimer, plus a skip-if-absent real
// ansible-playbook subprocess run as the "industry alternative" figure
// AGENTS.md's Performance Benchmarking rule requires, rather than a
// fabricated number.
package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
)

// benchDeviceCount mirrors internal/engine's own
// BenchmarkExecutorRun_DeviceFanOut device count (executor_bench_test.go),
// so the Walk-tier Executor and Crawl-tier dispatch Worker fan-out
// benchmarks measure a comparable workload shape rather than two
// arbitrarily different sizes that cannot be read side by side.
const benchDeviceCount = 50

// benchJobStore builds a real, SQLite-backed JobStore, the identical
// production write path TestEntJobStore_* and TestWorker_* already
// exercise (ent_store_test.go, worker_test.go), sized for a benchmark
// rather than a single test: tb is testing.TB, not testing.T, since
// enttest.Open's own TestingT constraint (Error, FailNow) is satisfied by
// both testing.T and testing.B, letting this one helper serve ordinary
// tests and this benchmark alike without a second, parallel
// implementation.
func benchJobStore(tb testing.TB) dispatch.JobStore {
	tb.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", tb.Name())
	client := enttest.Open(tb, "sqlite3", dsn)
	tb.Cleanup(func() { _ = client.Close() })
	return dispatch.NewEntJobStore(client)
}

// benchRunbookSource builds a real runbook.Source over a fixture
// directory containing one runbook, "pb-1", requiring
// capability.NameCiscoIOS via the real "ios_backup" fqcn, the identical
// fixture newTestRunbookSource (worker_test.go) uses for ordinary tests.
func benchRunbookSource(tb testing.TB) runbook.Source {
	tb.Helper()
	dir := tb.TempDir()
	content := "id: pb-1\ntasks:\n  - name: step\n    fqcn: ios_backup\n"
	path := filepath.Join(dir, "pb-1.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tb.Fatalf("failed to write runbook fixture: %v", err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		tb.Fatalf("NewDirSource: %v", err)
	}
	return src
}

// benchDevices builds benchDeviceCount capableDevice stubs (this
// package's own worker_test.go helper): real inventory.InventoryItem
// values in StateActive, declaring capability.NameCiscoIOS and a "host"
// property, so every one of them clears LifecycleAdmits and
// CapabilityAdmits and reaches the real dispatch-event-publish path this
// benchmark means to measure, rather than being skipped before the loop
// body this benchmark cares about ever runs.
func benchDevices() []pkginventory.InventoryItem {
	devices := make([]pkginventory.InventoryItem, benchDeviceCount)
	for i := range devices {
		devices[i] = capableDevice(fmt.Sprintf("bench-dev-%d", i), fmt.Sprintf("bench-router-%d", i), "10.0.0.1")
	}
	return devices
}

// BenchmarkWorker_DeviceFanOut measures Worker.HandleJobRequested's
// per-job overhead fanning a dispatch out across benchDeviceCount real,
// capability-satisfying devices: a real SQLite-backed JobStore write per
// step (Create, BeginFanOut, RecordTask x50, Complete), real
// LifecycleAdmits/CapabilityAdmits admission checks per device, and one
// real in-process event bus publish per device. A fresh Job is created
// each iteration, inside the timed region, since BeginFanOut only ever
// succeeds once per job (from "pending"; see JobStore.BeginFanOut's own
// doc comment), the identical real write a launcher's own Create call
// always performs before a job.requested event can exist at all. Timing
// that Create alongside the fan-out it gates keeps this benchmark's
// number the full, honest per-job cost, not merely the device loop
// measured in isolation.
func BenchmarkWorker_DeviceFanOut(b *testing.B) {
	store := benchJobStore(b)
	bus := event.NewInProcessBus()
	repo := &fakeRepository{Devices: benchDevices()}
	worker := dispatch.NewWorker(store, repo, benchRunbookSource(b), bus)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := &dispatch.Job{RunbookID: "pb-1", GroupName: "routers", Actor: "bench@example.com"}
		if err := store.Create(ctx, job); err != nil {
			b.Fatalf("Create failed: %v", err)
		}

		data, err := json.Marshal(map[string]string{"job_id": job.JobID})
		if err != nil {
			b.Fatalf("failed to marshal job.requested payload: %v", err)
		}
		evt := event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}

		if err := worker.HandleJobRequested(evt); err != nil {
			b.Fatalf("HandleJobRequested failed: %v", err)
		}
	}
}

// BenchmarkAnsiblePlaybookFanOutComparable runs a real ansible-playbook
// process against a real inventory of benchDeviceCount local-connection
// hosts, the honest per-fan-out comparison IMPLEMENTATION.md's Phase W5
// asks for ("Benchmark against a comparable Ansible workflow") applied to
// this package's own workload shape (fanning one task out across many
// targets, rather than executor_bench_test.go's five-task, single-target
// chain), following that same file's established precedent of skipping
// rather than fabricating a number when ansible-playbook is not on PATH.
// AWX/Ansible Tower itself has no local, license-free equivalent this
// benchmark could shell out to, so ansible-playbook against an inventory
// of this size is the closest honest stand-in, the same substitution
// executor_bench_test.go's own BenchmarkAnsiblePlaybookComparable already
// makes.
func BenchmarkAnsiblePlaybookFanOutComparable(b *testing.B) {
	binary, err := exec.LookPath("ansible-playbook")
	if err != nil {
		b.Skip("ansible-playbook not found on PATH; skipping the comparison rather than fabricating a number")
	}

	dir := b.TempDir()

	// A real inventory file naming benchDeviceCount distinct hosts, each
	// forced to a local connection: no real SSH target exists for this
	// benchmark to reach, and ansible_connection=local is the standard,
	// honest way to measure ansible's own per-host fan-out overhead
	// without conflating it with network or auth latency this benchmark
	// is not trying to measure.
	var inv strings.Builder
	inv.WriteString("[bench]\n")
	for i := 0; i < benchDeviceCount; i++ {
		inv.WriteString(fmt.Sprintf("bench-host-%d ansible_connection=local\n", i))
	}
	invPath := filepath.Join(dir, "inventory.ini")
	if err := os.WriteFile(invPath, []byte(inv.String()), 0o644); err != nil {
		b.Fatalf("failed to write comparison inventory: %v", err)
	}

	const playbook = `- hosts: bench
  gather_facts: no
  tasks:
    - debug: {msg: "dispatched"}
`
	playbookPath := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(playbookPath, []byte(playbook), 0o644); err != nil {
		b.Fatalf("failed to write comparison playbook: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// -f (forks) is set to benchDeviceCount so ansible fans this run
		// out with the same parallelism ceiling Worker's own device loop
		// is not artificially bounded below either; a low default fork
		// count (5) would understate ansible's real achievable fan-out
		// throughput and make this comparison unfair in our own favor.
		cmd := exec.Command(binary, playbookPath, "-i", invPath, "-f", strconv.Itoa(benchDeviceCount))
		cmd.Env = append(os.Environ(), "ANSIBLE_STDOUT_CALLBACK=minimal")
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("ansible-playbook failed: %v\n%s", err, out)
		}
	}
}

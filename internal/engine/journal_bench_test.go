// This file is the run journal's benchmark, and the design note's
// Section 9 is explicit that it is a correctness gate rather than
// tuning. FAILURE_PATTERNS.md #118 states the reason in one sentence:
// "the failure mode of an expensive control is not slowness. It is
// deletion." A journal an operator turns off under load records nothing,
// which is strictly worse than one that was never built, because the
// absence looks like a clean run.
//
// # What is being compared, and the one thing that cannot be
//
// Section 9 asks for "per-task write against no journal". There is no
// no-journal configuration to measure against: recordLevel runs on every
// Run, including one wired to the no-op default sink, deliberately, so
// that the projection is exercised by every ordinary test rather than
// running for the first time on the first real journal write. So the
// comparison is made the other way around, by measuring the projection
// on its own and against the whole run it sits inside:
//
//   - BenchmarkProjectLevel is the per-node cost the journal adds.
//   - BenchmarkExecutorRunJournaled is the whole five-task run, with the
//     no-op sink and with a capturing one, so the sink's own cost is
//     separated from the projection's.
//   - BenchmarkRedactTextComparable is the number the design decision
//     actually rests on: what one value-carrying entry would have cost if
//     the journal stored device output and masked it. Section 9's claim
//     is that the projection "never pays internal/redact's
//     linear-in-literal-set cost, which is what a masking journal would
//     pay on every entry against a set that grows all run". That is a
//     measurable claim, so it is measured here rather than asserted.
//
// # Measured
//
// AGENTS.md requires benchmark results to be recorded rather than only
// produced, so here is the first run, on an Intel i7-8700K, go test
// -bench, against a 3.9 KB running-config-shaped value:
//
//	ProjectLevel/devices=1          2963 ns/op     1920 B/op    12 allocs/op
//	ProjectLevel/devices=5         13714 ns/op     9536 B/op    35 allocs/op
//	ProjectLevel/devices=50       119676 ns/op    73713 B/op   263 allocs/op
//	RedactTextComparable/secrets=1    31623 ns/op
//	RedactTextComparable/secrets=8    55006 ns/op
//	RedactTextComparable/secrets=64  105946 ns/op
//
// Absolute numbers move with machine load, so read the shape rather than
// the digits: a second run on the same box gave 2584 / 14222 / 110134 and
// 25154 / 33964 / 90332. Three things in them matter and none is the
// absolute speed. The projection is flat per node, roughly 2.2 to 3.0
// microseconds whether a level carries one device or fifty. It does not
// move when the value under a key grows, because it never reads one. And
// masking one entry of the same content costs roughly 10 to 35 times as
// much, rising with both the text length and the size of a secret set
// that grows for the whole run, so a masking journal's per-entry cost
// would be worst exactly on the longest runs.
//
// At whole-run granularity the journal disappears into noise:
// BenchmarkExecutorRunJournaled's two sinks and the pre-journal
// BenchmarkExecutorRun_FiveTaskChain all land between 237 and 302
// microseconds across five repetitions of the same five-task runbook,
// which is the executor's own goroutine, lock and event-bus work
// swamping everything else. The stable signal there is allocations: 183
// per run with the no-op sink and 190 with a capturing one, the seven
// being the five kept entries and the slice that holds them.
package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// benchRunningConfig is a device-output-sized string: a few kilobytes of
// configuration text, the shape net.ios.config's backup stat really
// holds.
//
// Its size is the point of every measurement below. The projection reads
// map KEYS and never touches a value, so its cost must not move when
// this grows; redact.Text scans the whole string once per secret, so its
// cost must. A benchmark over a short string would hide both.
var benchRunningConfig = strings.Repeat(
	"interface GigabitEthernet0/1\n ip address 10.0.0.1 255.255.255.0\n no shutdown\n!\n", 48)

// benchNoDeviceResolver resolves nothing, so a benchmarked run stays
// controller-side and measures the executor and the projection rather
// than inventory lookup.
type benchNoDeviceResolver struct{}

// Resolve implements TargetResolver.
func (benchNoDeviceResolver) Resolve(string) []inventory.InventoryItem { return nil }

// benchCapturingJournal keeps every entry, which is the cheapest sink
// that still has to hold what it was handed. It is the stand-in for a
// real sink's own work without the file or network the real ones do.
type benchCapturingJournal struct {
	entries []JournalEntry
}

// Record implements Journal.
func (j *benchCapturingJournal) Record(_ context.Context, entries []JournalEntry) error {
	j.entries = append(j.entries, entries...)
	return nil
}

// benchStats builds one node's stats in the shape a real Collection
// method emits: several top-level keys, a diff carrying the whole prior
// and next configuration, and an inverse carrying it a third time.
//
// exec.command and net.ios.config between them are the model. The values
// are deliberately large, because the claim under test is that the
// projection's cost is independent of them.
func benchStats() map[string]interface{} {
	return map[string]interface{}{
		"cmd":     "show running-config",
		"rc":      0,
		"stdout":  benchRunningConfig,
		"stderr":  "",
		"msg":     "config read",
		"backup":  benchRunningConfig,
		"skipped": false,
		sdk.StatDiff: map[string]interface{}{
			"before": map[string]interface{}{"content": benchRunningConfig},
			"after":  map[string]interface{}{"content": benchRunningConfig},
		},
		sdk.StatInverse: map[string]interface{}{
			sdk.InverseFQCNKey: "net.ios.config",
			sdk.InverseParamsKey: map[string]interface{}{
				"path":    "/tmp/backup.cfg",
				"content": benchRunningConfig,
			},
		},
	}
}

// benchProjectionRun builds the run value projectLevel needs, with one
// task carrying a realistic param map.
func benchProjectionRun() *run {
	task := &Task{
		Name:     "read the running config",
		Register: "cfg",
		FQCN:     "ssh_exec",
		Params: map[string]interface{}{
			"command": "show running-config",
			"target":  "",
			"backup":  true,
		},
	}
	return &run{
		dag: &DAG{
			ID:      "bench",
			Version: "sha256:bench",
			Nodes:   map[string]*Task{"tasks[0]": task},
		},
		runID: "bench-run",
	}
}

// BenchmarkProjectLevel measures the per-node cost the journal adds to a
// run: one level's worth of NodeResults turned into JournalEntries.
//
// The fan-out sizes are one device, the default maxConcurrency of five,
// and a fifty-device level, because the cost the design has to defend is
// per node and a reader needs to see that it stays linear rather than
// finding out at fifty.
func BenchmarkProjectLevel(b *testing.B) {
	for _, devices := range []int{1, 5, 50} {
		b.Run(fmt.Sprintf("devices=%d", devices), func(b *testing.B) {
			stats := benchStats()
			out := make([]NodeResult, devices)
			for i := range out {
				// journalStats is what the projection reads (Stats is nil on
				// the failure paths by design); setting only Stats timed a
				// projection over nothing.
				out[i] = NodeResult{
					NodeID:         "tasks[0]",
					Device:         fmt.Sprintf("device-%d", i),
					Changed:        true,
					Stats:          stats,
					journalStats:   stats,
					journalChanged: true,
				}
			}
			level := [][]NodeResult{out}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := benchProjectionRun()
				entries, err := r.projectLevel(level)
				if err != nil {
					b.Fatalf("projectLevel refused a well formed level: %v", err)
				}
				if len(entries) != devices {
					b.Fatalf("projected %d entries for %d devices", len(entries), devices)
				}
			}
		})
	}
}

// BenchmarkExecutorRunJournaled measures a whole five-task run with the
// journal's two sink shapes, so the projection's cost can be read against
// the run it is embedded in and the sink's own cost can be read against
// the projection's.
//
// It mirrors BenchmarkExecutorRun_FiveTaskChain's workload exactly
// (five controller-side noop tasks) so the two numbers sit next to each
// other honestly.
func BenchmarkExecutorRunJournaled(b *testing.B) {
	eval, err := NewCELEvaluator()
	if err != nil {
		b.Fatalf("failed to init CEL evaluator: %v", err)
	}
	dag, err := NewBuilder(eval).Build([]byte(`{"id":"bench","tasks":[
		{"name":"t1","fqcn":"noop"},
		{"name":"t2","fqcn":"noop"},
		{"name":"t3","fqcn":"noop"},
		{"name":"t4","fqcn":"noop"},
		{"name":"t5","fqcn":"noop"}
	]}`))
	if err != nil {
		b.Fatalf("failed to build DAG: %v", err)
	}

	sinks := map[string]func() []ExecutorOption{
		// The default: the projection runs and the entries are discarded.
		// This is what every Executor with no WithJournal call does today.
		"noop_sink": func() []ExecutorOption { return nil },
		// A sink that keeps what it is handed, which is the floor any real
		// sink sits above.
		"capturing_sink": func() []ExecutorOption {
			return []ExecutorOption{WithJournal(&benchCapturingJournal{})}
		},
	}

	for name, opts := range sinks {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				x := NewExecutor(benchNoDeviceResolver{}, NewBuiltinActionExecutor(),
					lock.NewInProcessManager(), event.NewInProcessBus(),
					NewInProcessWorkflowContext(), 0, opts()...)
				if _, err := x.Run(context.Background(), dag); err != nil {
					b.Fatalf("Run failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkRedactTextComparable is the counterfactual: what one entry
// would cost if the journal stored a device value and masked it on the
// way out.
//
// The literal-set sizes are the point. A run's secret set grows as tasks
// discover secrets (executor_secrets.go's two writers), so a masking
// journal pays this cost against a set that is larger for every
// subsequent entry, on every entry, for a value that is a whole
// configuration file. The projection pays none of it, because it never
// looks at a value at all.
//
// This is not a claim that internal/redact is slow. It is doing a
// linear-in-secrets scan over a linear-in-text string because that is
// what masking is; the measurement exists to show what the journal
// avoided by not needing masking, which is Section 1's whole argument.
func BenchmarkRedactTextComparable(b *testing.B) {
	for _, secrets := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("secrets=%d", secrets), func(b *testing.B) {
			set := make([]string, secrets)
			for i := range set {
				set[i] = fmt.Sprintf("secret-value-%03d", i)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if out := redact.Text(set, benchRunningConfig); out == "" {
					b.Fatal("redact.Text returned nothing for a non-empty config")
				}
			}
		})
	}
}

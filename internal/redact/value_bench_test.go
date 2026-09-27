// Package redact_test holds the benchmarks for Value, measured against
// the render-then-mask path the text view used before it.
package redact_test

import (
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// The benchmarks for Value, which masks every stat of every task a run
// reports before either view prints it.
//
// The comparison that matters is BenchmarkValueLargeStats against
// BenchmarkRenderedTextLargeStats, which is what the text view did before
// Value existed: render the stats as YAML, then mask the text. Value walks
// the tree and masks each string, so it pays per string rather than per
// byte of rendered output, and it also applies the key rules, which text
// masking cannot. AWX has no comparable component (its masking is in the
// Python callback plugin, on a different path), so no industry figure is
// invented here.
//
// Measured 2026-09-27 on the lab machine (Core Ultra 7 265KF, WSL 2),
// three runs each: Value 1.20 to 1.33 ms, 391 KB and 6,005 allocations
// per tree; render and mask 9.2 to 9.4 ms, 15.7 MB and 34,078
// allocations. So Value is about eight times faster than the path it sits
// in front of. The text view still renders and masks its text after Value,
// which adds about 13% to that view's cost on a tree this large, for key
// rules and structure-aware masking it did not have.

// largeStats is a stats tree the size of a big virt.vbox.vm.list: 500 VMs
// of ten fields each, plus a long stdout.
func largeStats() map[string]any {
	vms := make([]map[string]any, 500)
	for i := range vms {
		vms[i] = map[string]any{
			"name": fmt.Sprintf("vm-%03d", i), "uuid": fmt.Sprintf("%08d-0000-0000-0000-000000000000", i),
			"state": "running", "cpus": 2, "memory_mb": 2048, "address": fmt.Sprintf("192.168.56.%d", i%250),
			"device": "lab", "size": "small", "autostart_enabled": false, "token": "t",
		}
	}
	return map[string]any{"vms": vms, "stdout": strings.Repeat("a line of output\n", 2000), "rc": 0}
}

func BenchmarkValueLargeStats(b *testing.B) {
	stats, secrets := largeStats(), []string{"hunter2-secret"}
	for b.Loop() {
		_ = redact.Value(secrets, stats)
	}
}

func BenchmarkRenderedTextLargeStats(b *testing.B) {
	stats, secrets := largeStats(), []string{"hunter2-secret"}
	for b.Loop() {
		out, err := yaml.Marshal(stats)
		if err != nil {
			b.Fatal(err)
		}
		_ = redact.Text(secrets, string(out))
	}
}

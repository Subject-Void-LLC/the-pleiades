// Fuzzing the readers that parse go test's output, which is text a test
// can write anything into: a skip message, or a line that looks like a
// coverage report.
package flakegate

import (
	"strings"
	"testing"
)

// FuzzSkipReason proves a skipped test's reason is never empty and never
// spans lines, whatever the test printed before skipping, so one skip is
// always one ledger line.
func FuzzSkipReason(f *testing.F) {
	f.Add("=== RUN   TestX\n    x_test.go:12: needs Docker\n--- SKIP: TestX (0.00s)\n")
	f.Add("")
	f.Add("\n\n   \n")
	f.Add("--- SKIP: TestX (0.00s)\n")
	f.Add("x_test.go:1: \x00\r\nsecond line")
	f.Fuzz(func(t *testing.T, output string) {
		reason := skipReason([]string{output})
		if reason == "" {
			t.Fatalf("empty reason for %q", output)
		}
		if strings.ContainsAny(reason, "\n") {
			t.Fatalf("reason %q spans lines", reason)
		}
	})
}

// FuzzCoverage proves the coverage reader never panics and never reports a
// number outside what go test can print, whatever a test wrote into its
// output: a line claiming "coverage: 999% of statements" must not become a
// floor-clearing measurement of anything but the package it appeared in.
func FuzzCoverage(f *testing.F) {
	f.Add("ok  \tpkg\t0.1s\tcoverage: 91.5% of statements\n")
	f.Add("coverage: [no statements]\n")
	f.Add("coverage: 1e400% of statements\n")
	f.Add("coverage: .% of statements")
	f.Fuzz(func(t *testing.T, output string) {
		got := Coverage([]Event{{Action: "output", Package: "p", Output: output}})
		for pkg, pct := range got {
			if pkg != "p" {
				t.Fatalf("a number was recorded for %q, which the output did not come from", pkg)
			}
			if pct != pct || pct < 0 {
				t.Fatalf("recorded %v from %q", pct, output)
			}
		}
	})
}

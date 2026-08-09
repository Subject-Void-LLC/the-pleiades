package topology_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// FuzzDurableName is this phase's Schema/Injection Hardening proof for
// topology.DurableName: no arbitrary caller-supplied string, however
// adversarial, can ever produce an illegal NATS durable-consumer name or an
// empty one. This is the same class of bug FAILURE_PATTERNS.md #18 records
// for an unvalidated runbook ID widening a NATS subject; here the
// equivalent risk is a crafted topic string reaching Bus.Subscribe and
// misrouting or breaking consumer creation.
func FuzzDurableName(f *testing.F) {
	seeds := []string{
		"pleiades.jobs.dispatch",
		"",
		"a.b",
		"a/b",
		"a\\b",
		"topic with spaces",
		"topic\twith\ttabs",
		"topic\nwith\nnewlines",
		"pleiades.events.>",
		"pleiades.events.*",
		"\x00\x01\x02",
		"unicode-é中文",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, logical string) {
		got := topology.DurableName(logical)

		if got == "" {
			t.Fatalf("DurableName(%q) returned an empty string", logical)
		}
		if !legalDurableName.MatchString(got) {
			t.Fatalf("DurableName(%q) = %q, contains illegal NATS durable-name characters", logical, got)
		}

		// Determinism must hold for every fuzzed input too, not just the
		// seed corpus: a durable consumer name that changes between calls
		// for the same logical subscriber can never actually resume.
		again := topology.DurableName(logical)
		if got != again {
			t.Fatalf("DurableName(%q) not deterministic: got %q then %q", logical, got, again)
		}
	})
}

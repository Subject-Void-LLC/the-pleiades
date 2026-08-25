package topology_test

import (
	"strings"
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

// FuzzDispatchSubject is Phase 101a's Schema/Injection Hardening proof for
// the dispatch subject: no device id, however adversarial, can widen a
// dispatch subject beyond the one token it is allowed to occupy.
//
// A device id is the input that makes this worth fuzzing rather than
// tabling. It is operator-supplied and pkg/inventory documents it as
// opaque, so it arrives from a YAML inventory or the API as arbitrary
// text, and it reaches subject construction on the dispatch path at the
// moment a job fans out. An id that expanded into several tokens would
// escape every filter subject this package declares, and the device would
// silently stop being dispatched to anyone; an id carrying "*" or ">"
// would be a wildcard published into the middle of a subject.
//
// Two inputs rather than one, because the property that matters most needs
// a pair: two DIFFERENT device ids must never land on the same subject.
// That is the whole reason a hash is appended rather than the input merely
// being sanitized, and a single-input fuzz cannot observe it.
func FuzzDispatchSubject(f *testing.F) {
	seeds := [][2]string{
		{"sw1", "sw2"},
		{"a.b", "a/b"},
		{"", "unnamed"},
		{"router1.example.com", "router1_example_com"},
		{"*", ">"},
		{"dev id", "dev\tid"},
		{"\x00\x01\x02", "\x00\x01\x03"},
		{"unicode-é中文", "unicode-e"},
		{"4d6e9c14-0785-49d2-b821-51a81b54b1cc", "4d6e9c14-0785-49d2-b821-51a81b54b1cd"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}

	prefix := strings.TrimSuffix(topology.DispatchSubjectAll(), ">")

	f.Fuzz(func(t *testing.T, idA, idB string) {
		subject := topology.DispatchSubject(idA)

		if !strings.HasPrefix(subject, prefix) {
			t.Fatalf("DispatchSubject(%q) = %q, which is not under the fleet filter %q",
				idA, subject, topology.DispatchSubjectAll())
		}

		// Exactly one token after the prefix. This is the property the
		// whole function exists for: the fleet filter and a per-device
		// filter both assume it, and neither would report its absence.
		token := strings.TrimPrefix(subject, prefix)
		if token == "" {
			t.Fatalf("DispatchSubject(%q) = %q, whose device token is empty", idA, subject)
		}
		if !legalDurableName.MatchString(token) {
			t.Fatalf("DispatchSubject(%q) = %q, whose device token %q is not a single legal token",
				idA, subject, token)
		}

		// Determinism has to hold for every fuzzed input, not only the
		// seeds: the Runner publishing a subject and the Controller
		// filtering on it are different processes agreeing by nothing more
		// than calling this function.
		if again := topology.DispatchSubject(idA); subject != again {
			t.Fatalf("DispatchSubject(%q) not deterministic: got %q then %q", idA, subject, again)
		}

		// The pair property. Two different devices sharing one subject
		// would mean one device's dispatch delivered under another's name,
		// which a per-device permission would then authorize.
		if idA != idB && subject == topology.DispatchSubject(idB) {
			t.Fatalf("device ids %q and %q both produced subject %q", idA, idB, subject)
		}
	})
}

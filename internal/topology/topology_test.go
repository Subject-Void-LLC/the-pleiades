package topology_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

func TestEventSubject(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		want      string
	}{
		{"simple type", "device.created", "pleiades.events.device.created"},
		{"nested workflow type", "workflow.node.status", "pleiades.events.workflow.node.status"},
		{"empty type", "", "pleiades.events."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topology.EventSubject(tt.eventType); got != tt.want {
				t.Errorf("EventSubject(%q) = %q, want %q", tt.eventType, got, tt.want)
			}
		})
	}
}

func TestDispatchSubject(t *testing.T) {
	// The device token is what the whole change is for, so the literal is
	// asserted rather than rebuilt from the same helper the code uses,
	// which would assert nothing.
	if got, want := topology.DispatchSubject("sw1"), "pleiades.jobs.dispatch.sw1-cf4ac28e"; got != want {
		t.Errorf("DispatchSubject(%q) = %q, want %q", "sw1", got, want)
	}
}

// TestDispatchSubjectIsOneTokenPerDevice is the property the flat subject
// could not offer: two devices never share a dispatch subject, and each
// subject adds exactly one token, so a filter over the prefix matches
// every device and a filter over one device matches only it.
func TestDispatchSubjectIsOneTokenPerDevice(t *testing.T) {
	a := topology.DispatchSubject("device-a")
	b := topology.DispatchSubject("device-b")
	if a == b {
		t.Fatalf("two devices share one dispatch subject: %q", a)
	}

	prefix := strings.TrimSuffix(topology.DispatchSubjectAll(), ">")
	for _, subject := range []string{a, b} {
		if !strings.HasPrefix(subject, prefix) {
			t.Errorf("subject %q is not under the fleet filter %q", subject, topology.DispatchSubjectAll())
			continue
		}
		if rest := strings.TrimPrefix(subject, prefix); strings.Contains(rest, ".") {
			t.Errorf("subject %q adds %d tokens, want exactly 1: a device id that expands into several tokens still matches the fleet filter's trailing wildcard, but breaks every single-token per-device filter, which is what scoping needs",
				subject, strings.Count(rest, ".")+1)
		}
	}
}

// TestDispatchSubjectRefusesToLeakTokens is the injection half. A device id
// is operator-supplied and opaque, so an ordinary hostname must not be able
// to widen a three-token subject into a six-token one, and a wildcard must
// not survive into the middle of a subject.
func TestDispatchSubjectRefusesToLeakTokens(t *testing.T) {
	prefix := strings.TrimSuffix(topology.DispatchSubjectAll(), ">")
	for _, id := range []string{
		"router1.example.com",
		"*",
		">",
		"a b",
		"..",
		"dev\tid",
	} {
		rest := strings.TrimPrefix(topology.DispatchSubject(id), prefix)
		if strings.ContainsAny(rest, ".*>") || strings.ContainsAny(rest, " \t\r\n") {
			t.Errorf("DispatchSubject(%q) produced the token %q, which is not a single legal token", id, rest)
		}
	}
}

// TestSubjectTokenKeepsDistinctInputsDistinct is why sanitizing alone is
// not enough: two ids differing only in characters the allow-list drops
// would otherwise collapse onto one subject and silently share it.
func TestSubjectTokenKeepsDistinctInputsDistinct(t *testing.T) {
	if a, b := topology.SubjectToken("a.b"), topology.SubjectToken("a/b"); a == b {
		t.Errorf("SubjectToken(%q) and SubjectToken(%q) both produced %q", "a.b", "a/b", a)
	}
}

// TestSubjectTokenIsDeterministic matters because the publisher and the
// subscriber of a subject run in different processes: the Runner publishes
// a job's log lines and the Controller subscribes to them, and they agree
// only by calling this function.
func TestSubjectTokenIsDeterministic(t *testing.T) {
	if a, b := topology.SubjectToken("job-1"), topology.SubjectToken("job-1"); a != b {
		t.Errorf("SubjectToken is not stable across calls: %q vs %q", a, b)
	}
}

// TestSubjectTokenNamesAnEmptyInput proves an empty id produces a real
// token rather than an empty one, which would collapse the subject by a
// token and match a filter nobody wrote.
func TestSubjectTokenNamesAnEmptyInput(t *testing.T) {
	got := topology.SubjectToken("")
	if got == "" || strings.HasPrefix(got, "-") {
		t.Errorf("SubjectToken(\"\") = %q, want a non-empty token", got)
	}
}

func TestLogSubject(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
		want  string
	}{
		// The hash suffix is what stops two job ids differing only in
		// characters the allow-list drops from sharing one log stream.
		{"uuid job id", "abc-123", "pleiades.jobs.logs.abc-123-5942d94f"},
		// An empty job id used to produce a subject ending in the
		// separator, which is one token short and matches a filter nobody
		// wrote. It now names itself instead.
		{"empty job id", "", "pleiades.jobs.logs.unnamed-e3b0c442"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topology.LogSubject(tt.jobID); got != tt.want {
				t.Errorf("LogSubject(%q) = %q, want %q", tt.jobID, got, tt.want)
			}
		})
	}
}

// TestResultSubject is LogSubject's own mirror for
// internal/runner's Write-Ahead-Log flush subject (PLAN.md Section 16's
// State Desync Mitigation).
func TestResultSubject(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
		want  string
	}{
		{"uuid job id", "abc-123", "pleiades.jobs.results.abc-123-5942d94f"},
		{"empty job id", "", "pleiades.jobs.results.unnamed-e3b0c442"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topology.ResultSubject(tt.jobID); got != tt.want {
				t.Errorf("ResultSubject(%q) = %q, want %q", tt.jobID, got, tt.want)
			}
		})
	}

	// ResultSubject must fall under StreamSubjectRoot ("pleiades.>"), the
	// same guarantee every other subject this package builds already has,
	// so it needs no separate stream or EnsureStream change.
	if got := topology.ResultSubject("job-1"); !strings.HasPrefix(got, "pleiades.") {
		t.Errorf("ResultSubject(%q) = %q, does not fall under StreamSubjectRoot %q", "job-1", got, topology.StreamSubjectRoot)
	}
}

// TestJobRequestedSubject proves JobRequestedSubject's fixed return value,
// mirroring TestDispatchSubject exactly: like DispatchSubject, it takes no
// argument and always returns the same subject, so there is nothing to
// table-drive, only the one value to pin down.
func TestJobRequestedSubject(t *testing.T) {
	if got, want := topology.JobRequestedSubject(), "pleiades.jobs.requested"; got != want {
		t.Errorf("JobRequestedSubject() = %q, want %q", got, want)
	}
}

func TestDeadLetterSubject(t *testing.T) {
	tests := []struct {
		name     string
		original string
		want     string
	}{
		{"dispatch subject", "pleiades.jobs.dispatch", "pleiades.dlq.pleiades.jobs.dispatch"},
		{"event subject", "pleiades.events.device.created", "pleiades.dlq.pleiades.events.device.created"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topology.DeadLetterSubject(tt.original); got != tt.want {
				t.Errorf("DeadLetterSubject(%q) = %q, want %q", tt.original, got, tt.want)
			}
		})
	}
}

// legalDurableName matches exactly the character class NATS allows in a
// durable consumer name: this test's own ground truth for "legal," checked
// independently of DurableName's internal regexp so a bug in the
// implementation's own pattern can't hide from its test.
var legalDurableName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func TestDurableName(t *testing.T) {
	tests := []struct {
		name    string
		logical string
	}{
		{"plain topic", "pleiades.jobs.dispatch"},
		{"topic with slash", "jobs/logs/123"},
		{"topic with wildcard", "pleiades.events.>"},
		{"topic with whitespace", "some topic with spaces"},
		{"empty string", ""},
		{"already legal", "already_legal-name"},
		{"longer than maxDurablePrefixLen", "pleiades.events.a.very.long.subject.name.that.exceeds.the.forty.eight.character.prefix.cap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topology.DurableName(tt.logical)
			if !legalDurableName.MatchString(got) {
				t.Errorf("DurableName(%q) = %q, contains illegal NATS durable-name characters", tt.logical, got)
			}
			if got == "" {
				t.Errorf("DurableName(%q) returned an empty string", tt.logical)
			}
		})
	}
}

func TestDurableName_Deterministic(t *testing.T) {
	logical := "pleiades.jobs.dispatch"
	first := topology.DurableName(logical)
	second := topology.DurableName(logical)
	if first != second {
		t.Errorf("DurableName(%q) is not deterministic: got %q then %q", logical, first, second)
	}
}

func TestDurableName_DistinctInputsAfterSanitizationCollisionStayDistinct(t *testing.T) {
	// "a.b" and "a/b" both sanitize to the same "a_b" prefix; the hash
	// suffix must keep them apart, or two different logical subscribers
	// would silently share one consumer group.
	a := topology.DurableName("a.b")
	b := topology.DurableName("a/b")
	if a == b {
		t.Errorf("DurableName(%q) and DurableName(%q) collided: both produced %q", "a.b", "a/b", a)
	}
}

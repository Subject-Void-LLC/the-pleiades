package topology_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
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
	if got, want := topology.DispatchSubject(), "pleiades.jobs.dispatch"; got != want {
		t.Errorf("DispatchSubject() = %q, want %q", got, want)
	}
}

func TestLogSubject(t *testing.T) {
	tests := []struct {
		name  string
		jobID string
		want  string
	}{
		{"uuid job id", "abc-123", "pleiades.jobs.logs.abc-123"},
		{"empty job id", "", "pleiades.jobs.logs."},
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
		{"uuid job id", "abc-123", "pleiades.jobs.results.abc-123"},
		{"empty job id", "", "pleiades.jobs.results."},
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

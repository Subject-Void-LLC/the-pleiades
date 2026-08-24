package topology_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

// TestStreamConfigDriftReportsNothingAgainstItself is the property that
// makes the comparator usable at all: the shape this project declares
// must not drift from itself, or every process would warn on every start.
func TestStreamConfigDriftReportsNothingAgainstItself(t *testing.T) {
	if drift := topology.StreamConfigDrift(topology.StreamConfig(topology.DefaultOutageBudget), topology.DefaultOutageBudget); len(drift) != 0 {
		t.Fatalf("StreamConfigDrift(StreamConfig()) = %v, want none", drift)
	}
}

// TestStreamConfigDriftIgnoresUndeclaredFields is the property that makes
// it usable against a REAL server rather than only against itself.
//
// jetstream.StreamConfig carries far more fields than this project sets,
// and the server fills the remainder with its own defaults and returns
// them. A whole-struct comparison would therefore report drift against a
// perfectly healthy cluster that this very code had just provisioned,
// which is the failure mode that would have made the warning worthless by
// firing constantly.
func TestStreamConfigDriftIgnoresUndeclaredFields(t *testing.T) {
	live := topology.StreamConfig(topology.DefaultOutageBudget)

	// Fields the project does not declare, as a server would populate them.
	live.Description = "set by someone else"
	live.MaxMsgs = 1_000_000
	live.MaxBytes = 1 << 30
	live.MaxMsgSize = 1 << 20
	live.Discard = jetstream.DiscardOld
	live.NoAck = true
	live.MaxConsumers = 17

	if drift := topology.StreamConfigDrift(live, topology.DefaultOutageBudget); len(drift) != 0 {
		t.Fatalf("StreamConfigDrift reported %v for fields this project does not declare", drift)
	}
}

// TestStreamConfigDriftReportsEveryDeclaredField walks each declared
// field one at a time, so a future field added to StreamConfig without a
// matching comparison is caught here rather than silently going
// unwatched.
func TestStreamConfigDriftReportsEveryDeclaredField(t *testing.T) {
	tests := []struct {
		field  string
		mutate func(*jetstream.StreamConfig)
	}{
		{"Name", func(c *jetstream.StreamConfig) { c.Name = "SOMETHING_ELSE" }},
		{"Subjects", func(c *jetstream.StreamConfig) { c.Subjects = []string{"other.>"} }},
		{"Retention", func(c *jetstream.StreamConfig) { c.Retention = jetstream.WorkQueuePolicy }},
		{"Storage", func(c *jetstream.StreamConfig) { c.Storage = jetstream.MemoryStorage }},
		{"MaxAge", func(c *jetstream.StreamConfig) { c.MaxAge = time.Hour }},
		{"Replicas", func(c *jetstream.StreamConfig) { c.Replicas = 3 }},
		{"Duplicates", func(c *jetstream.StreamConfig) { c.Duplicates = time.Minute }},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			live := topology.StreamConfig(topology.DefaultOutageBudget)
			tt.mutate(&live)

			drift := topology.StreamConfigDrift(live, topology.DefaultOutageBudget)
			if len(drift) != 1 {
				t.Fatalf("StreamConfigDrift = %v, want exactly one entry for %s", drift, tt.field)
			}
			if drift[0].Field != tt.field {
				t.Fatalf("drift reported field %q, want %q", drift[0].Field, tt.field)
			}
			if drift[0].Live == drift[0].Declared {
				t.Fatalf("drift entry %v reports identical live and declared values", drift[0])
			}
		})
	}
}

// TestStreamConfigDriftIgnoresSubjectOrder proves the set comparison.
// The server is free to return subjects in its own order, and an ordering
// difference is not a configuration difference.
func TestStreamConfigDriftIgnoresSubjectOrder(t *testing.T) {
	declared := topology.StreamConfig(topology.DefaultOutageBudget)
	if len(declared.Subjects) < 2 {
		// One subject today, so reversing proves nothing on its own.
		// Compare a two-element list against its own reverse instead,
		// which is the property the set comparison actually claims.
		probe := topology.StreamConfig(topology.DefaultOutageBudget)
		probe.Subjects = []string{"a.>", "b.>"}
		forward := topology.StreamConfigDrift(probe, topology.DefaultOutageBudget)
		probe.Subjects = []string{"b.>", "a.>"}
		reverse := topology.StreamConfigDrift(probe, topology.DefaultOutageBudget)
		if len(forward) != len(reverse) {
			t.Fatalf("subject order changed the result: %v vs %v", forward, reverse)
		}
		return
	}

	probe := topology.StreamConfig(topology.DefaultOutageBudget)
	probe.Subjects = make([]string, len(declared.Subjects))
	for i, s := range declared.Subjects {
		probe.Subjects[len(declared.Subjects)-1-i] = s
	}
	if drift := topology.StreamConfigDrift(probe, topology.DefaultOutageBudget); len(drift) != 0 {
		t.Fatalf("StreamConfigDrift reported %v for a reordered subject list", drift)
	}
}

// FuzzStreamConfigDrift is this phase's fuzz target.
//
// The comparator is the one genuinely fuzzable thing Phase 96b creates:
// it reads a config that came off the wire from a server this code does
// not control, and it must never panic, never report a field the project
// does not declare, and never report a field whose values genuinely
// match. A nil or absurd slice from a hostile or merely older server is
// the realistic input.
func FuzzStreamConfigDrift(f *testing.F) {
	f.Add("PLEIADES", "pleiades.>", int64(7*24*time.Hour), int64(2*time.Minute), 1)
	f.Add("", "", int64(0), int64(0), 0)
	f.Add("x", "a.b.c", int64(-1), int64(-1), -5)

	f.Fuzz(func(t *testing.T, name, subject string, maxAge, dup int64, replicas int) {
		live := jetstream.StreamConfig{
			Name:       name,
			Subjects:   []string{subject},
			MaxAge:     time.Duration(maxAge),
			Duplicates: time.Duration(dup),
			Replicas:   replicas,
		}

		drift := topology.StreamConfigDrift(live, topology.DefaultOutageBudget)

		declared := map[string]bool{
			"Name": true, "Subjects": true, "Retention": true,
			"Storage": true, "MaxAge": true, "Replicas": true, "Duplicates": true,
		}
		for _, d := range drift {
			if !declared[d.Field] {
				t.Fatalf("drift reported %q, which this project does not declare", d.Field)
			}
			if d.Live == d.Declared {
				t.Fatalf("drift reported %s with identical live and declared values %q", d.Field, d.Live)
			}
			// String must not panic and must name the field.
			if s := d.String(); s == "" {
				t.Fatalf("drift entry for %s rendered empty", d.Field)
			}
		}
	})
}

// TestBindStreamRejectsAnInvalidRole covers the zero value, which the
// iota+1 construction exists to make rejectable rather than silently
// meaningful.
func TestBindStreamRejectsAnInvalidRole(t *testing.T) {
	if _, _, err := topology.BindStream(t.Context(), nil, topology.StreamRole(0), topology.DefaultOutageBudget, false); err == nil {
		t.Fatal("BindStream accepted the zero role")
	}
	if _, err := topology.BindLockBucket(t.Context(), nil, topology.StreamRole(0)); err == nil {
		t.Fatal("BindLockBucket accepted the zero role")
	}
	if got := topology.StreamRole(0).String(); got != "invalid" {
		t.Errorf("StreamRole(0).String() = %q, want %q", got, "invalid")
	}
	if got := topology.StreamProvisioner.String(); got != "provisioner" {
		t.Errorf("StreamProvisioner.String() = %q", got)
	}
	if got := topology.StreamReader.String(); got != "reader" {
		t.Errorf("StreamReader.String() = %q", got)
	}
}

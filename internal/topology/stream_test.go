package topology_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

func TestStreamConfig(t *testing.T) {
	cfg := topology.StreamConfig()

	if cfg.Name != topology.StreamName {
		t.Errorf("StreamConfig().Name = %q, want %q", cfg.Name, topology.StreamName)
	}
	if len(cfg.Subjects) != 1 || cfg.Subjects[0] != topology.StreamSubjectRoot {
		t.Errorf("StreamConfig().Subjects = %v, want [%q]", cfg.Subjects, topology.StreamSubjectRoot)
	}
	if cfg.Storage != jetstream.FileStorage {
		t.Errorf("StreamConfig().Storage = %v, want FileStorage (durable, not ephemeral)", cfg.Storage)
	}
	if cfg.Duplicates <= 0 {
		t.Errorf("StreamConfig().Duplicates = %v, want a positive window so producer-side dedup (jetstream.WithMsgID) is actually enforced", cfg.Duplicates)
	}

	// Every subject this package hands out must actually be covered by the
	// one stream it declares, or a publish to it silently fails exactly
	// like the pre-topology "runbooks.dispatch" bug this package exists to
	// prevent.
	for _, subject := range []string{
		topology.EventSubject("device.created"),
		topology.DispatchSubject(),
		topology.LogSubject("job-1"),
		topology.DeadLetterSubject(topology.DispatchSubject()),
	} {
		if !subjectCoveredByRoot(cfg.Subjects[0], subject) {
			t.Errorf("subject %q is not covered by stream filter %q", subject, cfg.Subjects[0])
		}
	}
}

// subjectCoveredByRoot reports whether subject falls under root, which is
// expected to be a trailing ">" wildcard (topology's one real case). This
// intentionally duplicates none of internal/event's own subjectMatches
// logic; it is a narrow, test-only check of one specific, fixed pattern.
func subjectCoveredByRoot(root, subject string) bool {
	if len(root) < 2 || root[len(root)-1] != '>' {
		return root == subject
	}
	prefix := root[:len(root)-1]
	return len(subject) > len(prefix) && subject[:len(prefix)] == prefix
}

package native

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// taskTimeout reads fields' own "timeout" field and converts it to the
// time.Duration engine.WithTaskTimeout expects, split into its own
// function for the same reason internal/adapters/legacy's runTimeout is:
// a small, table-tested pure conversion, kept out of Execute's own
// sequencing logic.
//
// The runbook kind's own FieldSpec help text is explicit that this field
// is a PER-TASK bound ("seconds before a task is abandoned"), not a
// whole-run one: contrast internal/adapters/legacy's runTimeout, which
// implements the playbook kind's whole-run reading of the identically
// named field. Zero (unset, or an explicit 0) means no timeout, the
// field's own declared default rather than an omission.
func taskTimeout(fields launch.Fields) time.Duration {
	seconds := fields.Int("timeout")
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

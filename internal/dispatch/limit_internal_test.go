// Tests of how a job's limit is read.
package dispatch

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestLimitOf_ARollbackJobIsHeldOutsideIt: a rollback job dispatches the
// devices its plan undoes, so its limit, carried over from the job it undoes,
// is not read at all, even one that would refuse.
func TestLimitOf_ARollbackJobIsHeldOutsideIt(t *testing.T) {
	w := &Worker{}
	l, reason, err := w.limitOf(context.Background(), &Job{RollbackOf: "job-1", Fields: launch.Fields{LimitField: "~bad"}})
	if err != nil || reason != "" || !l.limit.IsZero() {
		t.Errorf("a rollback job's limit read as %+v, %q, %v; want none", l, reason, err)
	}
}

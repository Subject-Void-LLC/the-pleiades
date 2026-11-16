// Tests for StagedChecksAllowed.
package sdk

import "testing"

// stagedContext answers StagedChecks with allowed.
type stagedContext struct {
	RunbookContext
	allowed bool
}

func (c stagedContext) StagedChecksAllowed() bool { return c.allowed }

func TestStagedChecksAllowed(t *testing.T) {
	if StagedChecksAllowed(stagedContext{}) {
		t.Error("a context answering false allows a staged check")
	}
	if !StagedChecksAllowed(stagedContext{allowed: true}) {
		t.Error("a context answering true does not allow a staged check")
	}
	var silent struct{ RunbookContext }
	if StagedChecksAllowed(silent) {
		t.Error("a context that does not say allows a staged check")
	}
}

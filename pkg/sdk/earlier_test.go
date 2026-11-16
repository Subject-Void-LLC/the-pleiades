// Tests for EarlierChangePredicted and its context carriage.
package sdk

import (
	"context"
	"testing"
)

// earlierContext answers EarlierChanges with earlier.
type earlierContext struct {
	RunbookContext
	earlier bool
}

func (c earlierContext) EarlierChangePredicted() bool { return c.earlier }

func TestEarlierChangePredicted(t *testing.T) {
	if !EarlierChangePredicted(earlierContext{earlier: true}) || EarlierChangePredicted(earlierContext{}) {
		t.Error("a context's own answer was not read")
	}
	var silent struct{ RunbookContext }
	if EarlierChangePredicted(silent) {
		t.Error("a context that does not say read as an earlier change")
	}
	if EarlierChangeFrom(context.Background()) || !EarlierChangeFrom(WithEarlierChange(context.Background(), true)) {
		t.Error("the context carriage lost or invented the answer")
	}
}

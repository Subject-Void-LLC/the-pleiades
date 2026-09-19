// Package routing: CheckOnly, which the Runner's check loop runs every
// dispatch through.
package routing

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// CheckOnly wraps next so that every dispatch it runs is a check, whatever
// the payload says: the Runner's pull loop over the check subject
// (topology.CheckConsumerConfig) runs through it. The subject a dispatch
// arrived on is the decision, and the payload's own mode can only agree
// with it, never widen it: a payload on the check subject claiming
// "execute" is still checked.
func CheckOnly(next Executor) Executor {
	return checkOnly{next: next}
}

// checkOnly is CheckOnly's Executor.
type checkOnly struct {
	next Executor
}

// Execute runs payload as a check.
func (c checkOnly) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	payload.Mode = string(collection.ModeCheck)
	return c.next.Execute(ctx, payload)
}

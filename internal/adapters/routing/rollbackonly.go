// Package routing: RollbackOnly, which the Runner's rollback loop runs
// every dispatch through.
package routing

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// RollbackOnly wraps next so that the Runner's pull loop over the rollback
// subject (topology.RollbackConsumerConfig) runs only rollbacks: a
// dispatch arriving there with no rollback steps is refused, never run as
// the ordinary dispatch of the runbook it names, which is the one thing a
// rollback must never turn into. The refusal wraps ErrNoAdapter, so the
// Runner reports it and drops it rather than retrying it.
func RollbackOnly(next Executor) Executor {
	return rollbackOnly{next: next}
}

// rollbackOnly is RollbackOnly's Executor.
type rollbackOnly struct {
	next Executor
}

// Execute runs payload when it is a rollback.
func (r rollbackOnly) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	if payload.Rollback == nil {
		return wire.Outcome{}, fmt.Errorf("%w: a dispatch on the rollback subject carries no rollback, and is not run as an ordinary one", ErrNoAdapter)
	}
	return r.next.Execute(ctx, payload)
}

// Package sdk: whether an earlier task in this run's check predicted a change
// on the device, so a later task's check, which reads the device as it is,
// knows it may be judging a state the real run will not meet (LESSONS 255,
// 307; FAILURE_PATTERNS 564, 722, 739).
package sdk

import "context"

// EarlierChanges is implemented by a RunbookContext that knows whether an
// earlier task checked in this run, on the same device, predicted a change,
// could not be checked, or failed: in each case the real run may leave the
// device unlike what this check reads. Only a built-in method's context
// answers it; an external Collection program's never does.
type EarlierChanges interface {
	// EarlierChangePredicted reports whether such an earlier task exists.
	EarlierChangePredicted() bool
}

// EarlierChangePredicted reports whether rc says an earlier checked task on
// this device predicted a change, could not be checked, or failed: false for
// a context that does not say.
func EarlierChangePredicted(rc RunbookContext) bool {
	e, ok := rc.(EarlierChanges)
	return ok && e.EarlierChangePredicted()
}

// earlierKey marks a context carrying the earlier-change answer.
type earlierKey struct{}

// WithEarlierChange returns ctx carrying earlier, the answer the engine hands
// the check it makes under ctx, for whatever builds that call's RunbookContext
// to pass on. It stays in this process and crosses no child boundary.
func WithEarlierChange(ctx context.Context, earlier bool) context.Context {
	return context.WithValue(ctx, earlierKey{}, earlier)
}

// EarlierChangeFrom returns the answer WithEarlierChange put on ctx, false
// when there is none.
func EarlierChangeFrom(ctx context.Context) bool {
	earlier, _ := ctx.Value(earlierKey{}).(bool)
	return earlier
}

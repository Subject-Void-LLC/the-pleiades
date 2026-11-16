// Package engine: per device, whether a task checked earlier in this run
// predicted a change, could not be checked, or failed (sdk.EarlierChanges).
//
// A check reads each device as it is, so a later task whose outcome depends on
// an earlier task's predicted change is judged against a state the real run
// will not meet (FAILURE_PATTERNS 564, 722, 739). The predictions ledger
// (predicted.go) answers that for removed paths only, and only while every
// task records its paths; this answers the coarser question for every
// method: has anything checked on this device so far left it possibly unlike
// what a check now reads? It is written only at the level barrier, so a
// sibling in the same level never counts as earlier, and it is lent only to a
// built-in method's context.
package engine

import (
	"sync"
)

// earlierChanges is the set of devices an earlier checked task left
// possibly changed.
type earlierChanges struct {
	mu      sync.Mutex
	devices map[string]bool
}

// newEarlierChanges returns an empty set.
func newEarlierChanges() *earlierChanges {
	return &earlierChanges{devices: map[string]bool{}}
}

// record adds the devices one level's results leave possibly unlike what a
// check reads: a checked task that predicted a change, one that could not be
// checked, and one that failed (which a run goes on past only when its author
// lets it fail). A real task's change is on the device, which a later check
// reads as it is, so it adds nothing.
func (e *earlierChanges) record(results []NodeResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, nr := range results {
		if nr.Device == "" {
			continue
		}
		if nr.Unchecked || nr.Checked && (nr.Changed || nr.Err != nil) {
			e.devices[nr.Device] = true
		}
	}
}

// on reports whether device is in the set.
func (e *earlierChanges) on(device string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.devices[device]
}

// EarlierChangePredicted implements sdk.EarlierChanges.
func (c *runbookContext) EarlierChangePredicted() bool { return c.earlierChange }

// Hooks for this package's external tests.
package capture

import "time"

// SetAwaitPoll sets how often a stop reads the file's length while it
// waits, returning a function that restores it.
func SetAwaitPoll(d time.Duration) func() {
	old := awaitPoll
	awaitPoll = d
	return func() { awaitPoll = old }
}

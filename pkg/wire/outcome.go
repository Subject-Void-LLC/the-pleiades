// Package wire: Outcome, what one execution reports besides whether it
// failed.
package wire

// Outcome is what an execution adapter reports about a run that did not
// fail. A failure is the adapter's error; everything a run that finished
// has to say beyond "it finished" is here.
//
// It exists for a check. A check that could not answer for some of its
// tasks has not failed, and reporting it as a failure would be a lie in
// the other direction, but a check reported as plain success would read,
// to anyone polling the job, like a check that covered everything.
type Outcome struct {
	// Unchecked is how many tasks a check could not check, and zero for a
	// real run and for a check that checked every task.
	Unchecked int
}

// A read that ended before its prompt, keeping what the device printed.
package netcli

import (
	"errors"
	"fmt"
)

// ReadFailure is a read that ended before the prompt it waited for (the
// context's deadline, the session closing). Its text names the cause, as
// every netcli read error does, and never the output: Output is what the
// device printed before the read ended, for a caller to show only after
// masking it, since output read partway through a configuration can hold a
// secret. A timed-out command whose last line explains it (a question nobody
// answered, a prompt this dialect does not know) is then answerable from the
// failure alone (FAILURE_PATTERNS 651).
type ReadFailure struct {
	// Err is why the read ended.
	Err error
	// Output is everything read for the command before it ended, unmasked.
	Output string
	// suffix is the question the read stopped on, when there was one
	// (waitingOn), already safe to print.
	suffix string
}

// Error names the cause and, when the read stopped on a question, the
// question; never the output.
func (e *ReadFailure) Error() string {
	return fmt.Sprintf("netcli: %v%s", e.Err, e.suffix)
}

// Unwrap is the cause.
func (e *ReadFailure) Unwrap() error { return e.Err }

// LastOutput returns what a failed read had printed, when err is a
// ReadFailure, unmasked: the caller masks it before showing it.
func LastOutput(err error) (string, bool) {
	var rf *ReadFailure
	if errors.As(err, &rf) && rf.Output != "" {
		return rf.Output, true
	}
	return "", false
}

// readFailure is err from a read as a ReadFailure, with the partial output
// the shell kept and what was read before it.
func readFailure(err error, before string, suffix string) *ReadFailure {
	var partial []byte
	var re partialReader
	if errors.As(err, &re) {
		partial = re.PartialRead()
	}
	return &ReadFailure{Err: err, Output: before + string(partial), suffix: suffix}
}

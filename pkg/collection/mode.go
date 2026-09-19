// Execution modes, and the rules that keep a method's check support
// honest.
//
// A run either changes things (ModeExecute) or reports what it would
// change without changing anything (ModeCheck). The second is the dry run
// Ansible calls check mode. It lives in this package, rather than in the
// engine that walks a runbook, because the difference reaches all the way
// down to which function a Descriptor hands out: Invoke for one mode,
// Check for the other. A third-party Collection sees the mode only that
// way, as which of its two functions was called, so it never needs to
// import anything to know it.
//
// A method declares check support itself. Nothing infers it from a
// method's name, its namespace, or its parameters, because the whole
// value of a check is that it was asked of code that knows how to answer
// it. A method that has not declared support is reported as one that
// could not be checked, by name, and never as one that would have
// succeeded.
package collection

import "fmt"

// Mode is how a run asks a Collection method to act. It is a closed set:
// ParseMode refuses anything else, so a typo on a command line can never
// quietly become the mode that changes devices.
type Mode string

const (
	// ModeExecute applies changes. It is the default, and the only mode
	// that ever writes to a device.
	ModeExecute Mode = "execute"

	// ModeCheck contacts devices and reports what a run would change,
	// applying nothing. It maps to Ansible's check mode.
	ModeCheck Mode = "check"
)

// ParseMode is the inverse of a Mode's string form. An empty string is
// ModeExecute, since that is what a caller that never mentioned a mode
// has always meant. Any other unknown value is an error rather than a
// default: a misspelled "chekc" must not run for real.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "", ModeExecute:
		return ModeExecute, nil
	case ModeCheck:
		return ModeCheck, nil
	default:
		return "", fmt.Errorf("unknown mode %q: use %q or %q", s, ModeExecute, ModeCheck)
	}
}

// MethodFor returns the function that runs d in mode m.
//
// It is the one place that maps a mode to a function, shared by the
// engine's in-process dispatch and by every child process that runs a
// method across a process boundary, so the two cannot disagree about
// which body a check reaches.
//
// A method with no check support returns an error naming it for
// ModeCheck. That is not a failure of the method: it is the method's own
// declared answer, and a caller reports it as "could not be checked"
// rather than as a failed task.
func (d Descriptor) MethodFor(m Mode) (Method, error) {
	switch m {
	case ModeExecute:
		if d.Invoke == nil {
			return nil, fmt.Errorf("collection method %q carries no implementation", d.Name)
		}
		return d.Invoke, nil
	case ModeCheck:
		if !d.Manifest.SupportsCheck || d.Check == nil {
			return nil, fmt.Errorf("collection method %q does not support check mode", d.Name)
		}
		return d.Check, nil
	default:
		return nil, fmt.Errorf("collection method %q: unknown mode %q", d.Name, m)
	}
}

// NoCheckAnswer is why a method with no check support cannot be checked,
// as the engine and validation both say it: the method's own
// NoCheckReason when it gives one, and the bare fact otherwise.
func (d Descriptor) NoCheckAnswer() string {
	if d.Manifest.NoCheckReason != "" {
		return d.Manifest.NoCheckReason
	}
	return "it does not declare check support"
}

// checkCheckSupport rejects a check declaration that contradicts itself.
//
// Manifest.SupportsCheck is the serialized answer, the one a reference
// page prints and a loader reads from a third-party Collection's own
// description. Descriptor.Check is the function behind it. They are two
// fields that could disagree, which is exactly the drift Descriptor's own
// doc comment warns about, so the disagreement is refused here the same
// way a StatusImplemented method with no Invoke is: at process start,
// rather than as a surprise halfway through a run.
func checkCheckSupport(d Descriptor) error {
	switch {
	case d.Manifest.SupportsCheck && d.Check == nil:
		return fmt.Errorf("collection: %q declares check support but carries no Check function", d.Name)
	case !d.Manifest.SupportsCheck && d.Check != nil:
		return fmt.Errorf("collection: %q carries a Check function but its manifest does not declare check support", d.Name)
	case d.Manifest.SupportsCheck && d.Manifest.Status != StatusImplemented:
		// A declared stub has no behavior to predict. Claiming it can say
		// what it would change is a claim about code nobody has written.
		return fmt.Errorf("collection: %q declares check support but is not implemented", d.Name)
	case d.Manifest.SupportsCheck && d.Manifest.NoCheckReason != "":
		return fmt.Errorf("collection: %q declares check support and also a reason it cannot be checked", d.Name)
	case d.CheckCall != nil && !d.Manifest.SupportsCheck:
		return fmt.Errorf("collection: %q carries a CheckCall function but its manifest does not declare check support", d.Name)
	}
	return nil
}

// CannotCheckError is a Check's answer for one call it cannot check: the
// method supports check mode, but not with these parameters. exec.command
// is the shape it exists for: with creates or removes set it can say
// whether it would run, and without either it cannot know without running.
//
// Return it (through CannotCheck) rather than a plain error, and the task
// is reported as unchecked, naming Reason, where a plain error would fail
// the check. It is honored only in check mode; returned from Invoke it is
// an ordinary failure. It crosses a process boundary as its own flag
// (wire.ChildResponse.CannotCheck), since an error value does not.
type CannotCheckError struct {
	// Reason says why this call cannot be checked, in words an operator
	// can act on.
	Reason string
}

// Error implements error.
func (e *CannotCheckError) Error() string {
	return "this call cannot be checked: " + e.Reason
}

// CannotCheck returns a Check's "I cannot check this call" answer, with
// reason saying why. See CannotCheckError.
func CannotCheck(reason string) error {
	return &CannotCheckError{Reason: reason}
}

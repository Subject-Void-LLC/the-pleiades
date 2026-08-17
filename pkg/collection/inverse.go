// Coherence rules for a Manifest's declared Reversibility, checked at
// registration.
//
// There is much less to check here than there was under the previous
// design, and that is the point rather than a loss. That design put a
// whole inverse on the manifest (an FQCN, the prior-state keys a rollback
// would feed it) and this file checked the shape of it: that a named
// method was not the method itself, that captures were not declared by a
// read-only method, and so on. All of that was checking the internal
// consistency of a claim that could not be right in the first place,
// because the true inverse depends on what a run found rather than on
// what the method is.
//
// What is left is the one thing a manifest can honestly say and a
// registration can honestly enforce: an implemented method must answer
// whether it can ever undo itself, and a method answering "no" must say
// why. Everything else moved to run time, where the answer exists.
//
// Enforcing even this much matters. RequiredCapabilities on this same
// Manifest is read by no run-time gate at all (FAILURE_PATTERNS.md #151),
// and the lesson recorded there is that a declared constraint with no
// enforcement becomes a comment and is then cited as a guarantee.
package collection

import "fmt"

// checkReversibility rejects an incoherent Reversibility declaration.
//
// It applies only to an implemented method. A declared stub has no
// behavior to undo yet, and forcing all sixty-odd stubs to answer a
// question about code nobody has written would produce a table of
// guesses, which is worse than an empty field because it would look like
// an answer.
func checkReversibility(d Descriptor) error {
	if d.Manifest.Status != StatusImplemented {
		return nil
	}

	// A method that cannot be undone has to say why. This is the one place
	// a bare declaration is worth refusing: "this cannot be undone" is the
	// answer an operator most needs a reason for, and it is also the
	// easiest answer to reach for when the real one is "working out the
	// inverse looked like effort."
	if !d.Manifest.Reversibility.Reversible && d.Manifest.Reversibility.Notes == "" {
		return fmt.Errorf("collection: %q declares itself not reversible with no Notes explaining why: "+
			"say what about its effect this platform cannot observe or reconstruct", d.Name)
	}

	return nil
}

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

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

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

	if err := ValidateReversibility(d.Manifest.Reversibility); err != nil {
		return fmt.Errorf("collection: %q %w", d.Name, err)
	}
	return nil
}

// ValidateReversibility refuses a Reversibility that contradicts itself.
// Register calls it for an implemented method, and the external Collection
// loader calls it for each method a program describes, so the two cannot
// disagree. It reads the declaration alone: whether each Inverses target
// is registered, and declares the parameters named, is checked across the
// whole registry once every method is in it (internal/archtest), since a
// target may register after the method naming it.
//
// Its errors read after a method's name ("... declares itself ...").
func ValidateReversibility(r Reversibility) error {
	// A method that cannot be undone has to say why. This is the one place
	// a bare declaration is worth refusing: "this cannot be undone" is the
	// answer an operator most needs a reason for, and it is also the
	// easiest answer to reach for when the real one is "working out the
	// inverse looked like effort."
	if !r.Reversible && r.Notes == "" {
		return fmt.Errorf("declares itself not reversible with no Notes explaining why: " +
			"say what about its effect this platform cannot observe or reconstruct")
	}
	if r.ReadOnly && (r.Reversible || len(r.Inverses) > 0) {
		return fmt.Errorf("declares itself read-only and also something to undo; a method that changes nothing has nothing to undo")
	}
	if len(r.Inverses) > 0 && !r.Reversible {
		return fmt.Errorf("lists undo methods but declares itself not reversible")
	}
	seen := map[string]bool{}
	for _, spec := range r.Inverses {
		if err := validateInverseSpec(spec); err != nil {
			return fmt.Errorf("lists an undo through %q that %w", spec.FQCN, err)
		}
		if seen[spec.FQCN] {
			return fmt.Errorf("lists an undo through %q twice", spec.FQCN)
		}
		seen[spec.FQCN] = true
	}
	return nil
}

// validateInverseSpec refuses one undo declaration that could not be
// honored as written. Its errors read after "an undo through <fqcn> that".
func validateInverseSpec(spec sdk.InverseSpec) error {
	if namespace, method, ok := strings.Cut(spec.FQCN, "."); !ok || namespace == "" || method == "" {
		return fmt.Errorf("is not a namespaced method name")
	}
	names := map[string]string{}
	for list, params := range map[string][]string{"records": spec.Record, "withholds": spec.Withhold} {
		for _, name := range params {
			if name == "" {
				return fmt.Errorf("%s an empty parameter name", list)
			}
			if other, dup := names[name]; dup {
				return fmt.Errorf("%s %q, which it also %s", list, name, other)
			}
			names[name] = list
			// The engine reads target as the device to run on, so a
			// recorded target would move a replayed undo to another device.
			if IsReservedParam(name) {
				return fmt.Errorf("%s %q, which the engine reads as the device or tag a task runs on", list, name)
			}
		}
	}
	// An undo never inherits the forward task's choice to skip host key
	// verification: a replayed undo checks the host key like any task.
	if slices.Contains(spec.Record, sdk.ParamInsecureSkipHostKeyVerify) {
		return fmt.Errorf("records %q; an undo checks the device's host key whatever the task it undoes chose", sdk.ParamInsecureSkipHostKeyVerify)
	}
	return nil
}

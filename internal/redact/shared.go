package redact

import "sync"

// Shared returns the one Masker this process uses.
//
// # Why a process-wide instance, in a codebase built around composition roots
//
// This platform wires concrete implementations in cmd/ precisely to avoid
// globals, so a package-level instance needs a reason rather than a
// convenience. Masking has one, and it is not the usual "it is easier":
//
//   - There is nothing to configure. The ruleset is embedded at build time
//     and immutable, so two Maskers would differ in exactly one way, which
//     is the next point.
//   - A second instance would carry a second Literals set. A secret
//     registered with one and logged through the other would not be masked,
//     and the failure would be silent, one-directional, and permanent, since
//     anything already leaked is durable. Masking is the case where
//     "everything must agree" is the whole requirement rather than a
//     preference.
//   - Every terminal writer must carry the same rules, including the ones a
//     failure path reaches. A model where a writer can be constructed
//     without one is the model where the failure path is the one that
//     misses.
//
// The wiring stays visible where it matters: a composition root still
// installs this explicitly into its slog handler and its log output, and
// internal/archtest fails the build if one does not. What is global is the
// ruleset, not the decision to apply it.
//
// NewMasker remains exported for a caller that genuinely wants an isolated
// ruleset, which today means tests.
func Shared() *Masker { return sharedOnce() }

// sharedOnce builds the process-wide Masker exactly once.
//
// It panics on a failure for the same reason DefaultRuleset does: the
// ruleset is compiled into the binary, so a failure here is a build defect
// every process would hit identically at startup rather than a runtime
// condition a caller could do anything about.
var sharedOnce = sync.OnceValue(func() *Masker {
	m, err := NewMasker(DefaultRuleset())
	if err != nil {
		panic("redact: the embedded ruleset cannot be compiled: " + err.Error())
	}
	return m
})

// Text masks free text against the shared ruleset.
//
// secrets are values this caller already knows are secret, which is the
// common case: an executor holds the credential it just used, an adapter
// holds the payload it just dispatched. They are masked alongside whatever
// is in the shared Literals set, and the shared pattern rules apply to
// both.
//
// This is the entry point for code that has an explicit secret list. The
// logging seam has no such list, which is what Masker.Attr and
// Masker.Writer are for, and both read the same shared state, so the two
// entry points cannot disagree about what is secret.
//
// This function replaced credential.Mask, which was deleted rather than
// left as a delegate: PLAN.md Section 25 allows the masking ruleset one
// implementation, and two names for one thing is that duplication under a
// different spelling.
func Text(secrets []string, text string) string {
	return Shared().Text(secrets, text)
}

package redact

import (
	"sort"
	"strings"
)

// The by-value masking channel: a substring scrub over exact secret values
// this process knows the bytes of.
//
// This algorithm was written for Phase 16 and lived in
// internal/credential/mask.go as credential.Mask until Phase 22 moved it
// here. It is relocated verbatim, doc comment included, rather than
// rewritten. PLAN.md Section 25 allows the masking ruleset exactly one
// implementation, and the choice was between this tested one and a new one;
// a new one would have had to rediscover the asterisk-edge exception below,
// which was found the hard way. credential.Mask no longer exists: a
// surviving delegation would have been two names for one thing, which is
// the duplication this section forbids under a different spelling.

// maskPlaceholder replaces every masked secret occurrence in maskLiterals'
// output, regardless of the length of the secret it stands in for.
// Using a fixed placeholder instead of one sized to the secret is
// deliberate: a variable-length placeholder would leak the secret's
// length to whoever reads the masked output, which is exactly the kind
// of side channel this function exists to close.
const maskPlaceholder = "********"

// maskLiterals returns a copy of text with every non-empty string in
// secrets replaced by maskPlaceholder wherever it appears as a substring.
// It exists so that command output which happens to echo a password or key
// back does not leak it into logs or an ActionResult's captured output.
//
// Two rules make the result deterministic and safe:
//
//  1. Empty strings in secrets are skipped entirely. Masking "" would
//     match the zero-length gap between every pair of characters in
//     text, inserting a placeholder after every byte instead of doing
//     nothing. An empty secret means "this credential field was never
//     set," which is not a value to protect, so skipping it is the only
//     sane behavior.
//  2. Overlapping secrets are masked longest first. If one secret is a
//     substring of another (for example a password "abc" and a
//     passphrase "abcdef"), masking the shorter one first would replace
//     only the "abc" prefix and leave "def" exposed right next to the
//     placeholder. Masking longest-first always consumes the full longer
//     secret before a shorter one gets a chance to carve into it.
//
// All matching happens against the original text before any placeholder
// is inserted: this never re-scans its own output for further matches.
// That means a secret equal to "*" or equal to maskPlaceholder itself
// can only match real occurrences already present in the input text, not
// occurrences of maskPlaceholder that were just produced here.
//
// There is one documented exception to "the output never contains the
// secret," and it is broader than just "the secret is all asterisks":
// no separator is inserted between a placeholder and the original text
// immediately before or after it, so if a secret starts or ends with an
// asterisk, the placeholder's own boundary asterisks can combine with
// adjacent, unrelated leftover text to accidentally spell the secret
// back out. For example, masking the secret `*"` inside the text `*""`
// claims only the first two characters (the actual match), leaving the
// trailing `"` untouched; the output is maskPlaceholder followed by that
// leftover `"`, i.e. `********"`, whose last nine characters happen to
// read `*"` again, purely because the placeholder ends in `*` and the
// leftover character happens to continue the pattern. This can only
// happen at a placeholder's edge, since every placeholder is a uniform
// run of eight `*` characters: a secret with a non-asterisk character on
// both sides of its single interior asterisk (for example "a*b") can
// never be reconstructed this way, because no single contiguous window
// of the output can enter a placeholder's uniform interior and exit it
// again within the span of one secret unless every character it crosses
// is itself `*`. Concretely: the exception applies exactly when secret
// has a leading or trailing '*' (a secret made solely of asterisks is
// simply the case where both ends qualify); every real occurrence of such
// a secret is still fully replaced (so its exact length and
// position in the original text are hidden), but "the output contains no
// substring equal to the secret" is not a claim this function can make
// for that shape of secret. Every other secret is not subject to this
// exception.
//
// maskLiterals never panics, regardless of nil or empty secrets, nil or
// empty text, or secrets that repeat or overlap in any way.
func maskLiterals(secrets []string, text string) string {
	if text == "" || len(secrets) == 0 {
		return text
	}

	// The short circuit, added in Phase 22 and worth its own explanation
	// because it changes nothing about the result and a great deal about
	// the cost.
	//
	// Almost every string handed to this function contains no secret at
	// all: it is an ordinary log line in a process that happens to hold
	// live credentials. The work below (sorting the secret list, allocating
	// a claim table the length of the text, building a new string) is all
	// wasted on that case, and it was being paid on every log line of every
	// binary. Scanning first costs the same string searches the claim pass
	// would have done anyway, allocates nothing, and returns the original
	// string unchanged when there is nothing to do.
	if !anySecretPresent(secrets, text) {
		return text
	}

	nonEmpty := nonEmptySortedLongestFirst(secrets)
	if len(nonEmpty) == 0 {
		return text
	}

	// claimed[i] is true once byte offset i in text has been assigned to
	// a matched secret. Matching always happens against the original
	// text, never against previously inserted placeholders (see the
	// doc comment above), so a short secret can never accidentally
	// match inside a placeholder a longer secret already produced.
	claimed := make([]bool, len(text))
	for _, secret := range nonEmpty {
		claimSecretOccurrences(claimed, text, secret)
	}

	return buildMasked(claimed, text)
}

// anySecretPresent reports whether any non-empty secret occurs in text.
//
// Empty secrets are skipped here for the same reason maskLiterals skips
// them: strings.Contains reports true for the empty string against any
// text, so counting one would defeat the short circuit entirely and make
// every call take the slow path.
func anySecretPresent(secrets []string, text string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			return true
		}
	}
	return false
}

// nonEmptySortedLongestFirst returns the non-empty entries of secrets,
// sorted longest-first (rule 2 of maskLiterals' doc comment). Ties break on
// the string value itself so the result does not depend on the caller's
// input order when two secrets share a length, keeping the output
// deterministic for a given set of secrets regardless of how the caller
// happened to list them.
func nonEmptySortedLongestFirst(secrets []string) []string {
	nonEmpty := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	sort.Slice(nonEmpty, func(i, j int) bool {
		if len(nonEmpty[i]) != len(nonEmpty[j]) {
			return len(nonEmpty[i]) > len(nonEmpty[j])
		}
		return nonEmpty[i] < nonEmpty[j]
	})
	return nonEmpty
}

// claimSecretOccurrences scans text left to right for non-overlapping
// occurrences of secret and marks their byte ranges as claimed. A byte
// already claimed by an earlier (longer, since the caller sorts
// longest-first) secret is left alone: this is what stops a shorter secret
// from re-claiming, and thus visually splitting, a span a longer secret
// already fully covers.
func claimSecretOccurrences(claimed []bool, text, secret string) {
	start := 0
	for {
		idx := strings.Index(text[start:], secret)
		if idx == -1 {
			return
		}
		matchStart := start + idx
		matchEnd := matchStart + len(secret)

		// Only claim this occurrence if none of its bytes are already
		// claimed by a longer secret processed earlier. A partially
		// claimed match is left as-is rather than partially masked,
		// since the already-claimed bytes are covered by that earlier
		// secret's own placeholder anyway.
		if !anyClaimed(claimed[matchStart:matchEnd]) {
			for i := matchStart; i < matchEnd; i++ {
				claimed[i] = true
			}
		}

		// Advance past this whole occurrence, not just past its first
		// byte, so repeated non-overlapping occurrences of the same
		// secret (for example "aa" within "aaaa") are each found once.
		start = matchEnd
	}
}

// anyClaimed reports whether any byte in the given slice is already
// claimed by a previously processed (longer) secret.
func anyClaimed(claimed []bool) bool {
	for _, c := range claimed {
		if c {
			return true
		}
	}
	return false
}

// buildMasked walks text once, copying unclaimed bytes through unchanged
// and emitting exactly one maskPlaceholder for each contiguous run of
// claimed bytes, regardless of that run's length. Collapsing a whole
// contiguous run into a single placeholder (rather than one placeholder
// per matched secret) is what keeps adjacent or back-to-back secret
// occurrences from producing a visually confusing string of repeated
// placeholders.
func buildMasked(claimed []bool, text string) string {
	var b strings.Builder
	b.Grow(len(text))
	i := 0
	for i < len(text) {
		if !claimed[i] {
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteString(maskPlaceholder)
		for i < len(text) && claimed[i] {
			i++
		}
	}
	return b.String()
}

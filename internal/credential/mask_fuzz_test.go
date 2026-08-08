package credential_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// FuzzMask fuzzes Mask with a single (secret, text) pair, asserting the
// two invariants Mask's doc comment promises: it never panics, and
// (except for the documented leading/trailing-asterisk exception, see
// mask.go) if secret is non-empty and actually occurs in text, the
// masked output never contains secret as a substring.
func FuzzMask(f *testing.F) {
	f.Add("", "")
	f.Add("secret", "the secret is secret")
	f.Add("abc", "abcdef")
	f.Add("*", "pass=*** and more")
	f.Add("**", "****")
	f.Add("********", "********")
	f.Add("a", "aaaaaaaaaa")
	f.Add("", "some text with no secrets")
	f.Add("tok", "tok-tok-tok")
	// A middle-asterisk secret: never subject to the boundary exception
	// (see mask.go's doc comment for why), so this must always hold.
	f.Add("a*b", "xa*by")
	// The exact placeholder-boundary reconstruction this test's exception
	// carve-out exists for: masking `*"` inside `*""` leaves a trailing
	// `"` right after the placeholder, whose last `*` and that leftover
	// `"` spell the secret back out. See TestMask_LeadingAsteriskBoundaryException
	// (mask_test.go) for the same shape as a permanent, named regression.
	f.Add("*\"", "*\"\"")

	f.Fuzz(func(t *testing.T, secret, text string) {
		// Mask must never panic on any input; capturing that here (in
		// addition to Go's fuzzer already treating a panic as a
		// failure) gives a clearer failure message with the exact
		// inputs that triggered it.
		var out string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Mask panicked on secret=%q text=%q: %v", secret, text, r)
				}
			}()
			out = credential.Mask([]string{secret}, text)
		}()

		if secret == "" {
			if out != text {
				t.Fatalf("empty secret must be a no-op: got %q, want %q", out, text)
			}
			return
		}

		if !strings.Contains(text, secret) {
			return
		}

		// Documented exception (mask.go): a secret starting or ending
		// with '*' can be reconstructed across a placeholder's boundary
		// with adjacent, unrelated leftover text, since the placeholder
		// itself is built from '*' and Mask inserts no separator. Skip
		// the containment assertion for this shape of secret; every
		// other secret must still satisfy the invariant.
		if hasAsteriskBoundary(secret) {
			return
		}

		if strings.Contains(out, secret) {
			t.Fatalf("masked output still contains the secret: secret=%q text=%q out=%q", secret, text, out)
		}
	})
}

// hasAsteriskBoundary reports whether s starts or ends with '*'. s is
// assumed non-empty by FuzzMask's caller.
func hasAsteriskBoundary(s string) bool {
	return strings.HasPrefix(s, "*") || strings.HasSuffix(s, "*")
}

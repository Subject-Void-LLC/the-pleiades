package filters_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzSafeInt asserts SafeInt's real invariant, not just absence of a
// panic: the result is always either the caller's fallback or exactly
// what strconv.Atoi parses from the trimmed input. Any input that makes
// the two disagree is a bug in SafeInt, not a fuzzing false positive.
func FuzzSafeInt(f *testing.F) {
	f.Add("42", -1)
	f.Add("", -1)
	f.Add("-7", 0)
	f.Add("abc", 99)
	f.Add("3.14", 99)
	f.Add("99999999999999999999999999", 99)
	f.Add("  42 \r\n", -1)

	f.Fuzz(func(t *testing.T, in string, fallback int) {
		got := filters.SafeInt(in, fallback)

		if len(in) > filters.MaxInputBytes {
			if got != fallback {
				t.Fatalf("SafeInt(%q, %d): over MaxInputBytes but returned %d, not fallback", in, fallback, got)
			}
			return
		}

		trimmed := trimForTest(in)
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			if got != fallback {
				t.Fatalf("SafeInt(%q, %d) = %d, want fallback %d (strconv.Atoi: %v)", in, fallback, got, fallback, err)
			}
			return
		}
		if got != n {
			t.Fatalf("SafeInt(%q, %d) = %d, want parsed value %d", in, fallback, got, n)
		}
	})
}

// FuzzSafeFloat mirrors FuzzSafeInt's invariant for SafeFloat: the result
// is always either fallback or exactly what strconv.ParseFloat parses,
// except that a parsed NaN or infinity must also map to fallback.
func FuzzSafeFloat(f *testing.F) {
	f.Add("3.14", -1.0)
	f.Add("", -1.0)
	f.Add("abc", 9.9)
	f.Add("NaN", 9.9)
	f.Add("Inf", 9.9)
	f.Add("-Infinity", 9.9)
	f.Add("1.5e300", 9.9)

	f.Fuzz(func(t *testing.T, in string, fallback float64) {
		got := filters.SafeFloat(in, fallback)

		if len(in) > filters.MaxInputBytes {
			if got != fallback && !(math.IsNaN(got) && math.IsNaN(fallback)) {
				t.Fatalf("SafeFloat(%q, %v): over MaxInputBytes but returned %v, not fallback", in, fallback, got)
			}
			return
		}

		trimmed := trimForTest(in)
		x, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
			if got != fallback && !(math.IsNaN(got) && math.IsNaN(fallback)) {
				t.Fatalf("SafeFloat(%q, %v) = %v, want fallback %v", in, fallback, got, fallback)
			}
			return
		}
		if got != x {
			t.Fatalf("SafeFloat(%q, %v) = %v, want parsed value %v", in, fallback, got, x)
		}
	})
}

// FuzzSafeBool asserts SafeBool never panics and always returns a value
// drawn from its own documented vocabulary or the caller's fallback, never
// anything else (there is nothing else a bool could be, but this also
// pins down that a recognized spelling is never silently mapped to the
// fallback and vice versa, by re-deriving the expected outcome from the
// same table the implementation and the table test both use).
func FuzzSafeBool(f *testing.F) {
	f.Add("true", false)
	f.Add("false", true)
	f.Add("yes", false)
	f.Add("no", true)
	f.Add("on", false)
	f.Add("off", true)
	f.Add("", true)
	f.Add("enabled", true)
	f.Add("1", false)
	f.Add("0", true)

	f.Fuzz(func(t *testing.T, in string, fallback bool) {
		got := filters.SafeBool(in, fallback)

		if len(in) > filters.MaxInputBytes {
			if got != fallback {
				t.Fatalf("SafeBool(%q, %v): over MaxInputBytes but returned %v, not fallback", in, fallback, got)
			}
			return
		}

		want, recognized := boolVocabulary(trimForTest(in))
		if !recognized {
			if got != fallback {
				t.Fatalf("SafeBool(%q, %v) = %v, want fallback %v (unrecognized spelling)", in, fallback, got, fallback)
			}
			return
		}
		if got != want {
			t.Fatalf("SafeBool(%q, %v) = %v, want %v", in, fallback, got, want)
		}
	})
}

// trimForTest mirrors the whitespace trimming SafeInt/SafeFloat/SafeBool
// each apply before parsing, kept as a single helper so the three fuzz
// targets above stay in lockstep with the implementation's own
// strings.TrimSpace call rather than each re-deriving it slightly
// differently.
func trimForTest(s string) string {
	return trimSpaceLikeImplementation(s)
}

// This file holds revealed and percentDecoded to their contract directly:
// what they decode, what they leave, and that text which keeps changing is
// reported as not settled rather than read forever.
package project

import (
	"strings"
	"testing"
)

// TestPercentDecoded_DecodesOnlyWellFormedEscapes is the lenient decode: a
// bad escape is left as written and does not stop the good ones beside it.
func TestPercentDecoded_DecodesOnlyWellFormedEscapes(t *testing.T) {
	cases := map[string]string{
		"plain":          "plain",
		"a%3Fb":          "a?b",
		"a%3fb":          "a?b",
		"%zz%3F":         "%zz?",
		"trailing%":      "trailing%",
		"trailing%4":     "trailing%4",
		"%2540":          "%40",
		"%e2%81%87":      string(rune(0x2047)),
		"100%25 done%21": "100% done!",
	}
	for in, want := range cases {
		if got := percentDecoded(in); got != want {
			t.Errorf("percentDecoded(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRevealed_SettlesOrSaysItDidNot covers a plain URL (unchanged, settled),
// a layered encoding within the bound (read through to "?"), and one past
// the bound (not settled, which every caller refuses).
func TestRevealed_SettlesOrSaysItDidNot(t *testing.T) {
	if got, settled := revealed("https://git.example.invalid/r.git"); !settled || got != "https://git.example.invalid/r.git" {
		t.Errorf("revealed(plain) = %q, %v", got, settled)
	}
	if got, settled := revealed("r%25253F"); !settled || got != "r?" {
		t.Errorf("revealed(three layers) = %q, %v, want r? settled", got, settled)
	}
	if _, settled := revealed("r%" + strings.Repeat("25", revealRounds+1) + "3F"); settled {
		t.Error("revealed of more layers than revealRounds reported settled")
	}
	if !hasQueryOrFragment("r%" + strings.Repeat("25", revealRounds+1) + "3F") {
		t.Error("hasQueryOrFragment of text that did not settle = false, want it refused")
	}
	if hasAtLookalike("git@host.example.invalid:team/repo.git") {
		t.Error("hasAtLookalike of a written @ = true, want only an unwritten one flagged")
	}
}

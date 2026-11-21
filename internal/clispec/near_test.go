// Tests of NearSubcommand: a word one edit from a subcommand names it, and
// the subcommand itself, or a word further off, names none.
package clispec

import "testing"

// TestNearSubcommand names facts' subcommand a word is one edit from.
func TestNearSubcommand(t *testing.T) {
	facts, ok := Find(Root, "facts")
	if !ok {
		t.Fatal("no facts command")
	}
	for word, want := range map[string]string{
		"baselin":   "baseline",
		"baselines": "baseline",
		"bsaeline":  "baseline",
		"bseline":   "baseline",
		"bslne":     "",
		"baesline":  "baseline",
		"drfit":     "drift",
		"drif":      "drift",
		"draft":     "drift",
		"drift":     "",
		"baseline":  "",
		"web1":      "",
		"":          "",
		"d":         "",
	} {
		got, ok := NearSubcommand(facts, word)
		if got != want || ok != (want != "") {
			t.Errorf("NearSubcommand(facts, %q) = %q, %v; want %q", word, got, ok, want)
		}
	}
}

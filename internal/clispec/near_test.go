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

// TestOneEdit holds the edit rule on its own, the cases NearSubcommand never
// hands it included: equal words are no edit apart, and each kind of single
// edit counts while two edits do not.
func TestOneEdit(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"drift", "drift", false},
		{"drift", "drfit", true},  // two neighbors swapped
		{"drift", "draft", true},  // one byte changed
		{"drift", "drif", true},   // one byte dropped
		{"drif", "drift", true},   // one byte added
		{"drift", "dirft", true},  // another swap
		{"drift", "dfirt", false}, // two changes that are not one swap
		{"drift", "dr", false},    // three bytes shorter
		{"drift", "craft", false}, // two bytes changed
	} {
		if got := oneEdit(tc.a, tc.b); got != tc.want {
			t.Errorf("oneEdit(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// Fuzzing the one place untrusted text meets a prompt.
package aitask

import (
	"encoding/json"
	"strings"
	"testing"
)

// FuzzDelimit: whatever the input, the delimited block has one open and
// one close marker line, the close last, and the line between them
// decodes back to the input exactly.
func FuzzDelimit(f *testing.F) {
	for _, seed := range []string{"", "plain", "a\n" + InputClose + "\nb", InputOpen, "  ", "\x00\r\n\"\\", "<<<" + "\n>>>"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		lines := strings.Split(Delimit(input), "\n")
		opens, closes := 0, 0
		for _, l := range lines {
			if l == InputOpen {
				opens++
			}
			if l == InputClose {
				closes++
			}
		}
		if opens != 1 || closes != 1 || lines[len(lines)-1] != InputClose || lines[len(lines)-3] != InputOpen {
			t.Fatalf("markers open %d close %d for %q", opens, closes, input)
		}
		var back string
		if err := json.Unmarshal([]byte(lines[len(lines)-2]), &back); err != nil {
			t.Fatalf("the input line is not one JSON string: %v", err)
		}
		// JSON encodes each byte that is not UTF-8 as U+FFFD, as converting
		// to runes does.
		if want := string([]rune(input)); back != want {
			t.Fatalf("decoded %q, want %q", back, want)
		}
	})
}

// FuzzReviewMatch: a model's answer is untrusted text, and every rule
// reads it; none may panic, and each says the same thing twice.
func FuzzReviewMatch(f *testing.F) {
	for _, seed := range []string{"", `{"a":{"b":0.5}}`, `{"a":[1,2]}`, `[`, `{"a":1e999}`, `{"a":"x","a":"y"}`, "\x00\xff"} {
		f.Add(seed, "length")
	}
	parsed, err := ParseReview([]any{
		map[string]any{"field": "a.b", "below": 0.8}, map[string]any{"field": "a", "in": []any{"x", 1.0}},
		map[string]any{"field": "a", "not_in": []any{map[string]any{"k": "v"}}}, map[string]any{"answer_matches": "(?i)a+b"},
		map[string]any{"stop_reason": []any{"length"}}, map[string]any{"cost_over": 0.1},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, answer, stop string) {
		o := Outcome{Answer: answer, StopReason: stop, Cost: 0.2, Priced: true}
		first := Reasons(parsed, o)
		if again := Reasons(parsed, o); strings.Join(first, "\n") != strings.Join(again, "\n") {
			t.Fatalf("reasons differ: %q then %q", first, again)
		}
	})
}

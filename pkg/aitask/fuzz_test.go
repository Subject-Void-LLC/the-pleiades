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

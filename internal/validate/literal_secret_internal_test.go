// Internal tests for the literal secret warning's helpers.
package validate

import "testing"

// TestSecretWordBefore names the word a secret follows, and a fallback when
// the text holds nothing masked.
func TestSecretWordBefore(t *testing.T) {
	if got := secretWordBefore("  pre-shared-key WrittenOut-9876"); got != "pre-shared-key" {
		t.Errorf("secretWordBefore = %q", got)
	}
	if got := secretWordBefore("hostname r1"); got != "a secret word" {
		t.Errorf("secretWordBefore with nothing masked = %q", got)
	}
}

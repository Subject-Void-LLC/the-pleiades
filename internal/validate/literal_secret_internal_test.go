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

// TestMasksSomething_FindsNoneInWhatJSONCannotHold: a value JSON cannot
// hold is no document the run masks, so it draws no warning here, and the
// method refuses it itself.
func TestMasksSomething_FindsNoneInWhatJSONCannotHold(t *testing.T) {
	if masksSomething(map[string]any{"password": make(chan int)}) {
		t.Error("masksSomething found a secret in a value JSON cannot hold")
	}
	if !masksSomething(map[string]any{"password": "Hunter2Secret"}) {
		t.Error("masksSomething found no secret under a password field")
	}
}

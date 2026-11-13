// Tests of readsSecretRoot, which decides whether a set_fact value reads the
// secret root however the reference is written.
package validate

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestReadsSecretRoot_EveryShapeAndNoneThatDoesNotCompile holds a plain
// reference, one joined to other text, an unterminated reference (still a
// reference: refused, the safe direction), a value reading another root, and
// source that does not compile and names no secret, which reads nothing (the
// template rule refuses it on its own).
func TestReadsSecretRoot_EveryShapeAndNoneThatDoesNotCompile(t *testing.T) {
	eng := render.New()
	for source, want := range map[string]bool{
		"{{ secret.vpn.psk }}":           true,
		"key-{{ secret.vpn.psk ~ 'x' }}": true,
		"{{ vars.site }}":                false,
		"{{ secret.vpn.psk":              true,
		"{{ vars.site":                   false,
		"plain text":                     false,
	} {
		if got := readsSecretRoot(eng, source); got != want {
			t.Errorf("readsSecretRoot(%q) = %v, want %v", source, got, want)
		}
	}
}

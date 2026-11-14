// Tests for add-credential's warning about a secret that is the username.
package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestSameAsUsernameWarnings: a password or enable secret equal to the
// username is warned about, since its masking masks the username too; any
// other credential is not.
func TestSameAsUsernameWarnings(t *testing.T) {
	w := sameAsUsernameWarnings(credential.Credential{Username: "cisco", Password: "cisco", EnablePassword: "cisco"})
	if len(w) != 2 {
		t.Errorf("warnings = %v, want one for the password and one for the enable secret", w)
	}
	for _, line := range w {
		if strings.Contains(line, "cisco") {
			t.Errorf("the warning prints the value, which is the secret: %q", line)
		}
	}
	// Masking by value is exact, so a secret differing in case masks nothing
	// of the username, and is not warned about.
	if w := sameAsUsernameWarnings(credential.Credential{Username: "cisco", Password: "Cisco"}); len(w) != 0 {
		t.Errorf("warnings = %v for a secret differing in case, want none", w)
	}
	if w := sameAsUsernameWarnings(credential.Credential{Username: "netops", Password: "Gx-0123456789ab"}); len(w) != 0 {
		t.Errorf("warnings = %v, want none", w)
	}
}

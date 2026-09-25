// Tests for SecretName and SecretShaped.
package redact_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// TestSecretName covers whole names, word runs inside longer names, case,
// and the whole-word rule that keeps unrelated names out.
func TestSecretName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"password", true},
		{"db_password", true},
		{"MYSQL_ROOT_PASSWORD", true},
		{"vault-api-token", true},
		{"ansible_become_pass", true},
		{"deploy.private_key", true},
		{"tokenizer", false},
		{"passwordless", false},
		{"nginx_state", false},
		{"image_server", false},
		{"", false},
	} {
		if got := redact.SecretName(tc.name); got != tc.want {
			t.Errorf("SecretName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSecretShaped covers a value only a pattern rule recognizes, and a
// plain one.
func TestSecretShaped(t *testing.T) {
	if !redact.SecretShaped("-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----") {
		t.Error("a PEM private key was not recognized")
	}
	if !redact.SecretShaped("https://admin:hunter2hunter2@example.com/repo.git") {
		t.Error("a URL carrying a password was not recognized")
	}
	if redact.SecretShaped("cat9k_iosxe.17.03.04.SPA.bin") {
		t.Error("an image file name was taken for a secret")
	}
}

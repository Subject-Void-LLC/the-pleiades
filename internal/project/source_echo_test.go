// Package project_test's echo coverage: an edit that sends back the URL it
// was shown keeps the stored one, and anything else is a real change.
package project_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestKeepEchoedSourceURL_KeepsTheStoredURLOnlyForWhatItRedactsTo walks the
// edges: a URL with no credential redacts to itself, a userinfo password and
// an https token redact to "redacted", an scp-like URL net/url cannot read is
// withheld whole, and any other submission is taken as it is.
func TestKeepEchoedSourceURL_KeepsTheStoredURLOnlyForWhatItRedactsTo(t *testing.T) {
	for _, tc := range []struct {
		name, stored, submitted, want string
	}{
		{"a clean URL sent back", "https://example.invalid/a.git", "https://example.invalid/a.git", "https://example.invalid/a.git"},
		{"a clean URL changed", "https://example.invalid/a.git", "https://example.invalid/b.git", "https://example.invalid/b.git"},
		{"a password echoed redacted", "https://svc:hunter2@example.invalid/a.git", "https://redacted@example.invalid/a.git", "https://svc:hunter2@example.invalid/a.git"},
		{"a token echoed redacted", "https://tok3n@example.invalid/a.git", "https://redacted@example.invalid/a.git", "https://tok3n@example.invalid/a.git"},
		{"a password replaced", "https://svc:hunter2@example.invalid/a.git", "https://example.invalid/a.git", "https://example.invalid/a.git"},
		{"a withheld URL echoed", "git:hunter2@example.invalid:team/a.git", project.RedactedSourceURL("git:hunter2@example.invalid:team/a.git"), "git:hunter2@example.invalid:team/a.git"},
		{"an ssh user is not a secret", "git@example.invalid:team/a.git", "git@example.invalid:team/a.git", "git@example.invalid:team/a.git"},
		{"nothing stored, nothing sent", "", "", ""},
		{"a cleared URL is a change", "https://example.invalid/a.git", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := project.KeepEchoedSourceURL(tc.stored, tc.submitted); got != tc.want {
				t.Errorf("KeepEchoedSourceURL(%q, %q) = %q, want %q", tc.stored, tc.submitted, got, tc.want)
			}
		})
	}

	// The withheld case is only an echo if the placeholder really is what is
	// shown; if the redactor ever changed it, the row above would pass for
	// the wrong reason.
	if shown := project.RedactedSourceURL("git:hunter2@example.invalid:team/a.git"); shown == "git:hunter2@example.invalid:team/a.git" {
		t.Fatalf("the scp-like URL with a password is shown as stored: %q", shown)
	}
}

package credential_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/credential"
)

func TestMask(t *testing.T) {
	tests := []struct {
		name    string
		secrets []string
		text    string
		want    string
	}{
		{
			name:    "single secret masked",
			secrets: []string{"hunter2"},
			text:    "password is hunter2 today",
			want:    "password is ******** today",
		},
		{
			name:    "multiple secrets masked",
			secrets: []string{"hunter2", "swordfish"},
			text:    "password hunter2, key swordfish",
			want:    "password ********, key ********",
		},
		{
			name:    "secret not present leaves text unchanged",
			secrets: []string{"nowhere"},
			text:    "nothing to see here",
			want:    "nothing to see here",
		},
		{
			name:    "empty secret is skipped, not a corruption",
			secrets: []string{""},
			text:    "abc",
			want:    "abc",
		},
		{
			name:    "mixed empty and real secrets",
			secrets: []string{"", "abc"},
			text:    "xabcx",
			want:    "x********x",
		},
		{
			name:    "overlapping secrets masked longest first",
			secrets: []string{"abc", "abcdef"},
			text:    "prefix abcdef suffix",
			want:    "prefix ******** suffix",
		},
		{
			name:    "overlapping secrets, order in the slice does not matter",
			secrets: []string{"abcdef", "abc"},
			text:    "prefix abcdef suffix",
			want:    "prefix ******** suffix",
		},
		{
			name:    "repeated occurrences of the same secret all masked",
			secrets: []string{"tok"},
			text:    "tok-tok-tok",
			want:    "********-********-********",
		},
		{
			name:    "nil secrets slice never panics and is a no-op",
			secrets: nil,
			text:    "abc",
			want:    "abc",
		},
		{
			name:    "nil secrets on empty text never panics",
			secrets: nil,
			text:    "",
			want:    "",
		},
		{
			name:    "empty text with real secrets never panics",
			secrets: []string{"abc"},
			text:    "",
			want:    "",
		},
		{
			name:    "equal-length secrets break ties deterministically",
			secrets: []string{"bbb", "aaa"},
			text:    "aaa bbb",
			want:    "******** ********",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := credential.Mask(tt.secrets, tt.text)
			if got != tt.want {
				t.Errorf("Mask(%v, %q) = %q, want %q", tt.secrets, tt.text, got, tt.want)
			}
		})
	}
}

// TestMask_LongestFirstLeavesNoResidualFragment is a dedicated regression
// test for the exact scenario the overlapping-secrets rule exists for: a
// shorter secret that is a prefix of a longer one must not cause the
// longer secret's suffix to leak next to the placeholder.
func TestMask_LongestFirstLeavesNoResidualFragment(t *testing.T) {
	password := "abc"
	passphrase := "abcdef"
	text := "login abcdef now"

	got := credential.Mask([]string{password, passphrase}, text)

	if strings.Contains(got, "def") {
		t.Fatalf("residual fragment of the longer secret leaked: %q", got)
	}
	want := "login ******** now"
	if got != want {
		t.Fatalf("got %q, want %q (a single placeholder covering the full longer secret)", got, want)
	}
}

// TestMask_AllAsteriskSecretIsDocumentedException proves the simplest
// case of the documented exception to "the output never contains the
// secret": a secret made entirely of asterisks cannot be distinguished
// from maskPlaceholder itself, so the output legitimately still contains
// that substring, even though the real occurrence was fully replaced.
func TestMask_AllAsteriskSecretIsDocumentedException(t *testing.T) {
	got := credential.Mask([]string{"**"}, "key=** end")
	if !strings.Contains(got, "**") {
		t.Fatalf("expected the documented exception to hold (output still contains \"**\"), got %q", got)
	}
	// The real occurrence must still be gone from its original position;
	// what remains is maskPlaceholder's own asterisks, not the input.
	want := "key=******** end"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestMask_LeadingAsteriskBoundaryException is a named regression test
// for a fuzz-discovered instance of the same documented exception (see
// mask.go): a secret that merely starts (or ends) with '*', not one made
// entirely of asterisks, can also be reconstructed across a
// placeholder's boundary when adjacent leftover text happens to
// continue the pattern. FuzzMask found secret=`*"` text=`*""` as a
// minimal failing case before this exception was broadened to cover it;
// this pins that exact shape down the same way
// TestBuildFromYAML_RejectsAliasBomb (internal/engine/yaml_test.go) pins
// a curated, known-attack-shape regression rather than leaving it to be
// rediscovered by chance.
func TestMask_LeadingAsteriskBoundaryException(t *testing.T) {
	secret := `*"`
	text := `*""`

	got := credential.Mask([]string{secret}, text)

	// The real occurrence (the first two characters) is masked...
	want := "********\""
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// ...but the placeholder's trailing '*' plus the untouched leftover
	// '"' still spell the secret back out, which is exactly the
	// documented, accepted exception, not a masking failure: the
	// leftover '"' was never part of any match, and the real occurrence
	// really was replaced.
	if !strings.Contains(got, secret) {
		t.Fatalf("expected the documented boundary exception to reproduce here, got %q", got)
	}
}

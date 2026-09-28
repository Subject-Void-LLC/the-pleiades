// Tests for NormalizeEmail, the one rule every account table's key comes
// from.
package auth_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

func TestNormalizeEmail(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string // empty means refused
	}{
		{"already the key", "operator@example.test", "operator@example.test"},
		{"trimmed and lowercased", "  MiXeD@Example.TEST\t", "mixed@example.test"},
		{"a NEL at the edge is whitespace", string(rune(0x85)) + "a@example.test", "a@example.test"},
		{"a non-ASCII capital folds", string(rune(0xC4)) + "bc@example.test", string(rune(0xE4)) + "bc@example.test"},
		{"a capital dotted I folds to i", "adm" + string(rune(0x130)) + "n@example.test", "admin@example.test"},
		{"the Kelvin sign folds to k", string(rune(0x212A)) + "elvin@example.test", "kelvin@example.test"},
		{"a real replacement character is kept", "a" + string(utf8.RuneError) + "b@example.test", "a" + string(utf8.RuneError) + "b@example.test"},
		{"a combining mark is kept, not composed", "jose" + string(rune(0x301)) + "@example.test", "jose" + string(rune(0x301)) + "@example.test"},
		{"empty", "", ""},
		{"only whitespace", " \t ", ""},
		{"no @", "nonsense", ""},
		{"a byte that is not UTF-8", "a\xffb@example.test", ""},
		{"a NUL", "admin@example.test\x00", ""},
		{"a NUL inside", "adm\x00in@example.test", ""},
		{"a tab inside", "adm\tin@example.test", ""},
		{"a newline inside", "adm\nin@example.test", ""},
		{"DEL", "admin\x7f@example.test", ""},
		{"a C1 control inside", "adm" + string(rune(0x85)) + "in@example.test", ""},
		{"a text direction override", "admin" + string(rune(0x202e)) + "@example.test", ""},
		{"an escape sequence", "admin\x1b[2J@example.test", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := auth.NormalizeEmail(tc.in)
			if tc.want == "" {
				if !errors.Is(err, auth.ErrInvalidEmail) {
					t.Fatalf("NormalizeEmail(%q) = %q, %v; want ErrInvalidEmail", tc.in, got, err)
				}
				// The refusal is printed by callers, so it must not carry
				// the raw input: a malformed byte or a control character in
				// an error message reaches a log or a terminal as-is.
				if strings.ContainsFunc(err.Error(), unicode.IsControl) || !utf8.ValidString(err.Error()) {
					t.Errorf("the error %q carries the raw input", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// FuzzNormalizeEmail proves the rule's shape for any input: it never
// panics, it refuses exactly what it should, and what it accepts is a key
// that normalizes to itself, so a person typing the address the users list
// shows them reaches the same row the address was stored under.
func FuzzNormalizeEmail(f *testing.F) {
	for _, s := range []string{
		"operator@example.test", "  A@B ", "", "@", "a\xffb@x", "a\x00@x", "a\t@x",
		"a" + string(rune(0x202e)) + "@x", string(rune(0x130)) + "@x", string(rune(0x212A)) + "@x",
		"a" + string(utf8.RuneError) + "@x", "jose" + string(rune(0x301)) + "@x",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got, err := auth.NormalizeEmail(in)
		trimmed := strings.TrimSpace(in)
		malformed := !utf8.ValidString(trimmed) || strings.ContainsFunc(trimmed, func(r rune) bool {
			return unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
				r == 0x061c || r == 0x200e || r == 0x200f
		})
		shouldRefuse := trimmed == "" || malformed || !strings.Contains(strings.ToLower(trimmed), "@")

		if shouldRefuse {
			if !errors.Is(err, auth.ErrInvalidEmail) {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want a refusal", in, got, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) refused a usable address: %v", in, err)
		}
		switch {
		case !utf8.ValidString(got):
			t.Fatalf("NormalizeEmail(%q) = %q is not valid UTF-8", in, got)
		case got != strings.TrimSpace(got):
			t.Fatalf("NormalizeEmail(%q) = %q has whitespace at an edge", in, got)
		case got != strings.ToLower(got):
			t.Fatalf("NormalizeEmail(%q) = %q is not its own lower case", in, got)
		case !strings.Contains(got, "@"):
			t.Fatalf("NormalizeEmail(%q) = %q has no @", in, got)
		}
		again, err := auth.NormalizeEmail(got)
		if err != nil || again != got {
			t.Fatalf("NormalizeEmail is not idempotent: %q -> %q -> %q (%v)", in, got, again, err)
		}
	})
}

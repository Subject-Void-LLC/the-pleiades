// Package recordname_test holds the name rule to its near misses: the three
// names it refuses, and the dotted and escaped names beside them that stay
// usable (LESSONS 114).
package recordname_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// TestCheck_RefusesOnlyTheThreeNamesNoURLCarries tables the rule. The rows
// that matter are the ones a reasonable person writes next: a name of
// three dots, a dot with a letter, an escaped dot, a space around a dot.
// Each is a real name a URL can carry once escaped, so each stays usable.
func TestCheck_RefusesOnlyTheThreeNamesNoURLCarries(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"", false},
		{".", false},
		{"..", false},
		{"...", true},
		{".a", true},
		{"a.", true},
		{"..a", true},
		{" .", true},
		{". ", true},
		{"%2E", true},
		{"%2E%2E", true},
		{"a/..", true},
		{"sw/1", true},
		{"core-router-01", true},
	} {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			if got := recordname.Addressable(tc.name); got != tc.ok {
				t.Errorf("Addressable(%q) = %v, want %v", tc.name, got, tc.ok)
			}
			err := recordname.Check(tc.name)
			if tc.ok {
				if err != nil {
					t.Fatalf("Check(%q) = %v, want nil", tc.name, err)
				}
				return
			}
			if !errors.Is(err, recordname.ErrUnaddressable) {
				t.Fatalf("Check(%q) = %v, want an error matching ErrUnaddressable", tc.name, err)
			}
			// Wrapped the way a store wraps it, it still matches.
			if wrapped := fmt.Errorf("creating device: %w", err); !errors.Is(wrapped, recordname.ErrUnaddressable) {
				t.Errorf("a wrapped Check error no longer matches ErrUnaddressable")
			}
		})
	}
}

// TestCheck_SaysWhy holds the message to the reason a person can act on.
func TestCheck_SaysWhy(t *testing.T) {
	if msg := recordname.Check("").Error(); !strings.Contains(msg, "a name is required") {
		t.Errorf("Check(\"\") says %q, want it to say a name is required", msg)
	}
	for _, name := range []string{".", ".."} {
		msg := recordname.Check(name).Error()
		if !strings.Contains(msg, fmt.Sprintf("%q cannot be used", name)) || !strings.Contains(msg, "steps between folders") {
			t.Errorf("Check(%q) says %q, want it to name the name and say why", name, msg)
		}
	}
}

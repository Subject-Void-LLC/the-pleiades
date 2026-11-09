// Package recordname_test holds the name rule to its near misses: the three
// names it refuses, and the dotted and escaped names beside them that stay
// usable (LESSONS 114).
package recordname_test

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// TestCheck_RefusesOnlyTheThreeNamesNoURLCarries tables the address half
// of the rule over printable ASCII names. The rows that matter are the ones
// a reasonable person writes next: a name of three dots, a dot with a
// letter, an escaped dot, a space around a dot. Each is a real name a URL
// can carry once escaped, so each stays usable. recordname_shown_test.go
// tables the other half.
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
			if !errors.Is(err, recordname.ErrUnusable) {
				t.Fatalf("Check(%q) = %v, want an error matching ErrUnusable", tc.name, err)
			}
			// Wrapped the way a store wraps it, it still matches.
			if wrapped := fmt.Errorf("creating device: %w", err); !errors.Is(wrapped, recordname.ErrUnusable) {
				t.Errorf("a wrapped Check error no longer matches ErrUnusable")
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

// TestSegment_EscapesANameEqualToAStaticSibling: a name equal to a fixed
// segment beside its parameter has its first byte encoded, so the router
// reads it as the parameter; it decodes back to the name; every other name
// is url.PathEscape's form.
func TestSegment_EscapesANameEqualToAStaticSibling(t *testing.T) {
	static := map[string]bool{"new": true, "chart.json": true}
	for name, want := range map[string]string{
		"new":        "%6Eew",
		"chart.json": "%63hart.json",
		"news":       "news",
		"sw/1":       "sw%2F1",
		"New":        "New",
	} {
		got := recordname.Segment(name, static)
		if got != want {
			t.Errorf("Segment(%q) = %q, want %q", name, got, want)
		}
		if back, err := url.PathUnescape(got); err != nil || back != name {
			t.Errorf("Segment(%q) = %q, which decodes to %q, %v", name, got, back, err)
		}
	}
	if got := recordname.Segment("new", nil); got != "new" {
		t.Errorf("Segment with no static siblings = %q, want the name as it is", got)
	}
}

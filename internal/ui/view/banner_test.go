package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

func TestParseBanner_AcceptsEveryDeclaredLevel(t *testing.T) {
	for _, level := range view.BannerLevelNames() {
		t.Run(level, func(t *testing.T) {
			b, err := view.ParseBanner(level, "environment marking")
			if err != nil {
				t.Fatalf("ParseBanner(%q) = %v", level, err)
			}
			if !b.Configured() {
				t.Error("Configured() = false for a parsed banner")
			}
			if b.Class() != "banner-"+level {
				t.Errorf("Class() = %q, want banner-%s", b.Class(), level)
			}
			// Uppercased in Go rather than in CSS, so the text a screen
			// reader announces matches the text on screen.
			if b.Label() != "ENVIRONMENT MARKING" {
				t.Errorf("Label() = %q, want it uppercased", b.Label())
			}
		})
	}
}

// No configuration means no banner. Defaulting to one would eventually
// default to the wrong one, and a banner that says the wrong thing is
// worse than no banner at all.
func TestParseBanner_UnconfiguredIsSilent(t *testing.T) {
	b, err := view.ParseBanner("", "")
	if err != nil {
		t.Fatalf("ParseBanner(empty) = %v, want nil", err)
	}
	if b.Configured() {
		t.Error("an unconfigured banner reports itself configured")
	}
}

// Fails closed and loudly. An operator who configures a classification
// banner and gets nothing would believe a marking was displayed when it
// was not, which is the exact failure a marking exists to prevent.
func TestParseBanner_RejectsMisconfiguration(t *testing.T) {
	for _, tc := range []struct {
		name         string
		level, text  string
		wantContains string
	}{
		{"unknown level", "topsecret-ish", "SECRET", "unknown banner level"},
		{"level without text", "secret", "  ", "banner text is empty"},
		{"text too long", "secret", strings.Repeat("x", 200), "want at most"},
		{"newline in text", "secret", "SECRET\nnot really", "control character"},
		{"null in text", "secret", "SECRET\x00", "control character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := view.ParseBanner(tc.level, tc.text)
			if err == nil {
				t.Fatalf("ParseBanner(%q, %q) = nil, want an error", tc.level, tc.text)
			}
			if !strings.Contains(err.Error(), tc.wantContains) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantContains)
			}
		})
	}
}

// A level is a marking with a published meaning, not free text, so case is
// normalised rather than rejected -- but an unrecognised value never is.
func TestParseBanner_NormalisesLevelCase(t *testing.T) {
	b, err := view.ParseBanner("  SECRET  ", "secret")
	if err != nil {
		t.Fatalf("ParseBanner = %v", err)
	}
	if b.Class() != "banner-secret" {
		t.Errorf("Class() = %q, want banner-secret", b.Class())
	}
}

// The class is drawn from a closed set, so nothing configuration-controlled
// can reach a class attribute even if validation were somehow bypassed.
func TestBanner_ClassIsAlwaysFromTheClosedSet(t *testing.T) {
	rogue := view.Banner{Level: view.BannerLevel("'><script>"), Text: "x"}
	if got := rogue.Class(); !strings.HasPrefix(got, "banner-") || strings.ContainsAny(got, "<>'\"") {
		t.Errorf("Class() = %q, want a safe closed-set name", got)
	}
}

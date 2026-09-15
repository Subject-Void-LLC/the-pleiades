// Package view_test's settings-area tests. The surface is entirely declared,
// so these are about shape: that every tile exists, that every field explains
// itself, and that a secret is declared as one before anything stores it.
package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The settings area is entirely declared, which makes its tests about shape
// rather than behaviour: that every tile a reader is promised exists, that
// every field carries the explanation which is the whole reason to render an
// unimplemented surface at all, and that an unrecognised tab cannot produce a
// blank page.

func settingsModel(tab string) view.SystemSettingsModel {
	return view.SystemSettingsModel{
		Page: view.PageModel{Prefix: "/ui", Subject: "operator@example.test", Title: "Settings"},
		Tab:  tab,
	}
}

func TestSystemSettings_CarriesEveryTile(t *testing.T) {
	tiles := settingsModel("").Tiles()

	want := []string{"Authentication", "Jobs", "System", "User interface", "Logging"}
	if len(tiles) != len(want) {
		t.Fatalf("got %d tiles, want %d: %+v", len(tiles), len(want), tiles)
	}
	for i, title := range want {
		if tiles[i].Title != title {
			t.Errorf("tile %d = %q, want %q", i, tiles[i].Title, title)
		}
	}

	// Each is reachable as a tab, and the first is the one a bare URL opens.
	tabs := settingsModel("").Chrome().Tabs
	if len(tabs) != len(want) {
		t.Fatalf("got %d tabs, want one per tile", len(tabs))
	}
	if !tabs[0].Current || tabs[0].Href != "/ui/settings" {
		t.Errorf("first tab = %+v, want the current one at the bare URL", tabs[0])
	}
	for _, tab := range tabs[1:] {
		if !strings.Contains(tab.Href, "?tab=") {
			t.Errorf("tab %q has no address: %q", tab.Label, tab.Href)
		}
	}
}

func TestSystemSettings_EveryFieldExplainsItself(t *testing.T) {
	// A declared surface that does not say what each value is for is a list
	// of names, which is exactly the thing this exists instead of.
	for _, tile := range settingsModel("").Tiles() {
		if tile.Summary == "" {
			t.Errorf("tile %q has no summary", tile.Title)
		}
		if len(tile.Groups) == 0 {
			t.Errorf("tile %q has no groups", tile.Title)
		}
		for _, g := range tile.Groups {
			if g.Summary == "" {
				t.Errorf("%s / %s has no summary", tile.Title, g.Title)
			}
			if !g.Implemented() && g.Reason == "" {
				t.Errorf("%s / %s is declared and says nothing about why", tile.Title, g.Title)
			}
			if strings.EqualFold(strings.TrimSpace(g.Reason), "not implemented") {
				t.Errorf("%s / %s says only that it is not implemented, which the reader can already see", tile.Title, g.Title)
			}
			if len(g.Fields) == 0 {
				t.Errorf("%s / %s declares no fields, so its shape is not visible", tile.Title, g.Title)
			}
			for _, f := range g.Fields {
				if f.Label == "" {
					t.Errorf("%s / %s has an unlabelled field %q", tile.Title, g.Title, f.Name)
				}
				if f.Help == "" {
					t.Errorf("%s / %s / %s has no help, so its shape is a name and nothing else", tile.Title, g.Title, f.Label)
				}
				if f.Kind == "" {
					t.Errorf("%s / %s / %s declares no kind", tile.Title, g.Title, f.Label)
				}
			}
		}
	}
}

func TestSystemSettings_SecretsAreWriteOnlyKinds(t *testing.T) {
	// The shape has to be right before the store exists, because a field
	// declared as text is a field somebody implements as text. Every secret
	// on this surface is a password kind, which is the one kind the form
	// machinery refuses to render a value back into.
	secretish := []string{"secret", "password", "token"}
	for _, tile := range settingsModel("").Tiles() {
		for _, g := range tile.Groups {
			for _, f := range g.Fields {
				var looksSecret bool
				for _, word := range secretish {
					if strings.Contains(strings.ToLower(f.Name), word) {
						looksSecret = true
					}
				}
				if looksSecret && f.Kind != view.KindPassword {
					t.Errorf("%s / %s / %s looks like a secret and is declared %q, not a password kind",
						tile.Title, g.Title, f.Label, f.Kind)
				}
			}
		}
	}
}

func TestSystemSettings_TabSelectionAndFallback(t *testing.T) {
	for _, tc := range []struct{ name, tab, want string }{
		{"unset opens the first tile", "", "authentication"},
		{"a named tile", "logging", "logging"},
		{"a two-word tile", "user-interface", "user-interface"},
		// The value arrives from a query string, so it arrives from
		// whatever somebody pasted into an address bar.
		{"unknown falls back", "no-such-tile", "authentication"},
		{"a hostile value falls back", `"><script>`, "authentication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := settingsModel(tc.tab)
			if got := m.CurrentTab(); got != tc.want {
				t.Errorf("CurrentTab = %q, want %q", got, tc.want)
			}
			if got := m.CurrentTile().Slug(); got != tc.want {
				t.Errorf("CurrentTile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSystemSettings_ChromeSaysWhatItIsNot(t *testing.T) {
	c := settingsModel("").Chrome()

	if c.Title != "Settings" {
		t.Errorf("Title = %q", c.Title)
	}
	// The collision this page was named out of: the other settings page is
	// Preferences, and the summary points at it so nobody hunts for their
	// own password here.
	if !strings.Contains(c.Summary, "Preferences") {
		t.Errorf("Summary = %q, want it to distinguish this from the caller's own preferences", c.Summary)
	}
	if len(c.Badges) != 1 || c.Badges[0].Label != "Declared" {
		t.Errorf("Badges = %+v, want the declared marker", c.Badges)
	}
	if z := settingsModel("").Declared(); z.Tone != view.ZoneDeclared || z.Body == "" {
		t.Errorf("Declared = %+v, want the declared tone with a stated reason", z)
	}
}

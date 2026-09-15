// Package view_test's Preferences tests: the tabs, and the appearance controls
// that moved off the sidebar onto them.
package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The settings page is where the appearance controls went when they left the
// sidebar, and its model holds the copy that describes each choice. A skin
// named and not described is the thing it replaced, so the descriptions are
// asserted rather than assumed.

func settingsPage() view.PageModel {
	return view.PageModel{Prefix: "/ui", Subject: "operator@example.test"}
}

func TestAccount_TabsAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, tab  string
		passwords  bool
		want       string
		wantTabs   int
		appearance bool
	}{
		{name: "opens on appearance", passwords: true, want: "appearance", wantTabs: 2, appearance: true},
		{name: "the password section", tab: "password", passwords: true, want: "password", wantTabs: 2},
		{name: "an unknown tab falls back", tab: "nonsense", passwords: true, want: "appearance", wantTabs: 2, appearance: true},
		// A deployment federating against an external issuer holds no
		// password, so the section is not offered rather than offered and
		// then refused.
		{name: "no local credentials, no password tab", tab: "password", passwords: false, want: "appearance", wantTabs: 1, appearance: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := view.AccountModel{Page: settingsPage(), Tab: tc.tab, PasswordChanges: tc.passwords}

			if got := m.CurrentTab(); got != tc.want {
				t.Errorf("CurrentTab = %q, want %q", got, tc.want)
			}
			if m.ShowAppearance() != tc.appearance {
				t.Errorf("ShowAppearance = %v, want %v", m.ShowAppearance(), tc.appearance)
			}
			if m.ShowAppearance() == m.ShowPassword() {
				t.Error("exactly one section must render")
			}
			if got := len(m.Chrome().Tabs); got != tc.wantTabs {
				t.Errorf("tabs = %d, want %d", got, tc.wantTabs)
			}
		})
	}
}

func TestAccount_ChromeAndActions(t *testing.T) {
	m := view.AccountModel{Page: settingsPage(), PasswordChanges: true}

	c := m.Chrome()
	// "Preferences", not "Settings": Settings is the deployment's own
	// configuration and is a different page with a different scope behind
	// it. Two pages called the same thing is the collision this renamed out.
	if c.Title != "Preferences" {
		t.Errorf("Title = %q", c.Title)
	}
	if !strings.Contains(c.Summary, "operator@example.test") {
		t.Errorf("Summary = %q, want it to name who is signed in", c.Summary)
	}
	if last := c.Crumbs[len(c.Crumbs)-1]; !last.Current || last.Label != "Preferences" {
		t.Errorf("last crumb = %+v", last)
	}
	if got := m.PasswordAction(); got != "/ui/account/password" {
		t.Errorf("PasswordAction = %q", got)
	}
	if got := m.Page.AccountHref(); got != "/ui/account" {
		t.Errorf("AccountHref = %q", got)
	}
}

func TestAccount_EveryChoiceIsDescribed(t *testing.T) {
	m := view.AccountModel{Page: settingsPage()}

	skins := m.SkinChoices()
	if len(skins) == 0 {
		t.Fatal("no skins offered")
	}
	for _, c := range skins {
		if c.Summary == "" {
			t.Errorf("skin %q has no description, which is what four bare buttons already were", c.Label)
		}
	}
	// Exactly one is selected, and Selected agrees with the pressed value
	// the button renders.
	var selected int
	for _, c := range skins {
		if c.Selected {
			selected++
			if c.Pressed != "true" {
				t.Errorf("skin %q is selected but reports aria-pressed=%q", c.Label, c.Pressed)
			}
		}
	}
	if selected != 1 {
		t.Errorf("%d skins marked selected, want exactly 1", selected)
	}

	themes := m.ThemeChoices()
	if len(themes) != 3 {
		t.Errorf("themes = %d, want three states rather than a toggle", len(themes))
	}
	for _, c := range themes {
		if c.Summary == "" {
			t.Errorf("theme %q has no description", c.Label)
		}
	}
}

func TestAccount_AccessibilityModeIsDescribedInFull(t *testing.T) {
	// It is not a contrast switch, and calling it one undersells it into
	// being something nobody turns on. Each effect is a separate success
	// criterion somebody might need on its own.
	m := view.AccountModel{Page: settingsPage()}
	effects := m.AccessibilityEffects()
	if len(effects) < 4 {
		t.Fatalf("%d effects listed, want the whole mode described", len(effects))
	}

	joined := strings.ToLower(strings.Join(effects, " "))
	for _, expected := range []string{"contrast", "border", "motion", "underline", "focus"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("the description never mentions %q, so the mode reads as narrower than it is", expected)
		}
	}
}

func TestAccount_TheToggleStateIsAWordNotOnlyAFill(t *testing.T) {
	// A toggle whose state is signalled only by its background is a toggle
	// signalled by colour alone.
	off := view.PageModel{}
	on := view.PageModel{A11y: true}

	if off.A11yStateLabel() != "Off" || on.A11yStateLabel() != "On" {
		t.Errorf("state labels = %q / %q", off.A11yStateLabel(), on.A11yStateLabel())
	}
	// And the control submits the opposite of what is current, so pressing
	// it changes something.
	if off.A11yNext() != "on" || on.A11yNext() != "off" {
		t.Errorf("A11yNext = %q / %q, want each to submit the opposite state", off.A11yNext(), on.A11yNext())
	}
	if off.A11yPressed() != "false" || on.A11yPressed() != "true" {
		t.Errorf("A11yPressed = %q / %q", off.A11yPressed(), on.A11yPressed())
	}
}

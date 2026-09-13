// Package view's account page: the signed-in caller's own preferences.
//
// Named Preferences rather than Settings, which is the deployment's own
// configuration in systemsettings.go. The two share nothing but the word, and
// the word belongs to the one an operator arrives searching for.
package view

import "path"

// AccountModel is the signed-in caller's own preferences page.
//
// The appearance controls used to live pinned under the sidebar's navigation
// on every page: three fieldsets, eight buttons, roughly 360 pixels of chrome
// spent on settings most operators choose once. They are here now, where a
// skin can be shown rather than named, and where the accessibility mode has
// room for the sentence that says what it actually does.
//
// The one control that did not move is the accessibility toggle, which stays
// in the sidebar foot. See render's accessibilityToggle for why that is not
// an inconsistency.
type AccountModel struct {
	Page PageModel

	// Tab is the requested section, unvalidated. CurrentTab resolves it.
	Tab string

	// PasswordChanges reports whether this deployment holds local
	// credentials at all. A deployment federating against an external
	// issuer has no password to change, and rendering the tab would be an
	// affordance that can only ever fail.
	PasswordChanges bool
}

// The settings sections. Constants because the handler that routes to them
// and the model that links to them have to agree.
const (
	appearanceTab = "appearance"
	passwordTab   = "password"
)

// CurrentTab is the selected section, defaulting to appearance.
//
// Appearance rather than the profile, because it is the section with
// something to do in it: the profile is one line naming who you are signed in
// as, which the chrome already says.
func (m AccountModel) CurrentTab() string {
	if m.Tab == passwordTab && m.PasswordChanges {
		return passwordTab
	}
	return appearanceTab
}

// ShowAppearance reports whether the appearance section renders.
func (m AccountModel) ShowAppearance() bool { return m.CurrentTab() == appearanceTab }

// ShowPassword reports whether the password section renders.
func (m AccountModel) ShowPassword() bool { return m.CurrentTab() == passwordTab }

// Chrome is the settings page header, built the same way every other page's
// is so this page cannot drift from the rest of the application.
func (m AccountModel) Chrome() Chrome {
	base := m.Page.AccountHref()
	tabs := []Tab{{
		Label:   "Appearance",
		Slug:    appearanceTab,
		Href:    base,
		Current: m.ShowAppearance(),
	}}
	if m.PasswordChanges {
		tabs = append(tabs, Tab{
			Label:   "Password",
			Slug:    passwordTab,
			Href:    base + "?tab=" + passwordTab,
			Current: m.ShowPassword(),
		})
	}
	return Chrome{
		Crumbs: []Crumb{
			{Label: "Account"},
			{Label: "Preferences", Current: true},
		},
		Title:   "Preferences",
		Summary: "Signed in as " + m.Page.Subject + ". Nothing here changes what anyone else sees.",
		Tabs:    tabs,
	}
}

// PasswordAction is where the change-password form posts.
func (m AccountModel) PasswordAction() string {
	return path.Join(m.Page.Prefix, "account", "password")
}

// SkinChoice is one skin offered as a choice rather than a label.
//
// Four buttons reading BRUTALIST, LAS VENTANAS, HONEYCRISP and V. ONCE say
// nothing about what any of them looks like, and the fourth is abbreviated
// only because the full name never fit the row it was in. A choice with a
// described outcome is a choice somebody can make once and not revisit.
type SkinChoice struct {
	Value    string
	Label    string
	Summary  string
	Pressed  string
	Selected bool
}

// SkinChoices is the skin control, with each option described.
//
// The summaries name the real mechanism rather than a mood, because the
// mechanism is what a reader will actually notice: a bevel, a radius, a
// translucent pane. They are held here beside SkinOptions rather than in the
// template for the usual reason -- a template that holds copy is a template
// somebody has to edit to fix a sentence.
func (m AccountModel) SkinChoices() []SkinChoice {
	summaries := map[Skin]string{
		SkinBrutalist:       "The default. Hard four-pixel rules, square corners, pure black on white.",
		SkinLasVentanas:     "Windows 95. Four-layer bevels, a teal ground behind the dialog, Tahoma.",
		SkinHoneycrisp:      "macOS. Ten-pixel cards, a soft downward shadow, a translucent sidebar.",
		SkinLasVentanasOnce: "Windows 11. Mica sidebar, eight-pixel cards, the Fluent underline on fields.",
	}

	opts := m.Page.SkinOptions()
	out := make([]SkinChoice, 0, len(opts))
	for _, o := range opts {
		out = append(out, SkinChoice{
			Value:    o.Value,
			Label:    o.Label,
			Summary:  summaries[Skin(o.Value)],
			Pressed:  o.Pressed,
			Selected: o.Pressed == "true",
		})
	}
	return out
}

// ThemeChoices is the theme control, with each option described.
//
// Three options rather than a toggle, and the descriptions are where that
// earns its space: "follow the system" is the default and has to stay
// reachable after somebody has expressed a preference, which a two-state
// control has no way to say.
func (m AccountModel) ThemeChoices() []SkinChoice {
	summaries := map[Theme]string{
		ThemeSystem: "Follows whatever this device is set to, and changes with it.",
		ThemeLight:  "Always light, whatever the device is set to.",
		ThemeDark:   "Always dark, whatever the device is set to.",
	}

	opts := m.Page.ThemeOptions()
	out := make([]SkinChoice, 0, len(opts))
	for _, o := range opts {
		out = append(out, SkinChoice{
			Value:    o.Value,
			Label:    o.Label,
			Summary:  summaries[Theme(o.Value)],
			Pressed:  o.Pressed,
			Selected: o.Pressed == "true",
		})
	}
	return out
}

// AccessibilityEffects is what accessibility mode actually does, listed.
//
// It is not a contrast switch, and calling it one undersells it into being
// something people do not turn on. Over whichever skin and theme are active
// it replaces the palette, thickens every border and rule, stills every
// transition, underlines every link that is not a button, and enlarges the
// focus ring. Each of those is a separate WCAG success criterion somebody
// might need on its own, so the mode says all of them rather than the first.
func (m AccountModel) AccessibilityEffects() []string {
	return []string{
		"Raises contrast: the active skin's palette is replaced with one measured to clear WCAG 2.2 AA everywhere, 4.5:1 on body text and 3:1 on every control edge and boundary.",
		"Thickens borders from the skin's own width to three pixels, and every hairline rule to two, so a boundary is a boundary rather than a suggestion.",
		"Stills the interface: all motion stops. Transitions and animations are reduced to nothing, independently of whatever the operating system was asked for.",
		"Underlines every link that is not a button, so a link is never signalled by colour alone.",
		"Enlarges the focus ring to four pixels with four pixels of offset, in the mode's own link colour.",
	}
}

// A11yStateLabel is the toggle's current state as a word.
//
// Rendered beside the label rather than left to the button's pressed state
// alone. aria-pressed tells a screen reader; a sighted reader gets a word,
// because a toggle whose state is signalled only by its fill is a toggle
// whose state is signalled by colour alone.
func (p PageModel) A11yStateLabel() string {
	if p.A11y {
		return "On"
	}
	return "Off"
}

// AccountHref is where the sidebar's own-preferences link points.
func (p PageModel) AccountHref() string { return path.Join(p.Prefix, "account") }

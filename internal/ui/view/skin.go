package view

import "strings"

// Skin names a theme family: a complete set of values for the one token
// vocabulary every component reads.
//
// It is a separate axis from Theme, not a fourth theme, because the two
// answer different questions. Skin is "which design language", Theme is
// "light or dark", and they compose: Las Ventanas has a dark mode and
// Brutalist has a light one. Folding them into one enum would mean four
// values today and eight the next time either axis grows.
//
// The rule that keeps this cheap: **both skins expose exactly the same
// token names.** Every component rule reads var(--fg), var(--border),
// var(--shadow-offset) and so on, so a skin is a block of values rather
// than a second stylesheet. A skin that needed its own component rules
// would not be a skin, it would be a fork.
type Skin string

const (
	// SkinBrutalist is the default: white ground, 4px black borders, hard
	// non-blurry offset shadows, saturated primaries.
	SkinBrutalist Skin = "brutalist"

	// SkinLasVentanas is the Subject Void design language: tinted black
	// and cream rather than pure black and white, Facebook Blue as the
	// single accent, 2px borders, and no drop shadows at all.
	SkinLasVentanas Skin = "las-ventanas"

	// SkinHoneycrisp is the macOS-like skin, and the quietest of the
	// three: pure white on pure black in light mode, pure black with
	// off-white text in dark mode, Apple's own system blue as the single
	// accent, the macOS system colors for every status fill, 1px borders
	// and a 1px shadow offset. The name is a pun on the palette's source
	// rather than a description of its colors: a Honeycrisp is an apple.
	SkinHoneycrisp Skin = "honeycrisp"
)

// skinLabels is every selectable skin, in the order the control lists
// them, with the name a human reads.
var skinLabels = []struct {
	Skin  Skin
	Label string
}{
	{SkinBrutalist, "Brutalist"},
	{SkinLasVentanas, "Las Ventanas"},
	{SkinHoneycrisp, "Honeycrisp"},
}

// ParseSkin reads a submitted skin, falling back to the default for
// anything unrecognized.
//
// Unlike a banner level, an unknown skin is not worth failing a request
// over: the worst outcome is a page that looks like the default, which is
// a page that still works. A banner marking that silently changed would be
// a safety problem; a stylesheet that does is a cosmetic one.
func ParseSkin(raw string) Skin {
	s := Skin(strings.TrimSpace(strings.ToLower(raw)))
	switch s {
	case SkinLasVentanas, SkinHoneycrisp:
		return s
	default:
		return SkinBrutalist
	}
}

// SkinOption is one button in the appearance control.
type SkinOption struct {
	Value   string
	Label   string
	Pressed string
}

// SkinOptions returns the skin buttons, with the active one marked.
func (p PageModel) SkinOptions() []SkinOption {
	current := ParseSkin(p.Skin)

	out := make([]SkinOption, 0, len(skinLabels))
	for _, s := range skinLabels {
		pressed := "false"
		if s.Skin == current {
			pressed = "true"
		}
		out = append(out, SkinOption{Value: string(s.Skin), Label: s.Label, Pressed: pressed})
	}
	return out
}

// AccessibleMode is the explicit, in-app accessibility override.
//
// It is a third axis rather than a fourth theme, and it exists because the
// operating system cannot express everything a user needs. prefers-contrast
// and prefers-reduced-motion cover part of it and are honoured
// independently in the stylesheet; this covers the rest, and covers the
// user who wants a different setting in this application than they want
// system-wide.
//
// It is never a degraded mode. Every feature, every column, every action
// stays exactly where it was -- only the rendering changes. A setting that
// removed capability would be a bug, not a trade-off.
func (p PageModel) AccessibleMode() string {
	if p.A11y {
		return "true"
	}
	return "false"
}

// A11yPressed is the aria-pressed value for the accessibility toggle.
func (p PageModel) A11yPressed() string { return p.AccessibleMode() }

// A11yNext is the value the toggle submits, which is the opposite of the
// current state. Computing it here rather than in the template keeps the
// template free of the one decision it would otherwise have to make.
func (p PageModel) A11yNext() string {
	if p.A11y {
		return "off"
	}
	return "on"
}

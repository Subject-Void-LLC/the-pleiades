// Package gnmi: the rule a path's key value is held to, frozen so it never
// moves with a Go release, and the rule a node being created is held to
// beside it, which reads Go's own Unicode tables.
//
// A key value is refused a control character and an invisible format
// character, so it cannot hide in a log, a journal or a terminal what it
// names. A path is checked when a task is read and again when its recorded
// undo is replayed, which may be under a later Go release whose Unicode
// tables call a character a format character that the earlier one did not
// know at all (Unicode 15.0 added U+13439 to U+1343F, for one). A rule
// reading those tables could then refuse the undo it recorded. So Validate
// reads only a fixed list, the format characters of Unicode 17.0.0 (Go
// 1.27's tables) written out here, and CheckNew, asked only where a write
// creates a node, adds whatever the running release's tables call a format
// character, as the device-name rule reads Go's tables at creation only.
package gnmi

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// formatCharacters are the format characters (general category Cf) of
// Unicode 17.0.0, as Go 1.27's unicode.Cf lists them, in order. It never
// grows: a character a later Unicode makes a format character is refused by
// CheckNew alone, so no path an earlier release accepted is ever refused.
var formatCharacters = []struct{ lo, hi rune }{
	{0x00AD, 0x00AD},   // soft hyphen
	{0x0600, 0x0605},   // Arabic number signs
	{0x061C, 0x061C},   // Arabic letter mark
	{0x06DD, 0x06DD},   // Arabic end of ayah
	{0x070F, 0x070F},   // Syriac abbreviation mark
	{0x0890, 0x0891},   // Arabic pound and piastre marks above
	{0x08E2, 0x08E2},   // Arabic disputed end of ayah
	{0x180E, 0x180E},   // Mongolian vowel separator
	{0x200B, 0x200F},   // zero-width space and joiners, direction marks
	{0x202A, 0x202E},   // direction embeddings and overrides
	{0x2060, 0x2064},   // word joiner, invisible operators
	{0x2066, 0x206F},   // direction isolates, deprecated format characters
	{0xFEFF, 0xFEFF},   // zero-width no-break space
	{0xFFF9, 0xFFFB},   // interlinear annotation
	{0x110BD, 0x110BD}, // Kaithi number sign
	{0x110CD, 0x110CD}, // Kaithi number sign above
	{0x13430, 0x1343F}, // Egyptian hieroglyph format controls
	{0x1BCA0, 0x1BCA3}, // shorthand format controls
	{0x1D173, 0x1D17A}, // musical symbol format controls
	{0xE0001, 0xE0001}, // language tag
	{0xE0020, 0xE007F}, // tag characters
}

// frozenRefuses reports whether a key value may not hold r under the frozen
// rule: a C0 control, DEL, a C1 control (Unicode's control characters, a set
// its stability policy fixes), or one of formatCharacters.
func frozenRefuses(r rune) bool {
	if r <= 0x1F || (r >= 0x7F && r <= 0x9F) {
		return true
	}
	for _, f := range formatCharacters {
		if r >= f.lo && r <= f.hi {
			return true
		}
	}
	return false
}

// validKeyValue checks a key value: bounded UTF-8 holding nothing
// frozenRefuses refuses, and the wildcard only where allowed.
func validKeyValue(v string, allowWildcards bool) error {
	if v == wildcardValue && !allowWildcards {
		return errors.New("the wildcard * names no one entry, and this path must")
	}
	if len(v) > MaxKeyBytes {
		return fmt.Errorf("the value is longer than %d bytes", MaxKeyBytes)
	}
	if !utf8.ValidString(v) {
		return errors.New("the value is not UTF-8")
	}
	for _, r := range v {
		if frozenRefuses(r) {
			return fmt.Errorf("the value holds the control character %U", r)
		}
	}
	return nil
}

// CheckNew is the rule a path naming a node a write creates is held to:
// Validate's for one node (no wildcard), and no key value holding a
// character the running Go release's Unicode tables call a format
// character. It reads those tables, which a release can grow, so it is asked
// only of a path being written for the first time (net.gnmi.config's update
// and replace), never of one read back, from a journal, a device or a
// recorded undo, which Validate alone reads.
func (p Path) CheckNew() error {
	if err := p.Validate(false); err != nil {
		return err
	}
	for _, e := range p.Elems {
		for name, value := range e.Keys {
			for _, r := range value {
				if unicode.Is(unicode.Cf, r) {
					return fmt.Errorf("key %s of %s: the value holds the format character %U", name, e.Name, r)
				}
			}
		}
	}
	return nil
}

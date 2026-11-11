// Package wire: the rule a process holds a Collection identity another
// process made to before it stores it (Deliverable).
//
// This file imports no unicode package on purpose: its rule is fixed code
// point ranges, so two builds made with different Go releases answer the
// same for every identity, and a stored row cannot depend on which build
// stored it (LESSONS 415, 422).
package wire

import (
	"fmt"
	"strings"
)

// Deliverable holds an identity another process made, and this one is about
// to store, to Shape and a frozen list of characters that would make its
// group read as something else where it is printed, or show as nothing:
//
//   - the bidirectional controls: U+061C, U+200E-U+200F, U+202A-U+202E,
//     U+2066-U+2069;
//   - the zero width characters and joiners: U+200B-U+200D, U+2060, U+FEFF;
//   - the line and paragraph separators U+2028-U+2029, the soft hyphen
//     U+00AD and the Mongolian vowel separator U+180E;
//   - the Hangul fillers U+115F, U+1160, U+3164, U+FFA0;
//   - the variation selectors: U+180B-U+180D, U+180F, U+FE00-U+FE0F,
//     U+E0100-U+E01EF;
//   - the other format characters: the invisible math operators
//     U+2061-U+2064, the deprecated U+206A-U+206F, the interlinear
//     annotation marks U+FFF9-U+FFFB, the shorthand format controls
//     U+1BCA0-U+1BCA3 and the musical format controls U+1D173-U+1D17A;
//   - the tag characters U+E0001 and U+E0020-U+E007F, which spell a hidden
//     word after a visible one;
//   - private use: U+E000-U+F8FF, U+F0000-U+FFFFD, U+100000-U+10FFFD.
//
// It is the Controller's rule for a Runner's facts batch and journal rows:
// the Controller stores them, so it refuses what would mislead a reader,
// and it never asks Valid, whose answer moves with the Go release that
// built it. Every character it refuses Valid refuses too
// (TestFrozenRuleIsSubsetOfValid), so it turns away nothing a build wrote.
// What it keeps that Valid refuses is a character Valid's tables have not
// assigned, which a newer build's tables may call a letter. The list is
// frozen wire contract: it was set before any release published facts, and
// does not grow. A reader of a stored identity asks Shape alone.
//
// The zero identity does not pass: a caller checks IsZero first.
func (id CollectionIdentity) Deliverable() error {
	if err := id.Shape(); err != nil {
		return err
	}
	if strings.ContainsFunc(id.Group, misleadingCharacter) {
		// Fixed text: the group is delivered bytes, and this refusal is
		// printed where an operator reads it.
		return fmt.Errorf("%w: its group holds a direction control, an invisible or private-use character or a line separator", ErrInvalidIdentity)
	}
	return nil
}

// misleadingCharacter reports whether r is one of Deliverable's frozen
// characters. It compares code point numbers only, so no Go release can
// change its answer.
func misleadingCharacter(r rune) bool {
	switch {
	case r == 0x00AD: // soft hyphen
		return true
	case r == 0x061C: // Arabic letter mark
		return true
	case r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0: // Hangul fillers
		return true
	case r >= 0x180B && r <= 0x180F: // Mongolian variation selectors and vowel separator
		return true
	case r >= 0x200B && r <= 0x200F: // zero width space, joiners, LRM, RLM
		return true
	case r >= 0x2028 && r <= 0x202E: // line and paragraph separators, embeddings and overrides
		return true
	case r >= 0x2060 && r <= 0x2064: // word joiner, invisible math operators
		return true
	case r >= 0x2066 && r <= 0x206F: // isolates, deprecated format characters
		return true
	case r >= 0xE000 && r <= 0xF8FF: // private use
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return true
	case r == 0xFEFF: // zero width no-break space, the byte order mark
		return true
	case r >= 0xFFF9 && r <= 0xFFFB: // interlinear annotation
		return true
	case r >= 0x1BCA0 && r <= 0x1BCA3: // shorthand format controls
		return true
	case r >= 0x1D173 && r <= 0x1D17A: // musical symbol format controls
		return true
	case r == 0xE0001, r >= 0xE0020 && r <= 0xE007F: // tag characters
		return true
	case r >= 0xE0100 && r <= 0xE01EF: // variation selectors supplement
		return true
	case r >= 0xF0000 && r <= 0xFFFFD, r >= 0x100000 && r <= 0x10FFFD: // supplementary private use
		return true
	}
	return false
}

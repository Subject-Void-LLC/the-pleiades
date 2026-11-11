// Tests for Deliverable, the frozen rule a process holds an identity another
// process made to before storing it, and for refusals that repeat none of
// the bytes an identity was delivered with.
package wire

import (
	"errors"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// frozenRanges is every code point Deliverable refuses beyond Shape, as the
// inclusive ranges the owner's list names, written apart from
// misleadingCharacter so a change to its switch shows here.
var frozenRanges = [][2]rune{
	{0x00AD, 0x00AD},     // soft hyphen
	{0x061C, 0x061C},     // Arabic letter mark
	{0x115F, 0x1160},     // Hangul fillers
	{0x180B, 0x180D},     // Mongolian free variation selectors
	{0x180E, 0x180E},     // Mongolian vowel separator
	{0x180F, 0x180F},     // Mongolian free variation selector four
	{0x200B, 0x200F},     // zero width space, joiners, LRM, RLM
	{0x2028, 0x202E},     // line and paragraph separators, embeddings, overrides
	{0x2060, 0x2060},     // word joiner
	{0x2061, 0x2064},     // invisible math operators
	{0x2066, 0x2069},     // isolates
	{0x206A, 0x206F},     // deprecated format characters
	{0x3164, 0x3164},     // Hangul filler
	{0xE000, 0xF8FF},     // private use
	{0xFE00, 0xFE0F},     // variation selectors
	{0xFEFF, 0xFEFF},     // byte order mark
	{0xFFA0, 0xFFA0},     // halfwidth Hangul filler
	{0xFFF9, 0xFFFB},     // interlinear annotation
	{0x1BCA0, 0x1BCA3},   // shorthand format controls
	{0x1D173, 0x1D17A},   // musical symbol format controls
	{0xE0001, 0xE0001},   // language tag
	{0xE0020, 0xE007F},   // tag characters
	{0xE0100, 0xE01EF},   // variation selectors supplement
	{0xF0000, 0xFFFFD},   // supplementary private use A
	{0x100000, 0x10FFFD}, // supplementary private use B
}

// inFrozenRanges reports whether r is in frozenRanges, and how many code
// points the ranges hold.
func inFrozenRanges(r rune) bool {
	for _, span := range frozenRanges {
		if r >= span[0] && r <= span[1] {
			return true
		}
	}
	return false
}

// unassignedHere finds a code point this Go release's tables give no
// category and the frozen list does not name, which is how a character a
// newer Go has assigned looks to this build: the case of a character two
// toolchains classify differently. It is searched for, so a later Unicode
// cannot stale the tests.
func unassignedHere(t *testing.T) rune {
	t.Helper()
	for r := rune(0xA0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF || inFrozenRanges(r) {
			continue
		}
		if unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z,
			unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs, unicode.Noncharacter_Code_Point) {
			continue
		}
		return r
	}
	t.Fatal("this Go release assigns every code point")
	return 0
}

// TestDeliverable_RefusesExactlyTheFrozenList: over every code point, a
// group holding it passes Deliverable unless it is a control code (Shape's)
// or in the frozen list, and the refusal names neither the group nor the
// character.
func TestDeliverable_RefusesExactlyTheFrozenList(t *testing.T) {
	refused, want := 0, 0
	for _, span := range frozenRanges {
		want += int(span[1]-span[0]) + 1
	}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // a surrogate is no character; string(r) is U+FFFD
		}
		if r == 0 || r == '/' {
			continue // Shape refuses a NUL and a slash as no file name
		}
		id := CollectionIdentity{Group: withRune(r), Program: true, Manifests: validDigest}
		err := id.Deliverable()
		listed := inFrozenRanges(r)
		if (err != nil) != (listed || controlCode(r)) {
			t.Errorf("U+%04X: Deliverable gave %v, want refused %v", r, err, listed || controlCode(r))
			continue
		}
		if listed {
			refused++
			if !errors.Is(err, ErrInvalidIdentity) || strings.Contains(err.Error(), "acme") || strings.ContainsRune(err.Error(), r) {
				t.Errorf("U+%04X: refusal %q repeats the group", r, err)
			}
		}
	}
	if refused != want {
		t.Errorf("Deliverable refused %d of the frozen list's %d characters", refused, want)
	}
}

// TestDeliverable_RefusesATagCharacterGroup: a group spelled with tag
// characters after a plain name, which shows as the name alone, is refused.
func TestDeliverable_RefusesATagCharacterGroup(t *testing.T) {
	for _, r := range []rune{0xE0041, 0xE0001, 0xE007F, 0xFE0F, 0xE0100, 0xE000, 0x10FFFD, 0x2062, 0xFFF9, 0x1BCA1, 0x1D173} {
		id := CollectionIdentity{Group: withRune(r), Program: true, Manifests: validDigest}
		if err := id.Deliverable(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("a group holding U+%04X: Deliverable gave %v, want it refused", r, err)
		}
	}
}

// TestFrozenRuleIsSubsetOfValid: every identity Deliverable refuses, Valid
// refuses too, over every code point in the group, so a Controller holding a
// Runner's identity to Deliverable turns away nothing a build that holds
// itself to Valid wrote. A code point this toolchain has not assigned shows
// the other direction: Valid refuses it here and Deliverable keeps it, as a
// Controller built with an older Go must keep a group a newer Runner's
// tables call a letter.
func TestFrozenRuleIsSubsetOfValid(t *testing.T) {
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if misleadingCharacter(r) && !controlOrFormat(r) {
			t.Errorf("U+%04X: the frozen rule refuses it and Valid's check admits it", r)
		}
		id := CollectionIdentity{Group: withRune(r), Program: true, Manifests: validDigest}
		if id.Deliverable() != nil && id.Valid() == nil {
			t.Errorf("U+%04X: Deliverable refuses a group Valid accepts", r)
		}
	}
	r := unassignedHere(t)
	unassigned := CollectionIdentity{Group: withRune(r), Program: true, Manifests: validDigest}
	if err := unassigned.Deliverable(); err != nil {
		t.Errorf("a group holding the unassigned U+%04X: Deliverable refused it: %v", r, err)
	}
	if err := unassigned.Valid(); !errors.Is(err, ErrInvalidIdentity) {
		t.Errorf("a group holding the unassigned U+%04X: Valid gave %v, want it refused", r, err)
	}
	for _, id := range []CollectionIdentity{
		{Group: "file", Version: "0.4.0", Manifests: validDigest},
		{Group: withRune(0xE9), Program: true, Manifests: validDigest},
	} {
		if err := id.Deliverable(); err != nil {
			t.Errorf("%+v: Deliverable refused an identity a build writes: %v", id, err)
		}
	}
}

// TestDeliverableReadsNoUnicodeTable: the file that holds the frozen rule
// imports no unicode package, so its answer cannot move with the toolchain.
func TestDeliverableReadsNoUnicodeTable(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "deliverable.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, "unicode") {
			t.Errorf("deliverable.go imports %s", path)
		}
	}
}

// TestRefusalsRepeatNothingDelivered: every refusal Shape, Valid and
// Deliverable make is fixed text: none repeats the group, the version or the
// manifest digest it was given, however long, so delivered bytes never reach
// a log line or an operator through one.
func TestRefusalsRepeatNothingDelivered(t *testing.T) {
	const marker = "zq9marker"
	huge := strings.Repeat(marker, 1<<20/len(marker))
	for name, id := range map[string]CollectionIdentity{
		"a group that is not UTF-8":     {Group: marker + "\xff", Manifests: validDigest},
		"a group with a slash":          {Group: marker + "/x", Manifests: validDigest},
		"a group with an escape":        {Group: marker + "\x1b[2J", Program: true, Manifests: validDigest},
		"a group with an override":      {Group: marker + string(rune(0x202E)), Program: true, Manifests: validDigest},
		"a group with a filler":         {Group: marker + string(rune(0x3164)), Program: true, Manifests: validDigest},
		"a program with a version":      {Group: "acme", Program: true, Version: marker, Manifests: validDigest},
		"a version outside ASCII":       {Group: "file", Version: marker + " rc", Manifests: validDigest},
		"a manifest digest of 1 MB":     {Group: "file", Manifests: huge},
		"a manifest digest with marker": {Group: "file", Manifests: "sha256:" + marker},
	} {
		for check, err := range map[string]error{"Shape": id.Shape(), "Valid": id.Valid(), "Deliverable": id.Deliverable()} {
			if err == nil {
				continue
			}
			if !errors.Is(err, ErrInvalidIdentity) || strings.Contains(err.Error(), marker) || len(err.Error()) > 200 {
				t.Errorf("%s: %s's refusal repeats what it was given (%d bytes): %.120q", name, check, len(err.Error()), err.Error())
			}
		}
		if id.Valid() == nil {
			t.Errorf("%s: Valid accepted it", name)
		}
	}
}

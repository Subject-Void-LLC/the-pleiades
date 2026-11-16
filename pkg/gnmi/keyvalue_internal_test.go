// Package gnmi: the key value rule a path is read by is frozen. It refuses
// what it refused under Go 1.27 and nothing more, whatever a later release's
// Unicode tables say, while the rule a new node is held to reads the tables.
package gnmi

import (
	"slices"
	"testing"
	"unicode"
)

// firstUnassigned finds a code point this release's tables give no
// category, which is how a character a later Unicode assigns looks now. It
// is searched for, so a later Unicode cannot stale the test.
func firstUnassigned(t *testing.T) rune {
	t.Helper()
	for r := rune(0xA0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
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

// withPoint is table with r added, as a later Unicode would list it: every
// code point table holds and r, each its own range, in order, which
// unicode.Is's search relies on.
func withPoint(table *unicode.RangeTable, r rune) *unicode.RangeTable {
	points := []rune{r}
	for c := rune(0); c <= unicode.MaxRune; c++ {
		if unicode.Is(table, c) {
			points = append(points, c)
		}
	}
	slices.Sort(points)
	out := &unicode.RangeTable{}
	for _, c := range points {
		if c <= 0xFFFF {
			out.R16 = append(out.R16, unicode.Range16{Lo: uint16(c), Hi: uint16(c), Stride: 1}) // #nosec G115 -- checked to fit
			continue
		}
		out.R32 = append(out.R32, unicode.Range32{Lo: uint32(c), Hi: uint32(c), Stride: 1}) // #nosec G115 -- a code point is not negative
	}
	return out
}

// TestKeyValueRule_DoesNotMoveWithGosTables stands in a later Go release
// whose tables call a character a format character that this one leaves
// unassigned, and holds a path holding it, as a recorded undo would, to
// still parsing, while a path a write creates is refused it. Run before the
// rule was frozen, the path stopped parsing.
func TestKeyValueRule_DoesNotMoveWithGosTables(t *testing.T) {
	r := firstUnassigned(t)
	path := "/interface[name=lo" + string(r) + "]"
	if _, err := ParsePath(path); err != nil {
		t.Fatalf("ParsePath(%q) = %v under this release's tables", path, err)
	}
	// No test in this binary runs beside this one (none calls t.Parallel),
	// so nothing else reads unicode.Cf while it is swapped.
	tables := unicode.Cf
	unicode.Cf = withPoint(tables, r)
	defer func() { unicode.Cf = tables }()
	if !unicode.Is(unicode.Cf, r) {
		t.Fatalf("the grown table does not hold %U, so the test proves nothing", r)
	}
	p, err := ParsePath(path)
	if err != nil {
		t.Errorf("ParsePath(%q) = %v once a later release calls %U a format character: a recorded undo would stop parsing", path, err, r)
	}
	if err := p.CheckNew(); err == nil {
		t.Errorf("CheckNew(%q) accepted %U, which the running tables call a format character", path, r)
	}
}

// TestFormatCharacters_AreUnicode17s holds the frozen list to Go's own
// table where it was taken from: under Unicode 17.0.0, exactly unicode.Cf;
// under a later one, still inside it (a character leaving Cf is worth a
// person's look), the newly listed ones named and left to CheckNew.
func TestFormatCharacters_AreUnicode17s(t *testing.T) {
	for _, f := range formatCharacters {
		for r := f.lo; r <= f.hi; r++ {
			if !unicode.Is(unicode.Cf, r) {
				t.Errorf("%U is frozen as a format character, and Unicode %s does not call it one", r, unicode.Version)
			}
		}
	}
	var newer []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.Is(unicode.Cf, r) && !frozenRefuses(r) {
			newer = append(newer, r)
		}
	}
	switch {
	case unicode.Version == "17.0.0" && len(newer) > 0:
		t.Errorf("Unicode 17.0.0 calls %U format characters, and the frozen list leaves them out", newer)
	case len(newer) > 0:
		t.Logf("Unicode %s adds format characters %U, refused at creation by CheckNew alone", unicode.Version, newer)
	}
}

// TestValidKeyValue_RefusesControlsAndFormatCharacters holds the frozen rule
// to every character it names, and to nothing beside them in the ranges it
// covers.
func TestValidKeyValue_RefusesControlsAndFormatCharacters(t *testing.T) {
	for _, r := range []rune{0x00, 0x1B, 0x7F, 0x85, 0x9F, 0x00AD, 0x061C, 0x200B, 0x200E, 0x202E, 0x2066, 0xFEFF, 0xE0001, 0xE007F} {
		if err := validKeyValue("a"+string(r), false); err == nil {
			t.Errorf("%U accepted", r)
		}
	}
	for _, r := range []rune{' ', 'é', 0xA0, 0x2010, 0x2065, 0xE0000, 0xE0080} {
		if err := validKeyValue("a"+string(r), false); err != nil {
			t.Errorf("%U refused: %v", r, err)
		}
	}
}

// Package facts_test: the reader's key rule is frozen. It asks only a code
// point's number, so no Go upgrade can change what a stored key reads as,
// while the rule a new fact's key is held to (ValidKey) is the one it was.
package facts_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// keyHolding is a key with r between two ordinary characters.
func keyHolding(r rune) string { return "k" + string(r) + "1" }

// eachScalar calls f for every code point valid UTF-8 can encode.
func eachScalar(f func(r rune)) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // a surrogate is no character; string(r) is U+FFFD
		}
		f(r)
	}
}

// frozenRefuses is the reader's whole per-character rule, as arithmetic: C0,
// U+0020, the slash, DEL and C1.
func frozenRefuses(r rune) bool {
	return r <= 0x20 || r == 0x2F || (r >= 0x7F && r <= 0x9F)
}

// readerBefore is wellFormedKey as it was before it was frozen, when it read
// Go's unicode tables.
func readerBefore(key string) bool {
	if key == "" || len(key) > facts.MaxKeyBytes || !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) || r == '/' {
			return false
		}
	}
	return true
}

// validBefore is ValidKey as it was before the freeze.
func validBefore(key string) bool {
	return readerBefore(key) && recordname.Addressable(key) && recordname.ShowsAsItself(key) == nil
}

// capped reports the first ten failures of a sweep and fails on the rest
// without printing them, so a broken rule does not print a million lines.
func capped(t *testing.T) func(format string, args ...any) {
	n := 0
	return func(format string, args ...any) {
		t.Helper()
		if n++; n <= 10 {
			t.Errorf(format, args...)
			return
		}
		t.Fail()
	}
}

// firstUnassigned finds a code point this Go release's tables give no
// category, which is how a rune a newer Go has assigned looks to this
// reader (LESSONS 422). It is searched for, so a later Unicode cannot stale
// the test.
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

// TestReadableKey_AsksOnlyACodePointsNumber: for every code point the reader
// answers what fixed arithmetic says. No table is consulted, so a
// reclassification in a later Go release, out of a graphic category or into
// one, cannot change whether a stored key reads.
func TestReadableKey_AsksOnlyACodePointsNumber(t *testing.T) {
	report := capped(t)
	refused := 0
	eachScalar(func(r rune) {
		want := !frozenRefuses(r)
		if !want {
			refused++
		}
		if got := facts.ReadableKey(keyHolding(r)); got != want {
			report("ReadableKey(key holding %U) = %v, want %v", r, got, want)
		}
	})
	// 32 C0, U+0020, the slash, DEL and 32 C1.
	if refused != 67 {
		t.Fatalf("the fixed rule refuses %d code points, want 67", refused)
	}
}

// TestFrozenReader_OnlyWidensReadingAndLeavesCreationAlone: every key the
// reader read before, and every key ValidKey accepts, still reads; and
// ValidKey is, for every code point, exactly the rule it was. The last
// catches a freeze that drops the printable check from creation.
func TestFrozenReader_OnlyWidensReadingAndLeavesCreationAlone(t *testing.T) {
	report := capped(t)
	newly := 0
	eachScalar(func(r rune) {
		key := keyHolding(r)
		before, now, valid := readerBefore(key), facts.ReadableKey(key), facts.ValidKey(key)
		if before && !now {
			report("a key holding %U read before the freeze and does not now", r)
		}
		if valid && !now {
			report("ValidKey accepts a key holding %U that ReadableKey refuses", r)
		}
		if want := validBefore(key); valid != want {
			report("ValidKey(key holding %U) = %v, was %v before the freeze", r, valid, want)
		}
		if now && !before {
			newly++
		}
	})
	if newly == 0 {
		t.Fatal("the sweep found nothing newly readable, so it did not compare the two readers")
	}
	for _, key := range []string{"", ".", "..", "...", "os.family", strings.Repeat("a", facts.MaxKeyBytes+1)} {
		if want := validBefore(key); facts.ValidKey(key) != want {
			t.Errorf("ValidKey(%q) = %v, was %v before the freeze", key, !want, want)
		}
	}
}

// TestReadableKey_ReadsACodePointThisGoHasNotAssigned: a newer-Go writer can
// store a key holding a rune this binary's tables do not know, and it reads;
// no new fact may be named with it here.
func TestReadableKey_ReadsACodePointThisGoHasNotAssigned(t *testing.T) {
	r := firstUnassigned(t)
	key := "package." + string(r) + ".version"
	if !utf8.ValidString(key) || unicode.IsPrint(r) {
		t.Fatalf("%U is not an unassigned, encodable code point here", r)
	}
	if !facts.ReadableKey(key) {
		t.Errorf("a key holding the unassigned %U is not readable", r)
	}
	if facts.ValidKey(key) {
		t.Errorf("a new fact may be named with the unassigned %U", r)
	}
}

// TestReadableKey_RefusesControlsSpaceAndSlash, at the start, middle and end
// of a key, with the code points on either side of each range kept.
func TestReadableKey_RefusesControlsSpaceAndSlash(t *testing.T) {
	for _, r := range []rune{0x00, 0x09, 0x0A, 0x0D, 0x1B, 0x1F, 0x20, 0x2F, 0x7F, 0x80, 0x85, 0x9B, 0x9F} {
		for _, key := range []string{string(r) + "k", keyHolding(r), "k" + string(r)} {
			if facts.ReadableKey(key) {
				t.Errorf("ReadableKey reads a key holding %U (% x)", r, key)
			}
		}
	}
	for _, r := range []rune{0x21, 0x2E, 0x30, 0x7E, 0xA0, 0xA1} {
		for _, key := range []string{string(r) + "k", keyHolding(r), "k" + string(r)} {
			if !facts.ReadableKey(key) {
				t.Errorf("ReadableKey refuses a key holding %U (% x)", r, key)
			}
		}
	}
}

// TestReadableKey_ReadsWhateverClassACharacterLandsIn: one character of each
// class a stored key's character could be moved into, or that ValidKey
// refuses by table, reads all the same; none may name a new fact.
func TestReadableKey_ReadsWhateverClassACharacterLandsIn(t *testing.T) {
	for name, r := range map[string]rune{
		"U+180E, Zs until Unicode 6.3 and Cf since": 0x180E,
		"zero width non-joiner (Cf)":                0x200C,
		"right-to-left override (Cf)":               0x202E,
		"no-break space (Zs)":                       0x00A0,
		"line separator (Zl)":                       0x2028,
		"private use (Co)":                          0xE000,
		"noncharacter":                              0xFFFE,
		"the last code point":                       0x10FFFF,
		"blank braille (So)":                        0x2800,
		"variation selector 16 (Mn)":                0xFE0F,
		"Hangul choseong filler (Lo)":               0x115F,
	} {
		key := keyHolding(r)
		if !facts.ReadableKey(key) {
			t.Errorf("%s: a stored key holding %U is not readable", name, r)
		}
		if facts.ValidKey(key) {
			t.Errorf("%s: a new fact may be named with %U", name, r)
		}
	}
}

// TestReadableKey_KeepsItsFixedShape: empty, the length bound in bytes, and
// UTF-8 validity, with a real U+FFFD kept (FAILURE_PATTERNS 932).
func TestReadableKey_KeepsItsFixedShape(t *testing.T) {
	eAcute := string(rune(0xE9)) // two bytes
	for name, c := range map[string]struct {
		key  string
		want bool
	}{
		"empty":                       {"", false},
		"at the bound":                {strings.Repeat("a", facts.MaxKeyBytes), true},
		"past the bound":              {strings.Repeat("a", facts.MaxKeyBytes+1), false},
		"at the bound, two-byte end":  {strings.Repeat("a", facts.MaxKeyBytes-2) + eAcute, true},
		"past the bound by one byte":  {strings.Repeat("a", facts.MaxKeyBytes-1) + eAcute, false},
		"a byte that is not UTF-8":    {string([]byte{'a', 0xFF, 'b'}), false},
		"a truncated character":       {"a" + eAcute[:1], false},
		"an encoded surrogate":        {string([]byte{'a', 0xED, 0xA0, 0x80}), false},
		"an overlong slash":           {string([]byte{'a', 0xC0, 0xAF, 'b'}), false},
		"a real U+FFFD":               {"package.a" + string(rune(0xFFFD)) + "b", true},
		"dot, stored by old releases": {".", true},
		"dot dot":                     {"..", true},
	} {
		if got := facts.ReadableKey(c.key); got != c.want {
			t.Errorf("%s: ReadableKey(% x) = %v, want %v", name, c.key, got, c.want)
		}
	}
}

// TestFileStore_ReadsAKeyThisToolchainCallsUnprintable: the CLI's file store
// reads back a history line whose key holds a code point this binary's
// tables have not assigned (as a newer Go's newly assigned letter looks to an
// older one, the one kind of key a writer of another build can store that
// this build's ValidKey refuses), where the line reader held keys to the
// tables and called the whole history corrupt. Record checks no key, as a
// writer of another build stores what its own ValidKey allowed.
func TestFileStore_ReadsAKeyThisToolchainCallsUnprintable(t *testing.T) {
	store, err := facts.NewFileStore(filepath.Join(t.TempDir(), "facts"))
	if err != nil {
		t.Fatal(err)
	}
	r := firstUnassigned(t)
	key := "package.a" + string(r) + "b"
	if unicode.IsPrint(r) {
		t.Fatal("the fixture needs a character this toolchain does not call printable")
	}
	hash, err := facts.Hash(key, "1.0")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Record(ctx, "d1", facts.Observation{Entries: []facts.Entry{{Key: key, Value: "1.0", Hash: hash}}, At: time.Now()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	current, err := store.Current(ctx, "d1")
	if err != nil {
		t.Fatalf("Current over a history holding the key: %v", err)
	}
	if len(current) != 1 || current[0].Key != key {
		t.Errorf("Current = %+v, want the stored fact", current)
	}
	history, err := store.History(ctx, "d1", key)
	if err != nil || len(history) != 1 {
		t.Errorf("History = %+v, %v; want the one stored value", history, err)
	}
	if v, err := store.Verify(ctx, "d1"); err != nil || v.Break != nil {
		t.Errorf("Verify = %+v, %v; want the chain unbroken", v, err)
	}
}

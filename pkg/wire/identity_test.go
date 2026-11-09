// Tests for CollectionIdentity: its zero value writes no key under omitzero,
// Valid accepts exactly the shapes a build writes, and Shape is the part of
// Valid no Go release can change.
package wire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// validDigest is a well-formed manifest digest.
var validDigest = ManifestDigestPrefix + strings.Repeat("0a", 32)

// TestCollectionIdentity_ZeroWritesNoKey: a carrier with omitzero leaves a
// zero identity out entirely, so a record written before the field reads the
// same bytes, and a set identity writes its three keys.
func TestCollectionIdentity_ZeroWritesNoKey(t *testing.T) {
	type carrier struct {
		Name       string             `json:"name"`
		Collection CollectionIdentity `json:"collection,omitzero"`
	}
	zero, err := json.Marshal(carrier{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if string(zero) != `{"name":"a"}` {
		t.Errorf("zero identity wrote %s, want no collection key", zero)
	}
	set, err := json.Marshal(carrier{Name: "a", Collection: CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: validDigest}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"a","collection":{"group":"file","version":"0.4.0","manifests":"` + validDigest + `"}}`
	if string(set) != want {
		t.Errorf("set identity wrote %s, want %s", set, want)
	}
	// An external program's identity has no version, and writes no version key.
	external, err := json.Marshal(CollectionIdentity{Group: "acme", Manifests: validDigest})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(external), "version") {
		t.Errorf("an identity with no version wrote %s", external)
	}
	if !(CollectionIdentity{}).IsZero() || (CollectionIdentity{Group: "file"}).IsZero() {
		t.Error("IsZero is not true exactly for the zero identity")
	}
}

// TestCollectionIdentity_Valid accepts a built-in's and an external
// program's identity and refuses each malformed field by name.
func TestCollectionIdentity_Valid(t *testing.T) {
	good := []CollectionIdentity{
		{Group: "file", Version: "0.4.0", Manifests: validDigest},
		{Group: "virt.clab", Version: "0.0.0-dev+0123456789ab.dirty", Manifests: validDigest},
		{Group: "acme-widgets", Manifests: validDigest},
		{Group: strings.Repeat("g", MaxIdentityGroupBytes), Version: strings.Repeat("1", MaxIdentityVersionBytes), Manifests: validDigest},
	}
	for _, id := range good {
		if err := id.Valid(); err != nil {
			t.Errorf("%+v: %v", id, err)
		}
	}
	bad := map[string]CollectionIdentity{
		"zero":                            {},
		"no group":                        {Version: "0.4.0", Manifests: validDigest},
		"long group":                      {Group: strings.Repeat("g", MaxIdentityGroupBytes+1), Manifests: validDigest},
		"group with a slash":              {Group: "a/b", Manifests: validDigest},
		"group with a NUL":                {Group: "a\x00b", Manifests: validDigest},
		"group not UTF-8":                 {Group: "a\xffb", Manifests: validDigest},
		"group with escape":               {Group: "acme\x1b[2J", Program: true, Manifests: validDigest},
		"group with newline":              {Group: "acme\n", Program: true, Manifests: validDigest},
		"group with override":             {Group: "acme\u202e", Program: true, Manifests: validDigest},
		"group with a line separator":     {Group: "acme\u2028file", Program: true, Manifests: validDigest},
		"group with a Hangul filler":      {Group: "acme\u3164", Program: true, Manifests: validDigest},
		"group with a variation selector": {Group: "acme\ufe0f", Program: true, Manifests: validDigest},
		"a program with a version":        {Group: "acme", Program: true, Version: "1.0", Manifests: validDigest},
		"long version":                    {Group: "file", Version: strings.Repeat("1", MaxIdentityVersionBytes+1), Manifests: validDigest},
		"version with space":              {Group: "file", Version: "0.4.0 rc", Manifests: validDigest},
		"version with escape":             {Group: "file", Version: "0.4.0\x1b[2J", Manifests: validDigest},
		"no digest":                       {Group: "file", Version: "0.4.0"},
		"63 hex digits":                   {Group: "file", Manifests: validDigest[:len(validDigest)-1]},
		"upper-case hex":                  {Group: "file", Manifests: validDigest[:len(validDigest)-1] + "A"},
		"upper-case prefix":               {Group: "file", Manifests: "SHA256:" + strings.Repeat("0a", 32)},
		"another algorithm":               {Group: "file", Manifests: "sha512:" + strings.Repeat("0a", 32)},
	}
	for name, id := range bad {
		if err := id.Valid(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("%s: Valid gave %v, want ErrInvalidIdentity", name, err)
		}
	}
}

// withRune is "acme" with the character r appended, built in code so the
// test's source holds no invisible character.
func withRune(r rune) string {
	return "acme" + string(r)
}

// TestCollectionIdentity_ShapeIsValidLessTheUnicodeTables: Shape, what a
// reader holds a stored identity to, refuses every fault Valid does except
// the ones only Go's Unicode tables decide, so an identity written under one
// toolchain is never unreadable under the next. A character those tables
// call format, invisible or a variation selector passes Shape and is still
// refused by Valid, which only a writer calls. A control code is refused by
// both, by its fixed code point.
func TestCollectionIdentity_ShapeIsValidLessTheUnicodeTables(t *testing.T) {
	tablesOnly := map[string]rune{
		"a direction override": 0x202E,
		"a line separator":     0x2028,
		"a Hangul filler":      0x3164,
		"a variation selector": 0xFE0F,
		"a zero-width joiner":  0x200D,
		"an unassigned point":  0x0378,
	}
	for name, r := range tablesOnly {
		id := CollectionIdentity{Group: withRune(r), Program: true, Manifests: validDigest}
		if err := id.Shape(); err != nil {
			t.Errorf("a group with %s: Shape refused it: %v", name, err)
		}
		if err := id.Valid(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("a group with %s: Valid gave %v, want ErrInvalidIdentity", name, err)
		}
	}
	both := map[string]CollectionIdentity{
		"zero":                     {},
		"no group":                 {Version: "0.4.0", Manifests: validDigest},
		"long group":               {Group: strings.Repeat("g", MaxIdentityGroupBytes+1), Manifests: validDigest},
		"group with a slash":       {Group: "a/b", Manifests: validDigest},
		"group with a NUL":         {Group: "a\x00b", Manifests: validDigest},
		"group not UTF-8":          {Group: "a\xffb", Manifests: validDigest},
		"group with escape":        {Group: "acme\x1b[2J", Program: true, Manifests: validDigest},
		"group with newline":       {Group: "acme\n", Program: true, Manifests: validDigest},
		"group with DEL":           {Group: "acme\x7f", Program: true, Manifests: validDigest},
		"group with a C1 code":     {Group: withRune(0x9B), Program: true, Manifests: validDigest},
		"a program with a version": {Group: "acme", Program: true, Version: "1.0", Manifests: validDigest},
		"long version":             {Group: "file", Version: strings.Repeat("1", MaxIdentityVersionBytes+1), Manifests: validDigest},
		"version with space":       {Group: "file", Version: "0.4.0 rc", Manifests: validDigest},
		"no digest":                {Group: "file", Version: "0.4.0"},
		"63 hex digits":            {Group: "file", Manifests: validDigest[:len(validDigest)-1]},
		"upper-case hex":           {Group: "file", Manifests: validDigest[:len(validDigest)-1] + "A"},
	}
	for name, id := range both {
		if err := id.Shape(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("%s: Shape gave %v, want ErrInvalidIdentity", name, err)
		}
		if err := id.Valid(); !errors.Is(err, ErrInvalidIdentity) {
			t.Errorf("%s: Valid gave %v, want ErrInvalidIdentity", name, err)
		}
	}
	for _, id := range []CollectionIdentity{
		{Group: "file", Version: "0.4.0", Manifests: validDigest},
		{Group: withRune(0xE9), Program: true, Manifests: validDigest},
	} {
		if err := id.Shape(); err != nil {
			t.Errorf("%+v: Shape refused a valid identity: %v", id, err)
		}
		if err := id.Valid(); err != nil {
			t.Errorf("%+v: Valid refused a valid identity: %v", id, err)
		}
	}
}

// TestControlCodeIsAlsoControlOrFormat: every rune Shape refuses as a
// control code, Valid's own check refuses too, over every code point, so
// Shape refuses nothing a writer holding an identity to Valid could have
// written.
func TestControlCodeIsAlsoControlOrFormat(t *testing.T) {
	refused := 0
	for r := rune(0); r <= 0x10FFFF; r++ {
		if controlCode(r) {
			refused++
			if !controlOrFormat(r) {
				t.Errorf("U+%04X is a control code Shape refuses and Valid's check admits", r)
			}
		}
	}
	if refused != 65 {
		t.Errorf("Shape refuses %d control codes, want the 65 of category Cc", refused)
	}
}

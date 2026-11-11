// Package entstore_test: a code point this toolchain has not assigned, the
// stand-in for a character two toolchains classify differently.
package entstore_test

import (
	"testing"
	"unicode"
)

// unassignedHere finds a code point this Go release's tables give no
// category, which is how a character a newer Go has assigned looks to this
// build. It is searched for, so a later Unicode cannot stale a test.
func unassignedHere(t *testing.T) rune {
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

// Internal tests for package iso9660's name rules: identifier ordering,
// ISO 9660 identifier derivation and collision handling, and the
// invariant checks on narrowing conversions.
package iso9660

import (
	"strings"
	"testing"
)

// TestCompareIdentifiers checks the ECMA-119 9.3 order, including the
// cases where it differs from comparing whole identifiers byte by byte.
func TestCompareIdentifiers(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		// ';' (0x3B) sorts after '1' (0x31) as a byte, but 9.3 compares
		// extensions padded with spaces, so "C" comes before "C1".
		{"AB.C;1", "AB.C1;1", -1},
		// The same holds for names: "A" padded is "A " and sorts first.
		{"A.;1", "A0.;1", -1},
		{"A.Z;1", "AA.B;1", -1},
		{"META_DATA.;1", "USER_DATA.;1", -1},
		{"user-data", "meta-data", 1},
		{"x", "x", 0},
		// Joliet names with no extension compare as names alone.
		{"network-config", "network-config2", -1},
	}
	for _, tc := range tests {
		if got := compareIdentifiers(tc.a, tc.b); sign(got) != tc.want {
			t.Errorf("compareIdentifiers(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
		if got := compareIdentifiers(tc.b, tc.a); sign(got) != -tc.want {
			t.Errorf("compareIdentifiers(%q, %q) = %d, want sign %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

// sign reduces a comparison result to -1, 0 or 1.
func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// TestAssignPrimaryIDs checks each derivation rule the package
// documentation states, including collisions and shortening.
func TestAssignPrimaryIDs(t *testing.T) {
	long := strings.Repeat("n", 40)
	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{"cloud-init seed", []string{"meta-data", "network-config", "user-data"},
			[]string{"META_DATA.;1", "NETWORK_CONFIG.;1", "USER_DATA.;1"}},
		{"extension kept", []string{"config.yaml"}, []string{"CONFIG.YAML;1"}},
		{"only the last dot separates", []string{"a.b.c"}, []string{"A_B.C;1"}},
		{"leading dot", []string{".hidden"}, []string{".HIDDEN;1"}},
		{"collision gets a suffix", []string{"a-b", "a_b"}, []string{"A_B.;1", "A_B_1.;1"}},
		{"suffix skips a taken name", []string{"a-b", "a_b", "a_b_1"}, []string{"A_B.;1", "A_B_1.;1", "A_B_1_1.;1"}},
		{"long name shortened", []string{long + ".extension"},
			[]string{strings.Repeat("N", 22) + ".EXTENSIO;1"}},
		{"shortened names collide", []string{long + "1", long + "2"},
			[]string{strings.Repeat("N", 30) + ".;1", strings.Repeat("N", 28) + "_1.;1"}},
		{"long extension alone", []string{"." + strings.Repeat("e", 40)},
			[]string{".EEEEEEEE;1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]entry, len(tc.names))
			for i, n := range tc.names {
				entries[i] = entry{name: n}
			}
			assignPrimaryIDs(entries)
			for i, e := range entries {
				if e.primary != tc.want[i] {
					t.Errorf("%q became %q, want %q", e.name, e.primary, tc.want[i])
				}
				base, ext := splitName(strings.TrimSuffix(e.primary, ";1"))
				if len(base)+len(ext) > maxPrimaryChars {
					t.Errorf("%q is longer than ECMA-119 7.5.1 allows", e.primary)
				}
			}
		})
	}
}

// TestNarrowingPanics checks the invariant guards on u8 and u32, which
// no valid input reaches, fail loudly rather than truncating.
func TestNarrowingPanics(t *testing.T) {
	for name, f := range map[string]func(){
		"u8 above 255":  func() { u8(256) },
		"u8 negative":   func() { u8(-1) },
		"u32 negative":  func() { u32(-1) },
		"u32 above max": func() { u32(1 << 32) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic for an out-of-range value")
				}
			}()
			f()
		})
	}
	if u8(255) != 255 || u32(1<<32-1) != 1<<32-1 {
		t.Fatal("the largest in-range values did not survive")
	}
}

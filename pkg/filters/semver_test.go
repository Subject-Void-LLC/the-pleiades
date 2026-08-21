package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestCompareSemVer(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"equal", "1.2.3", "1.2.3", 0},
		{"major_less", "1.9.9", "2.0.0", -1},
		{"major_greater", "2.0.0", "1.9.9", 1},
		{"minor_differs", "1.2.0", "1.3.0", -1},
		{"patch_differs", "1.2.3", "1.2.4", -1},
		{"v_prefix_ignored", "v1.2.3", "1.2.3", 0},
		{"prerelease_suffix_ignored", "1.2.3-rc1", "1.2.3", 0},
		{"build_suffix_ignored", "1.2.3+build5", "1.2.3", 0},
		{"missing_patch_is_zero", "1.2", "1.2.0", 0},
		{"missing_minor_and_patch_is_zero", "1", "1.0.0", 0},
		{"non_numeric_byte_truncates_rest_not_just_that_component", "1.x.3", "1.0.3", -1},
		{"non_numeric_byte_truncation_matches_bare_major", "1.x.3", "1", 0},
		{"both_empty", "", "", 0},
		{"empty_less_than_real", "", "1.0.0", -1},
		{"over_cap_truncated_deterministically", strings.Repeat("9", filters.MaxInputBytes+1), strings.Repeat("9", filters.MaxInputBytes+1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.CompareSemVer(tc.a, tc.b); got != tc.want {
				t.Errorf("CompareSemVer(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestCompareSemVer_AntiSymmetric proves CompareSemVer(a,b) and
// CompareSemVer(b,a) always disagree in sign (or both are zero), the
// basic invariant any ordering comparator must hold.
func TestCompareSemVer_AntiSymmetric(t *testing.T) {
	pairs := [][2]string{
		{"1.0.0", "2.0.0"}, {"1.2.3", "1.2.3"}, {"3.0.0", "1.0.0"}, {"1.9.0", "1.10.0"},
	}
	for _, p := range pairs {
		fwd := filters.CompareSemVer(p[0], p[1])
		rev := filters.CompareSemVer(p[1], p[0])
		if fwd != -rev {
			t.Errorf("CompareSemVer(%q,%q)=%d and CompareSemVer(%q,%q)=%d are not anti-symmetric", p[0], p[1], fwd, p[1], p[0], rev)
		}
	}
}

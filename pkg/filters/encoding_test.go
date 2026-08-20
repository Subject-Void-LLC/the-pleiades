package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestURLEncode(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello", "hello"},
		{"space_becomes_plus", "hello world", "hello+world"},
		{"reserved_chars", "a/b?c=d&e", "a%2Fb%3Fc%3Dd%26e"},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.URLEncode(tc.in); got != tc.want {
				t.Errorf("URLEncode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestURLDecode(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello", "hello"},
		{"plus_becomes_space", "hello+world", "hello world"},
		{"percent_escapes", "a%2Fb%3Fc%3Dd%26e", "a/b?c=d&e"},
		{"malformed_percent", "%ZZ", ""},
		{"trailing_percent", "100%", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.URLDecode(tc.in); got != tc.want {
				t.Errorf("URLDecode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestURLEncodeDecodeRoundTrip(t *testing.T) {
	inputs := []string{"hello world", "a/b?c=d&e", "unicode: é中", ""}
	for _, in := range inputs {
		enc := filters.URLEncode(in)
		if got := filters.URLDecode(enc); got != in {
			t.Errorf("round trip: URLEncode(%q) = %q, URLDecode(that) = %q, want %q", in, enc, got, in)
		}
	}
}

func TestStringToHex(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hi", "6869"},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.StringToHex(tc.in); got != tc.want {
				t.Errorf("StringToHex(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHexToString(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "6869", "hi"},
		{"empty", "", ""},
		{"odd_length", "abc", ""},
		{"non_hex_char", "zz", ""},
		{"over_cap", strings.Repeat("6", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.HexToString(tc.in); got != tc.want {
				t.Errorf("HexToString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStringHexRoundTrip(t *testing.T) {
	inputs := []string{"hi", "", "binary: \x00\x01\xff", "unicode: é"}
	for _, in := range inputs {
		hexStr := filters.StringToHex(in)
		if got := filters.HexToString(hexStr); got != in {
			t.Errorf("round trip: StringToHex(%q) = %q, HexToString(that) = %q, want %q", in, hexStr, got, in)
		}
	}
}

func TestBytesToHuman(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"zero", 0, "0B"},
		{"under_1024", 512, "512B"},
		{"just_under_1024", 1023, "1023B"},
		{"exactly_1_kib", 1024, "1KiB"},
		{"1.5_kib", 1536, "1.5KiB"},
		{"exactly_1_mib", 1024 * 1024, "1MiB"},
		{"exactly_1_gib", 1024 * 1024 * 1024, "1GiB"},
		{"lossy_rounding", 1500, "1.46KiB"},
		{"negative", -1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.BytesToHuman(tc.in); got != tc.want {
				t.Errorf("BytesToHuman(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHumanToBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"bare_number", "1024", 1024},
		{"kib", "1KiB", 1024},
		{"kib_lowercase", "1kib", 1024},
		{"fractional_kib", "1.5KiB", 1536},
		{"gib", "1GiB", 1024 * 1024 * 1024},
		{"unrecognized_unit", "1XiB", -1},
		{"garbage", "garbage", -1},
		{"empty", "", -1},
		{"negative", "-5", -1},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.HumanToBytes(tc.in); got != tc.want {
				t.Errorf("HumanToBytes(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestBytesHumanRoundTrip is this phase's own Adversarial Pattern
// Justification requirement: BytesToHuman/HumanToBytes round-trip for
// every representative input -- picked, per BytesToHuman's own doc
// comment, so the scaled form has no more than two decimal digits
// (every power of 1024, plus a couple of clean fractions), which is
// where the round trip is exact rather than lossy.
func TestBytesHumanRoundTrip(t *testing.T) {
	inputs := []int{0, 1, 512, 1023, 1024, 1536, 2560, 1024 * 1024, 1024 * 1024 * 1024, 5 * 1024 * 1024 * 1024}
	for _, n := range inputs {
		human := filters.BytesToHuman(n)
		if got := filters.HumanToBytes(human); got != n {
			t.Errorf("round trip: BytesToHuman(%d) = %q, HumanToBytes(that) = %d, want %d", n, human, got, n)
		}
	}
}

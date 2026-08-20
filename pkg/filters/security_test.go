package filters_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestSHA256Hash(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// Well-known SHA-256 test vectors, not derived from the
		// implementation under test.
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.SHA256Hash(tc.in); got != tc.want {
				t.Errorf("SHA256Hash(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	t.Run("over_cap", func(t *testing.T) {
		if got := filters.SHA256Hash(strings.Repeat("a", filters.MaxStructuredInputBytes+1)); got != "" {
			t.Errorf("SHA256Hash(over cap) = %q, want \"\"", got)
		}
	})
}

func TestHMACGenerate(t *testing.T) {
	message := "The quick brown fox jumps over the lazy dog"
	key := "key"
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message))
	want := hex.EncodeToString(mac.Sum(nil))

	if got := filters.HMACGenerate(message, key); got != want {
		t.Errorf("HMACGenerate(%q, %q) = %q, want %q (independently computed)", message, key, got, want)
	}

	t.Run("different_key_different_output", func(t *testing.T) {
		if filters.HMACGenerate(message, "other-key") == want {
			t.Error("HMACGenerate with a different key produced the same output")
		}
	})
	t.Run("over_cap_message", func(t *testing.T) {
		if got := filters.HMACGenerate(strings.Repeat("a", filters.MaxStructuredInputBytes+1), key); got != "" {
			t.Errorf("HMACGenerate(over cap message) = %q, want \"\"", got)
		}
	})
	t.Run("over_cap_key", func(t *testing.T) {
		if got := filters.HMACGenerate(message, strings.Repeat("a", filters.MaxInputBytes+1)); got != "" {
			t.Errorf("HMACGenerate(over cap key) = %q, want \"\"", got)
		}
	})
}

func TestSecureCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", "same-secret", "same-secret", true},
		{"different", "secret-a", "secret-b", false},
		{"different_lengths", "short", "much-longer-value", false},
		{"both_empty", "", "", true},
		{"case_sensitive", "Secret", "secret", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.SecureCompare(tc.a, tc.b); got != tc.want {
				t.Errorf("SecureCompare(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}

	t.Run("over_cap", func(t *testing.T) {
		big := strings.Repeat("a", filters.MaxInputBytes+1)
		if filters.SecureCompare(big, big) {
			t.Error("SecureCompare(over-cap, identical over-cap) = true, want false (refused before comparing)")
		}
	})
}

// TestSecureCompare_ConstantTime is this phase's own Adversarial Pattern
// Justification requirement: prove SecureCompare runs in roughly the
// same time whether its two inputs differ at the very first byte or the
// very last. This is inherently an empirical, statistical proof on
// shared hardware, not a formal one -- crypto/subtle.ConstantTimeCompare
// itself is what actually guarantees constant time; this test's real job
// is to prove SecureCompare's own construction (the length check above
// it, in particular) did not reintroduce a content-dependent shortcut.
// Many samples and a generous tolerance keep this from flaking on a
// loaded CI machine while still catching a gross regression (e.g. a
// stray "if a[0] != b[0] { return false }" fast path, which would show
// up as an order-of-magnitude difference, not a rounding error).
func TestSecureCompare_ConstantTime(t *testing.T) {
	const length = 4096
	const samples = 2000

	base := strings.Repeat("x", length)
	diffAtStart := "y" + base[1:]
	diffAtEnd := base[:length-1] + "y"

	timeFor := func(a, b string) time.Duration {
		start := time.Now()
		for i := 0; i < samples; i++ {
			filters.SecureCompare(a, b)
		}
		return time.Since(start)
	}

	// Warm up (page faults, CPU frequency scaling) before the samples
	// that are actually compared.
	timeFor(base, diffAtStart)
	timeFor(base, diffAtEnd)

	earlyDiff := timeFor(base, diffAtStart)
	lateDiff := timeFor(base, diffAtEnd)

	ratio := float64(lateDiff) / float64(earlyDiff)
	if ratio > 3.0 || ratio < 1.0/3.0 {
		t.Errorf("SecureCompare: early-diff took %v, late-diff took %v (ratio %.2f); want within 3x of each other",
			earlyDiff, lateDiff, ratio)
	}
}

func TestGenerateRandomPassword(t *testing.T) {
	t.Run("zero_length", func(t *testing.T) {
		if got := filters.GenerateRandomPassword(0); got != "" {
			t.Errorf("GenerateRandomPassword(0) = %q, want \"\"", got)
		}
	})
	t.Run("negative_length", func(t *testing.T) {
		if got := filters.GenerateRandomPassword(-1); got != "" {
			t.Errorf("GenerateRandomPassword(-1) = %q, want \"\"", got)
		}
	})
	t.Run("over_max_length", func(t *testing.T) {
		if got := filters.GenerateRandomPassword(100000); got != "" {
			t.Errorf("GenerateRandomPassword(100000) = %q, want \"\"", got)
		}
	})

	for _, length := range []int{1, 8, 32, 128} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			got := filters.GenerateRandomPassword(length)
			if len(got) != length {
				t.Fatalf("GenerateRandomPassword(%d): len = %d, want %d", length, len(got), length)
			}
			for _, r := range got {
				if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%^&*-_=+", r) {
					t.Errorf("GenerateRandomPassword(%d) contains unexpected character %q", length, r)
				}
			}
		})
	}

	t.Run("two_generations_differ", func(t *testing.T) {
		a := filters.GenerateRandomPassword(32)
		b := filters.GenerateRandomPassword(32)
		if a == b {
			t.Error("two independent 32-character passwords were identical; randomness is not engaged")
		}
	})
}

func TestMaskPII(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ssn", "SSN is 123-45-6789 on file", "SSN is [REDACTED-SSN] on file"},
		{"credit_card_16_dashes", "card 4111-1111-1111-1111 charged", "card [REDACTED-CC] charged"},
		{"credit_card_16_spaces", "card 4111 1111 1111 1111 charged", "card [REDACTED-CC] charged"},
		{"credit_card_16_no_sep", "card 4111111111111111 charged", "card [REDACTED-CC] charged"},
		{"amex_15", "amex 3782 822463 10005 used", "amex [REDACTED-CC] used"},
		{"bearer_token", "Authorization: Bearer abc123.def456-ghi", "Authorization: Bearer [REDACTED-TOKEN]"},
		{"bearer_case_insensitive", "auth: bearer XYZ789", "auth: Bearer [REDACTED-TOKEN]"},
		{"no_pii", "just a plain log line with nothing sensitive", "just a plain log line with nothing sensitive"},
		{"multiple_in_one_line", "ssn 123-45-6789 and Bearer abcDEF123",
			"ssn [REDACTED-SSN] and Bearer [REDACTED-TOKEN]"},
		{"nine_bare_digits_not_ssn", "order number 123456789 shipped", "order number 123456789 shipped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.MaskPII(tc.in); got != tc.want {
				t.Errorf("MaskPII(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	t.Run("over_cap_returns_marker_not_original_or_empty", func(t *testing.T) {
		huge := strings.Repeat("a", filters.MaxStructuredInputBytes+1)
		got := filters.MaskPII(huge)
		if got != "[REDACTED-OVERSIZED-INPUT]" {
			t.Errorf("MaskPII(over cap) = %q, want the oversized marker", got)
		}
		if got == huge {
			t.Error("MaskPII(over cap) returned the unredacted original, a real leak risk")
		}
		if got == "" {
			t.Error("MaskPII(over cap) returned \"\", indistinguishable from \"nothing was here\"")
		}
	})
}

func TestWindowsSIDToHex(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// S-1-5-18 is the well-known NT AUTHORITY\SYSTEM SID: revision 1,
		// one sub-authority (18), identifier authority 5. Hand-computed:
		// 01 (revision) 01 (sub-authority count) 000000000005 (6-byte
		// big-endian authority) 12000000 (sub-authority 18, little-endian).
		{"nt_authority_system", "S-1-5-18", "010100000000000512000000"},
		{"lowercase_s_accepted", "s-1-5-18", "010100000000000512000000"},
		{"missing_s_prefix", "1-5-18", ""},
		{"too_few_parts", "S-1", ""},
		{"non_numeric_revision", "S-x-5-18", ""},
		{"non_numeric_authority", "S-1-x-18", ""},
		{"non_numeric_subauthority", "S-1-5-x", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("S-1-5-", 1000), ""},
		{"too_many_subauthorities", "S-1-5" + strings.Repeat("-1", 256), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.WindowsSIDToHex(tc.in); got != tc.want {
				t.Errorf("WindowsSIDToHex(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHexToWindowsSID(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"nt_authority_system", "010100000000000512000000", "S-1-5-18"},
		{"malformed_odd_length_hex", "abc", ""},
		{"too_short", "0101", ""},
		// Declares subauthority count 2 (byte[1] = 0x02) but supplies only
		// one 4-byte subauthority's worth of trailing data (12 bytes total,
		// not the 16 that count would require): len(buf) != 8+4*subCount.
		{"length_mismatch_for_declared_subauthority_count", "010200000000000512000000", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("ab", filters.MaxInputBytes), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.HexToWindowsSID(tc.in); got != tc.want {
				t.Errorf("HexToWindowsSID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWindowsSIDToHex_HexToWindowsSID_RoundTrip(t *testing.T) {
	sids := []string{
		"S-1-5-18",
		"S-1-5-21-3623811015-3361044348-30300820-1013",
		"S-1-1-0",
	}
	for _, sid := range sids {
		hex := filters.WindowsSIDToHex(sid)
		if hex == "" {
			t.Fatalf("WindowsSIDToHex(%q) = \"\", want a real encoding", sid)
		}
		got := filters.HexToWindowsSID(hex)
		if got != sid {
			t.Errorf("round trip: WindowsSIDToHex(%q) = %q, HexToWindowsSID(%q) = %q, want %q", sid, hex, hex, got, sid)
		}
	}
}

func TestSNMPOIDTranslate(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"sys_descr", "1.3.6.1.2.1.1.1.0", "sysDescr.0"},
		{"sys_up_time", "1.3.6.1.2.1.1.3.0", "sysUpTime.0"},
		{"if_descr", "1.3.6.1.2.1.2.2.1.2", "ifDescr"},
		{"whitespace_trimmed", "  1.3.6.1.2.1.1.1.0  ", "sysDescr.0"},
		{"unknown_oid", "1.3.6.1.4.1.9.9.9.9", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("1.", filters.MaxInputBytes), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.SNMPOIDTranslate(tc.in); got != tc.want {
				t.Errorf("SNMPOIDTranslate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

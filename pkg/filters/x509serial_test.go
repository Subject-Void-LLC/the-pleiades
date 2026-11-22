// Tests for SerialHex: the form OpenSSL's "x509 -serial" prints, lowercased
// (checked by hand against OpenSSL 3.0.2: 0 is 00, 128 is 80, 0x0a1b is
// 0A1B), and nothing for a serial that has no such form.
package filters_test

import (
	"math/big"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// TestSerialHex: two lowercase digits a byte, a leading zero digit kept, no
// pad byte before a first digit 8 to f, zero as 00, and "" for a negative or
// missing serial.
func TestSerialHex(t *testing.T) {
	twenty := new(big.Int).SetBytes([]byte(strings.Repeat("\xab", 20)))
	for _, tc := range []struct {
		name   string
		serial *big.Int
		want   string
	}{
		{"a leading zero digit", big.NewInt(0x0A1B), "0a1b"},
		{"the top bit set", big.NewInt(128), "80"},
		{"zero", big.NewInt(0), "00"},
		{"twenty bytes", twenty, strings.Repeat("ab", 20)},
		{"negative", big.NewInt(-5), ""},
		{"none", nil, ""},
	} {
		if got := filters.SerialHex(tc.serial); got != tc.want {
			t.Errorf("%s: SerialHex = %q, want %q", tc.name, got, tc.want)
		}
	}
}

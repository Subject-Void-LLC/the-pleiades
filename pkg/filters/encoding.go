package filters

import (
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
)

// humanUnits are BytesToHuman/HumanToBytes' binary (base-1024) size
// units, in ascending order; index i is 1024^i bytes.
var humanUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// URLEncode percent-encodes s for safe inclusion in a URL query
// component (net/url.QueryEscape: a space becomes "+", not "%20", the
// form-encoding convention rather than net/url.PathEscape's).
func URLEncode(s string) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	return url.QueryEscape(s)
}

// URLDecode reverses URLEncode's percent-encoding. Returns "" if s
// exceeds MaxInputBytes or is not valid query-escaped text (a bare "%"
// not followed by two hex digits, for example).
func URLDecode(s string) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	decoded, err := url.QueryUnescape(s)
	if err != nil {
		return ""
	}
	return decoded
}

// StringToHex encodes s as lowercase hexadecimal, two characters per
// byte.
func StringToHex(s string) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	return hex.EncodeToString([]byte(s))
}

// HexToString decodes a hexadecimal string back to the original bytes,
// the inverse of StringToHex. Returns "" if s exceeds MaxInputBytes, has
// odd length, or contains a non-hex character; the decoded bytes are
// returned as a Go string with no UTF-8 validation, since a Go string is
// just a byte sequence and StringToHex's own input was never required to
// be valid UTF-8 either.
func HexToString(s string) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(decoded)
}

// BytesToHuman formats a non-negative byte count as a human-readable
// binary (base-1024) size, e.g. 1536 -> "1.5KiB", 1073741824 ->
// "1GiB". A value under 1024 is always a bare byte count ("512B"); at
// or above 1024, the value is scaled to the largest unit that keeps it
// at least 1, formatted to at most two decimal places with a trailing
// ".00" or ".50" trimmed to "" or "5" respectively. Returns "" for a
// negative n.
//
// This formatting is lossy by design once a value is scaled (the same
// way `ls -lh`/`du -h` are): BytesToHuman(1500) rounds to "1.46KiB",
// and HumanToBytes("1.46KiB") reconstructs 1495, not 1500. The round
// trip is exact only for a value whose scaled form has no more than two
// decimal digits to begin with (every power of 1024, and a value like
// 1536 = 1.5KiB); this phase's own Adversarial Pattern Justification
// item is proven against representative values of that shape, not
// against every possible int.
func BytesToHuman(n int) string {
	if n < 0 {
		return ""
	}
	if n < 1024 {
		return strconv.Itoa(n) + "B"
	}
	val := float64(n)
	unit := 0
	for val >= 1024 && unit < len(humanUnits)-1 {
		val /= 1024
		unit++
	}
	s := strconv.FormatFloat(val, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s + humanUnits[unit]
}

// HumanToBytes parses a human-readable binary (base-1024) size back to
// a byte count, the inverse of BytesToHuman: the unit suffix must be one
// of BytesToHuman's own ("B", "KiB", "MiB", ..., "EiB"), matched case-
// insensitively, or omitted entirely (a bare number means bytes).
// Returns -1, the same sentinel every int-returning function in this
// package uses for malformed input, if s exceeds MaxInputBytes, has no
// parseable numeric prefix, is negative, or names an unrecognized unit.
func HumanToBytes(s string) int {
	if len(s) > MaxInputBytes {
		return -1
	}
	trimmed := strings.TrimSpace(s)
	i := 0
	for i < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == '-' || trimmed[i] == '+' || (trimmed[i] >= '0' && trimmed[i] <= '9')) {
		i++
	}
	numPart := trimmed[:i]
	unitPart := strings.TrimSpace(trimmed[i:])
	if numPart == "" {
		return -1
	}
	val, err := strconv.ParseFloat(numPart, 64)
	if err != nil || val < 0 {
		return -1
	}
	if unitPart == "" {
		return int(val)
	}
	for idx, u := range humanUnits {
		if !strings.EqualFold(unitPart, u) {
			continue
		}
		mult := 1
		for j := 0; j < idx; j++ {
			mult *= 1024
		}
		return int(val * float64(mult))
	}
	return -1
}

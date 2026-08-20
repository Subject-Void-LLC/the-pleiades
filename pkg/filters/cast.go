package filters

import (
	"math"
	"strconv"
	"strings"
)

// SafeInt parses s as a base-10 integer, returning fallback if s is empty,
// exceeds MaxInputBytes, is not a valid integer, or overflows int. Leading
// and trailing whitespace (including a bare trailing "\r", the shape
// captured CLI output carries constantly) is trimmed before parsing.
//
// This closes the one real "safe cast" gap the expression engine's own
// optional-chaining syntax does not: a value that is present but
// malformed, such as stat.retries holding the string "unknown". A value
// that is missing entirely is a different, already-solved problem, the
// engine's own "?." / ".orValue(default)" syntax; do not reach for SafeInt
// to paper over a field that might not exist at all.
func SafeInt(s string, fallback int) int {
	if len(s) > MaxInputBytes {
		return fallback
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// SafeFloat parses s as a base-10 floating-point number, returning
// fallback if s is empty, exceeds MaxInputBytes, is not a valid number, or
// parses to NaN or an infinity. NaN and Inf are refused deliberately,
// diverging from Go's own strconv.ParseFloat: a NaN silently makes every
// downstream comparison false, which is the opposite of what a "safe"
// filter should produce, so it is treated the same as any other malformed
// input and mapped to fallback rather than passed through.
//
// See SafeInt's doc comment for the distinction between a malformed value
// (this function's job) and a missing one (the engine's own optional
// chaining, not this package).
func SafeFloat(s string, fallback float64) float64 {
	if len(s) > MaxInputBytes {
		return fallback
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fallback
	}
	return f
}

// SafeBool parses s as a boolean, accepting strconv.ParseBool's own
// vocabulary (1/t/T/true/True/TRUE, 0/f/F/false/False/FALSE) case
// insensitively, plus yes/no/on/off, matching the truthy spellings YAML
// 1.1 and Ansible both already trained a migrating runbook author to
// expect and that real device CLI output commonly prints. Any other value,
// including an empty string or one exceeding MaxInputBytes, returns
// fallback. There is no built-in default for an unrecognized spelling: the
// caller's own fallback argument is the only default this function ever
// produces.
//
// See SafeInt's doc comment for the distinction between a malformed value
// (this function's job) and a missing one (the engine's own optional
// chaining, not this package).
func SafeBool(s string, fallback bool) bool {
	if len(s) > MaxInputBytes {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "on":
		return true
	case "0", "f", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

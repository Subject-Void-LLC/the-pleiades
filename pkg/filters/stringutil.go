package filters

import (
	"regexp"
	"strings"
	"unicode"
)

// CamelToSnake converts a camelCase or PascalCase identifier to
// snake_case, treating a run of uppercase runes as one acronym rather
// than one underscore per letter: "classifyIP" becomes "classify_ip",
// not "classify_i_p". This is the same algorithm
// internal/forge/filterscaffold's own camelToSnake uses (proven against
// Phase 51's own real, acronym-heavy filter names), duplicated here
// rather than imported since pkg/ may not import internal/.
func CamelToSnake(s string) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prevUpper := runes[i-1] >= 'A' && runes[i-1] <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if !prevUpper || nextLower {
				b.WriteByte('_')
			}
		}
		if isUpper {
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SnakeToCamel converts a snake_case identifier to camelCase: the first
// segment is lowercased in full, and each later segment has its first
// rune capitalized and the rest lowercased. This is not CamelToSnake's
// exact inverse for an acronym-heavy input (CamelToSnake collapses an
// acronym to lowercase, and SnakeToCamel has no way to know "ip" was
// once "IP" rather than "Ip"); the two are round-trip-exact only for an
// input with no acronym, which is why this phase's own checklist does
// not name this pair in its Adversarial Pattern Justification item the
// way it names OctalToSymbolicPerms/SymbolicToOctalPerms and
// BytesToHuman/HumanToBytes.
func SnakeToCamel(s string) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	var b strings.Builder
	for i, part := range strings.Split(s, "_") {
		if part == "" {
			continue
		}
		lower := []rune(strings.ToLower(part))
		if i > 0 {
			lower[0] = unicode.ToUpper(lower[0])
		}
		b.WriteString(string(lower))
	}
	return b.String()
}

// MaskSecret masks s, keeping only its last keepLast runes visible and
// replacing every rune before that with "*". keepLast is clamped to
// [0, len(s) in runes]: a negative keepLast masks everything, and a
// keepLast at or beyond s's own length leaves s unchanged (there being
// nothing left to mask).
func MaskSecret(s string, keepLast int) string {
	if len(s) > MaxInputBytes {
		return ""
	}
	runes := []rune(s)
	if keepLast < 0 {
		keepLast = 0
	}
	if keepLast > len(runes) {
		keepLast = len(runes)
	}
	maskedCount := len(runes) - keepLast
	return strings.Repeat("*", maskedCount) + string(runes[maskedCount:])
}

// RegexExtract extracts one named capture group's match from s against
// pattern (RE2 syntax, the same engine every regexp.Compile call in this
// codebase uses, which runs in linear time with no catastrophic-
// backtracking failure mode -- unlike a PCRE-style backtracking engine,
// a pathological pattern cannot turn this into a resource-exhaustion
// vector). Returns "" if s or pattern exceeds MaxInputBytes, pattern
// does not compile, pattern has no group named groupName, or pattern
// does not match s.
func RegexExtract(s string, pattern string, groupName string) string {
	if len(s) > MaxInputBytes || len(pattern) > MaxInputBytes {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	match := re.FindStringSubmatch(s)
	if match == nil {
		return ""
	}
	for i, name := range re.SubexpNames() {
		if name == groupName && i < len(match) {
			return match[i]
		}
	}
	return ""
}

// IsEmptyOrWhitespace reports whether s is empty or contains only
// Unicode whitespace.
func IsEmptyOrWhitespace(s string) bool {
	if len(s) > MaxInputBytes {
		return false
	}
	return strings.TrimSpace(s) == ""
}

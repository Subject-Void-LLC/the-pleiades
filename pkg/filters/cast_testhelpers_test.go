package filters_test

import "strings"

// trimSpaceLikeImplementation mirrors the exact trimming SafeInt,
// SafeFloat and SafeBool each apply to their input before parsing
// (strings.TrimSpace), factored out so the fuzz targets re-derive their
// expected outcome the same way the implementation does, rather than
// drifting from it independently.
func trimSpaceLikeImplementation(s string) string {
	return strings.TrimSpace(s)
}

// boolVocabulary mirrors SafeBool's own recognized spelling table. It is
// deliberately a second, independent copy of the switch in cast.go rather
// than a call into unexported implementation detail, since the fuzz test
// exists to catch the two definitions drifting apart.
func boolVocabulary(trimmed string) (value bool, recognized bool) {
	switch strings.ToLower(trimmed) {
	case "1", "t", "true", "yes", "on":
		return true, true
	case "0", "f", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

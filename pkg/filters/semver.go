package filters

import "strconv"

// CompareSemVer compares two semantic-version-shaped strings and reports
// their relative order: -1 if a < b, 0 if equal, 1 if a > b, matching
// strings.Compare's own return convention rather than inventing a new
// one. It compares major, minor and patch numerically, component by
// component: a missing trailing component counts as 0, so "1.2"
// compares equal to "1.2.0". An optional leading "v" is stripped, and
// any pre-release or build suffix after the first character that is not
// a digit or a dot is dropped before comparison, so "1.2.3-rc1" compares
// equal to "1.2.3" rather than being rejected outright. This truncation
// is a prefix cut, not a per-component one: a non-numeric byte appearing
// before the version core ends discards everything from that point on,
// not just the one component containing it, so "1.x.3" behaves as "1"
// (minor and patch both 0), not as "1.0.3" with only its middle
// component zeroed. A caller wanting to reject that kind of malformed
// input outright should validate with a format check first; CompareSemVer
// itself always returns a real answer, leniently.
//
// CompareSemVer has no fallback argument to return on malformed input
// the way this package's cast filters do; it always returns a real
// answer. A caller wanting strict semver validation first is expected to
// pair it with a format check of its own; this function is a comparator,
// not a validator. Input longer than MaxInputBytes is truncated to that
// length before parsing rather than trusted whole, the same "never hand
// an unbounded string to strconv" discipline every other function in
// this package follows.
func CompareSemVer(a, b string) int {
	if len(a) > MaxInputBytes {
		a = a[:MaxInputBytes]
	}
	if len(b) > MaxInputBytes {
		b = b[:MaxInputBytes]
	}
	aMajor, aMinor, aPatch := parseSemVerParts(a)
	bMajor, bMinor, bPatch := parseSemVerParts(b)

	if c := compareInt(aMajor, bMajor); c != 0 {
		return c
	}
	if c := compareInt(aMinor, bMinor); c != 0 {
		return c
	}
	return compareInt(aPatch, bPatch)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// parseSemVerParts extracts up to three dot-separated numeric components
// from s, leniently: a leading "v" is stripped, the scan stops at the
// first byte that is not a digit or a dot (dropping any pre-release or
// build suffix), and a missing or unparseable component is 0.
func parseSemVerParts(s string) (major, minor, patch int) {
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	end := len(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && (c < '0' || c > '9') {
			end = i
			break
		}
	}
	core := s[:end]

	nums := [3]int{}
	start := 0
	part := 0
	for i := 0; i <= len(core) && part < 3; i++ {
		if i < len(core) && core[i] != '.' {
			continue
		}
		if n, err := strconv.Atoi(core[start:i]); err == nil {
			nums[part] = n
		}
		part++
		start = i + 1
	}
	return nums[0], nums[1], nums[2]
}

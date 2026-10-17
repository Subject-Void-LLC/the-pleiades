// Tags ordered as versions, as GNU `sort -V` orders them, for
// container.image.prune's keep_newest.
package image

// compareVersions orders a and b as GNU sort -V does: gnulib's filevercmp
// (a trailing run of ".letters" suffixes compared last), then plain byte
// order to break a tie. It is negative when a sorts first.
func compareVersions(a, b string) int {
	if a == b {
		return 0
	}
	ap, bp := prefixLen(a), prefixLen(b)
	result := verrevcmp(a[:ap], b[:bp])
	if result == 0 && !(ap == len(a) && bp == len(b)) {
		result = verrevcmp(a, b)
	}
	if result != 0 {
		return result
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// isDigit, isAlpha and isAlnum are the C locale's classes, which sort -V
// uses whatever the locale.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }

// prefixLen is the length of s without its suffix: the longest trailing
// run of "." followed by a letter or "~" and then letters, digits or "~"
// (gnulib's file_prefixlen).
func prefixLen(s string) int {
	n := len(s)
	prefix := 0
	for i := 0; ; {
		if i == n {
			return prefix
		}
		i++
		prefix = i
		for i+1 < n && s[i] == '.' && (isAlpha(s[i+1]) || s[i+1] == '~') {
			for i += 2; i < n && (isAlnum(s[i]) || s[i] == '~'); i++ {
			}
		}
	}
}

// order is a character's weight where text is compared with text: the end
// first, then "~" lower still, digits, letters, and every other character
// after the letters (gnulib's order).
func order(s string, pos int) int {
	if pos == len(s) {
		return -1
	}
	c := s[pos]
	switch {
	case isDigit(c):
		return 0
	case isAlpha(c):
		return int(c)
	case c == '~':
		return -2
	}
	return int(c) + 256
}

// verrevcmp compares runs of text by order and runs of digits as numbers
// (gnulib's verrevcmp).
func verrevcmp(s1, s2 string) int {
	p1, p2 := 0, 0
	for p1 < len(s1) || p2 < len(s2) {
		firstDiff := 0
		for (p1 < len(s1) && !isDigit(s1[p1])) || (p2 < len(s2) && !isDigit(s2[p2])) {
			c1, c2 := order(s1, p1), order(s2, p2)
			if c1 != c2 {
				return c1 - c2
			}
			p1++
			p2++
		}
		for p1 < len(s1) && s1[p1] == '0' {
			p1++
		}
		for p2 < len(s2) && s2[p2] == '0' {
			p2++
		}
		for p1 < len(s1) && p2 < len(s2) && isDigit(s1[p1]) && isDigit(s2[p2]) {
			if firstDiff == 0 {
				firstDiff = int(s1[p1]) - int(s2[p2])
			}
			p1++
			p2++
		}
		if p1 < len(s1) && isDigit(s1[p1]) {
			return 1
		}
		if p2 < len(s2) && isDigit(s2[p2]) {
			return -1
		}
		if firstDiff != 0 {
			return firstDiff
		}
	}
	return 0
}

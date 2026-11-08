// Package catalystcenter: the one spelling of a base URL's path, which is
// part of the controller's identity.
package catalystcenter

import (
	"errors"
	"net/url"
	"strings"
)

// basePath returns the decoded and escaped forms of parsed's path in one
// canonical spelling, with any trailing / trimmed, refusing a dot segment.
//
// BaseURL is the synced controller's DeviceID, so two spellings of one path
// must give one string: a%2fb and a%2Fb, or %7E and ~, name the same path
// and must not onboard the controller twice. The spelling is RFC 3986's
// normalization (section 6.2.2): an escaped unreserved character is
// decoded, and every other escape's hex digits are upper case. Nothing else
// changes. Re-escaping each decoded segment with url.PathEscape would also
// turn a literal ; or , into %3B or %2C, which a Java server reads as a
// different path, so the request sent would change.
//
// A dot segment in the path is resolved away by the server, so the
// controller would be reached somewhere other than the URL says.
func basePath(parsed *url.URL) (decoded, escaped string, err error) {
	for _, seg := range strings.Split(parsed.Path, "/") {
		if seg == "." || seg == ".." {
			return "", "", errors.New("catalystcenter: the base URL's path may not contain . or .. segments")
		}
	}
	escaped = strings.TrimRight(normalizeEscapes(parsed.EscapedPath()), "/")
	// The decoded form is read back from the escaped one, so the two agree
	// even when the path ended in an escaped slash, which is kept.
	decoded, err = url.PathUnescape(escaped)
	if err != nil {
		return "", "", errors.New("catalystcenter: the base URL's path is not validly escaped")
	}
	return decoded, escaped, nil
}

// normalizeEscapes rewrites each escape in a validly escaped path to RFC
// 3986's normal form: decoded when it is an unreserved character, else with
// upper-case hex digits.
func normalizeEscapes(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '%' || i+2 >= len(p) {
			b.WriteByte(p[i])
			continue
		}
		hex := strings.ToUpper(p[i+1 : i+3])
		if c, err := url.PathUnescape("%" + hex); err == nil && len(c) == 1 && unreserved(c[0]) {
			b.WriteString(c)
		} else {
			b.WriteString("%" + hex)
		}
		i += 2
	}
	return b.String()
}

// unreserved reports whether c is one of RFC 3986's unreserved characters,
// which mean the same escaped or not.
func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

// Package httpapi: how Join keeps a caller's escaping and which decoded
// segments it refuses.
package httpapi

import (
	"fmt"
	"net/url"
	"strings"
)

// escapedPathOf is rel's path as its caller escaped it, with each escape
// kept exactly as written and each byte a path may not hold unescaped (a raw
// space, a quote, a non-ASCII byte) escaped.
//
// url.URL.EscapedPath keeps the caller's form only when the whole path is
// validly escaped; otherwise it escapes the decoded path afresh, which turns
// a %2F the caller wrote into a separator. So "/items/a%2Fb c" would be sent
// as /items/a/b%20c. Escaping only the raw bytes keeps the %2F and still
// sends the space as %20, which is how a path with a raw space was sent
// before. url.Parse has already refused a % that does not begin an escape.
func escapedPathOf(rel *url.URL) string {
	raw := rel.RawPath
	if raw == "" {
		// url.Parse leaves RawPath empty only when Path's default encoding
		// is the path as written.
		return rel.EscapedPath()
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '%' && i+2 < len(raw):
			// An escape, kept as written.
			b.WriteString(raw[i : i+3])
			i += 2
		case pathByte(c):
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// pathByte reports whether c may stand unescaped in an escaped path: the
// characters net/url itself leaves there.
func pathByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("-._~!$&'()*+,;=:@[]/", c) >= 0
}

// dotSegment reports whether one decoded path segment is a dot segment on
// some server: "." and "..", and either followed by a ";" path parameter,
// since a Java server (Batfish is one) strips the parameter before it
// resolves the segment, so "..;x" climbs like "..".
func dotSegment(seg string) bool {
	if i := strings.IndexByte(seg, ';'); i >= 0 {
		seg = seg[:i]
	}
	return seg == "." || seg == ".."
}

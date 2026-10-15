// A remote command's text, made safe to quote in an error, a log or a
// terminal.
package remoteexec

import (
	"fmt"
	"strings"
	"unicode"
)

// Excerpt returns text cut to max bytes, with every rune that is not
// printable escaped as \uXXXX, so a device cannot write terminal control
// sequences into an error, a log or a terminal through a command's output,
// nor make an error of any size (LESSONS_LEARNED 266). What was cut is
// counted rather than dropped silently. It is the same rendering pkg/kubeapi
// gives an API server's error text.
func Excerpt(text string, max int) string {
	var b strings.Builder
	for i, r := range text {
		if b.Len() >= max {
			fmt.Fprintf(&b, "... (%d more bytes)", len(text)-i)
			break
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}

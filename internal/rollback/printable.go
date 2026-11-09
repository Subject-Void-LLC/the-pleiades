// Package rollback: printing a method name or a node id a journal or a
// dispatch carried.
package rollback

import "strconv"

// Printable is s as a refusal, a warning, a job event or an API body prints
// a method name (an FQCN, an undo's FQCN) or a node id that came from a
// journal entry or a dispatch: as it is when every byte of it is visible
// ASCII other than a quote or a backslash, which every registered method
// name and every graph node id is, and quoted (strconv.Quote, %q's form)
// otherwise. A Walk row is a Runner's to write and holds both as sent, so a
// direction override, a zero-width character or a line break in one would
// otherwise reach the reader raw and make the line read as something else.
// termsafe escapes what a terminal acts on, and a JSON body escapes control
// characters, but neither escapes every format character; strconv.Quote
// escapes each one that is not printable.
func Printable(s string) string {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b < 0x21 || b > 0x7e || b == '"' || b == '\\' {
			return strconv.Quote(s)
		}
	}
	if s == "" {
		return `""`
	}
	return s
}

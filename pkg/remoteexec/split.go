package remoteexec

import (
	"fmt"
	"strings"
)

// SplitWords splits a command line into an argument vector the way a
// POSIX shell would, honoring single quotes, double quotes and
// backslash escapes, and honoring nothing else.
//
// It is the counterpart of QuoteCommand and exists for one specific job:
// letting a module accept the free-form command string people actually
// write ("systemctl restart nginx") while still running it with no shell
// involved. The module splits here, then quotes every resulting element
// with QuoteCommand, so the remote shell receives a command line it
// cannot find anything to interpret in. "echo hi; rm -rf /" becomes the
// five literal arguments echo, hi;, rm, -rf, / and the semicolon is just
// a character in the second one, which is exactly what the same string
// does through Ansible's command module.
//
// What it deliberately does NOT do is the rest of a shell: no variable
// expansion, no command substitution, no globbing, no operators, no
// comments. Those are not missing features. A module whose whole promise
// is "no shell is involved" cannot implement the parts of a shell that
// run other programs, and a caller that wants them wants exec.shell.
//
// An unterminated quote is an error rather than a silent guess. The two
// plausible guesses (treat the rest of the line as quoted, or drop the
// quote) produce different argument vectors, and neither is what the
// author meant.
func SplitWords(s string) ([]string, error) {
	var (
		words   []string
		current strings.Builder
		started bool // whether current holds a word, including a deliberately empty one
	)

	// flush ends the word being built. started, rather than
	// current.Len(), is what distinguishes an empty quoted argument ('')
	// from the gap between two spaces.
	flush := func() {
		if started {
			words = append(words, current.String())
			current.Reset()
			started = false
		}
	}

	const (
		outside = iota
		inSingle
		inDouble
	)
	state := outside

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch state {
		case inSingle:
			// Inside single quotes every byte is literal, with no escapes
			// of any kind, until the closing quote. That is the whole rule.
			if c == '\'' {
				state = outside
				continue
			}
			current.WriteByte(c)

		case inDouble:
			switch {
			case c == '"':
				state = outside
			case c == '\\' && i+1 < len(s) && isDoubleQuoteEscapable(s[i+1]):
				// Inside double quotes a backslash escapes only this small
				// set. Before any other byte it is a literal backslash,
				// which is why a Windows-style path survives intact.
				i++
				current.WriteByte(s[i])
			default:
				current.WriteByte(c)
			}

		default: // outside
			switch {
			case c == ' ' || c == '\t' || c == '\n' || c == '\r':
				flush()
			case c == '\'':
				state = inSingle
				started = true
			case c == '"':
				state = inDouble
				started = true
			case c == '\\':
				if i+1 >= len(s) {
					return nil, fmt.Errorf("command line ends with a dangling backslash: %q", s)
				}
				i++
				current.WriteByte(s[i])
				started = true
			default:
				current.WriteByte(c)
				started = true
			}
		}

		// Any byte written while outside whitespace starts or continues a
		// word. Handled inline above for the outside case; the quoted
		// cases set started when their opening quote was seen.
		if state != outside {
			started = true
		}
	}

	switch state {
	case inSingle:
		return nil, fmt.Errorf("command line has an unterminated single quote: %q", s)
	case inDouble:
		return nil, fmt.Errorf("command line has an unterminated double quote: %q", s)
	}

	flush()
	return words, nil
}

// isDoubleQuoteEscapable reports whether c is one of the bytes a
// backslash escapes inside double quotes. Everything else keeps its
// backslash, which is what a POSIX shell does and what Python's shlex
// does in posix mode, the two references a module author is most likely
// to be comparing against.
func isDoubleQuoteEscapable(c byte) bool {
	switch c {
	case '$', '`', '"', '\\', '\n':
		return true
	default:
		return false
	}
}

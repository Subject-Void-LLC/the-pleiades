// Package playbook: splitting a when: condition into tokens.
package playbook

import (
	"fmt"
	"strings"
	"unicode"
)

// whenPunct are the operators and punctuation the condition grammar
// uses, longest first so "<=" is never read as "<" then "=".
var whenPunct = []string{"==", "!=", "<=", ">=", "<", ">", "|", "(", ")", "[", "]", ".", ","}

// tokenizeWhen splits src into tokens. A string literal becomes one token
// holding its quote, its decoded text and its quote again, so the parser
// never sees an escape sequence. Anything outside the grammar (arithmetic,
// ~, a {{ }} wrapper, an unknown escape) is refused here.
func tokenizeWhen(src string) ([]string, error) {
	var toks []string
	r := []rune(src)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '"' || c == '\'':
			text, next, err := readWhenString(r, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, string(c)+text+string(c))
			i = next
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(r) && unicode.IsDigit(r[i+1]) && startsOperand(toks)):
			j := i + 1
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.' || r[j] == '_' || r[j] == 'e' || r[j] == 'E') {
				j++
			}
			toks = append(toks, strings.ReplaceAll(string(r[i:j]), "_", ""))
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i + 1
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_') {
				j++
			}
			toks = append(toks, string(r[i:j]))
			i = j
		default:
			matched := false
			for _, p := range whenPunct {
				if strings.HasPrefix(string(r[i:min(i+2, len(r))]), p) {
					toks = append(toks, p)
					i += len([]rune(p))
					matched = true
					break
				}
			}
			switch {
			case matched:
			case c == '{':
				return nil, fmt.Errorf("a condition is already Jinja, so {{ }} or {%% %%} inside it is not translated")
			default:
				return nil, fmt.Errorf("%q is not something this converter translates", string(c))
			}
		}
	}
	return toks, nil
}

// startsOperand reports whether the next token begins an operand, so a
// "-" there is a sign rather than a subtraction, which is refused.
func startsOperand(toks []string) bool {
	if len(toks) == 0 {
		return true
	}
	switch toks[len(toks)-1] {
	case "==", "!=", "<=", ">=", "<", ">", "(", "[", ",", "and", "or", "not", "in":
		return true
	}
	return false
}

// readWhenString reads the string literal starting at r[i], returning its
// decoded text and the index after its closing quote.
func readWhenString(r []rune, i int) (string, int, error) {
	quote := r[i]
	var b strings.Builder
	for j := i + 1; j < len(r); j++ {
		switch c := r[j]; {
		case c == quote:
			return b.String(), j + 1, nil
		case c == '\\':
			if j+1 >= len(r) {
				return "", 0, fmt.Errorf("unfinished escape in a string")
			}
			j++
			switch r[j] {
			case '\\', '\'', '"':
				b.WriteRune(r[j])
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			default:
				return "", 0, fmt.Errorf("escape \\%c is not one this converter translates", r[j])
			}
		default:
			b.WriteRune(c)
		}
	}
	return "", 0, fmt.Errorf("unterminated string")
}

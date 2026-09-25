// Package playbook: Ansible's free-form "key=value" arguments.
//
// A task may write its arguments as one string: `apt: name=curl
// state=present`, or `command: /usr/bin/make install chdir=/src`. Ansible
// splits such a string on spaces outside quotes and outside {{ }}, reads
// each key=value token as an argument, and, for a module with a free-form
// command (command, shell), keeps every other token as the command text.
// This file does the same, and refuses a string it cannot split the way
// Ansible would.
package playbook

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// kvKey matches the key half of a key=value token.
var kvKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// errUnbalanced marks a free-form string whose quotes or braces never
// close.
var errUnbalanced = errors.New("unbalanced quotes or {{ }} in free-form arguments")

// splitArgs splits s on whitespace outside quotes and outside {{ }},
// keeping each token as written, quotes included.
func splitArgs(s string) ([]string, error) {
	// Splitting works on runes, which would silently turn an invalid byte
	// into U+FFFD and change the command; refuse it instead.
	if !utf8.ValidString(s) {
		return nil, errors.New("free-form arguments are not valid UTF-8")
	}
	var tokens []string
	var cur strings.Builder
	var quote rune
	depth := 0
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			cur.WriteRune(r)
			if r == '\\' && i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
			} else if r == quote {
				quote = 0
			}
		case r == '{' && i+1 < len(runes) && runes[i+1] == '{':
			depth++
			cur.WriteString("{{")
			i++
		case r == '}' && i+1 < len(runes) && runes[i+1] == '}' && depth > 0:
			depth--
			cur.WriteString("}}")
			i++
		case depth == 0 && (r == '"' || r == '\''):
			quote = r
			cur.WriteRune(r)
		case depth == 0 && (r == ' ' || r == '\t' || r == '\n'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 || depth != 0 {
		return nil, errUnbalanced
	}
	flush()
	return tokens, nil
}

// unquote removes one pair of matching outer quotes, as Ansible does for
// a key=value token's value.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		inner := s[1 : len(s)-1]
		if s[0] == '"' {
			inner = strings.ReplaceAll(inner, `\"`, `"`)
		} else {
			inner = strings.ReplaceAll(inner, `\'`, `'`)
		}
		return inner
	}
	return s
}

// parseKV reads free-form arguments. When rawKeys is nil every token must
// be key=value; otherwise only a token whose key is in rawKeys is an
// argument, and every other token is kept, in order and as written, as
// the free-form text Ansible calls _raw_params.
func parseKV(s string, rawKeys map[string]bool) (args map[string]string, raw string, err error) {
	tokens, err := splitArgs(s)
	if err != nil {
		return nil, "", err
	}
	args = map[string]string{}
	var rest []string
	for _, tok := range tokens {
		m := kvKey.FindStringSubmatch(tok)
		if m != nil && (rawKeys == nil || rawKeys[m[1]]) {
			args[m[1]] = unquote(m[2])
			continue
		}
		if rawKeys == nil {
			return nil, "", errors.New("free-form arguments hold a token that is not key=value")
		}
		rest = append(rest, tok)
	}
	return args, strings.Join(rest, " "), nil
}

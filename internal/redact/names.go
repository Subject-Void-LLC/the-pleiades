// Package redact: deciding whether a name or a value is secret before it
// is written anywhere, for code that holds data to copy rather than a log
// line to mask.
package redact

import "strings"

// SecretName reports whether name, a variable or parameter name, names a
// secret under m's key rules. The whole name matches, and so does any run
// of its words, split on "_", "-" and ".": db_password matches through
// password, and vault_api_token through api_token, while tokenizer does
// not match token, since a rule matches whole words only. Case is ignored.
//
// It is deliberately generous. It exists for a caller about to copy a
// value somewhere lasting, a converted runbook say, and for that caller a
// false positive costs a sentence asking a person to supply the value,
// while a false negative writes the secret into a file.
func (m *Masker) SecretName(name string) bool {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for start := range words {
		for end := start + 1; end <= len(words); end++ {
			if _, secret := m.keys[strings.Join(words[start:end], "_")]; secret {
				return true
			}
		}
	}
	return false
}

// SecretShaped reports whether value holds text one of m's pattern rules
// masks: a PEM private key, a bearer token, a JSON web token, an access
// key id, a URL carrying a password. It is Text's pattern channel asked as
// a question.
func (m *Masker) SecretShaped(value string) bool {
	return m.Text(nil, value) != value
}

// SecretName is Shared().SecretName.
func SecretName(name string) bool { return Shared().SecretName(name) }

// SecretShaped is Shared().SecretShaped.
func SecretShaped(value string) bool { return Shared().SecretShaped(value) }

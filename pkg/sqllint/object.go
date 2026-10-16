// Package sqllint: naming what a DROP drops, for the additive-only
// rule's message.
package sqllint

import (
	"slices"
	"strings"
)

// multiWordObjects are the kinds of thing a DROP names in more than one
// word, each longer one before any it starts with.
var multiWordObjects = [][]string{
	{"FOREIGN", "DATA", "WRAPPER"},
	{"TEXT", "SEARCH", "CONFIGURATION"}, {"TEXT", "SEARCH", "DICTIONARY"},
	{"TEXT", "SEARCH", "PARSER"}, {"TEXT", "SEARCH", "TEMPLATE"},
	{"MATERIALIZED", "VIEW"}, {"FOREIGN", "TABLE"}, {"EVENT", "TRIGGER"},
	{"ACCESS", "METHOD"}, {"USER", "MAPPING"},
	{"OPERATOR", "CLASS"}, {"OPERATOR", "FAMILY"},
}

// object names what a DROP drops, from the words after it: the kind of
// thing, in as many words as it takes, with its article.
func object(w []string) string {
	for len(w) > 0 && (w[0] == "IF" || w[0] == "EXISTS" || w[0] == "CONCURRENTLY") {
		w = w[1:]
	}
	switch {
	case len(w) == 0:
		return "something"
	case len(w) > 1 && w[0] == "NOT" && w[1] == "NULL":
		// ALTER ... ALTER COLUMN c DROP NOT NULL.
		return "a NOT NULL constraint"
	case w[0] == "OWNED":
		// DROP OWNED BY role.
		return "everything a role owns"
	}
	kind := w[0]
	for _, multi := range multiWordObjects {
		if len(w) >= len(multi) && slices.Equal(w[:len(multi)], multi) {
			kind = strings.Join(multi, " ")
			break
		}
	}
	kind = strings.ToLower(kind)
	// "an" before a vowel sound; every kind starting with u says "you".
	if strings.ContainsRune("aeio", rune(kind[0])) {
		return "an " + kind
	}
	return "a " + kind
}

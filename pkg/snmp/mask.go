// Package snmp: masking the client's credential out of its errors, and an
// agent's secrets out of what it answers, before either reaches a stat, a
// fact, a warning or a log.
//
// The engine masks every output by the run's secrets too; this is the first
// pass, for what the engine cannot see: a community shorter than
// wire.MinSNMPCommunityLength, which is masked by value nowhere else, and the
// agent's own community table, which holds communities the run was never
// given.
package snmp

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
)

// secretTables are the subtrees whose octet strings are secrets whatever
// their value: SNMP-COMMUNITY-MIB's community table (RFC 3584), whose
// snmpCommunityName column is a community, and USM's user table (RFC 3414),
// whose key-change columns an agent should answer empty and need not.
var secretTables = []string{"1.3.6.1.6.3.18.1.1", "1.3.6.1.6.3.15.1.2.2"}

// openModules are the parts of snmpModules (OIDSNMPModules) that hold no
// name of the agent's security: SNMPv2-MIB's own objects, the engine group,
// and the message and USM counters.
var openModules = []string{"1.3.6.1.6.3.1", "1.3.6.1.6.3.10.2.1", "1.3.6.1.6.3.11.2", "1.3.6.1.6.3.15.1.1"}

// SecurityOID reports whether oid lies in the agent's own security
// configuration: snmpModules but the parts openModules names. There the
// table indexes spell what the agent is configured with (measured on net-snmp
// 5.9.4: vacmSecurityToGroupTable is indexed by the security name each
// community maps to, and usmUserTable by each user name; an agent may name a
// community there), so a binding's OID can carry a secret, not only its
// value, and masking the value is not enough. net.snmp.get reads none of it.
func SecurityOID(oid string) bool {
	if !Under(oid, OIDSNMPModules) {
		return false
	}
	for _, open := range openModules {
		if Under(oid, open) {
			return false
		}
	}
	return true
}

// SecuritySubtree reports whether a walk rooted at root would read nothing
// but the agent's security configuration: root is in it (SecurityOID) and
// holds none of openModules, as snmpModules itself does. A walk of such a
// root is refused; a walk of any other root leaves the security bindings out.
func SecuritySubtree(root string) bool {
	if !SecurityOID(root) {
		return false
	}
	for _, open := range openModules {
		if Under(open, root) {
			return false
		}
	}
	return true
}

// SecretOID reports whether oid's value is masked by its place alone
// (secretTables).
func SecretOID(oid string) bool {
	for _, root := range secretTables {
		if Under(oid, root) {
			return true
		}
	}
	return false
}

// MaskValues is text with each of secrets replaced by its placeholder: one
// of at least netcfg.MinLearnedLength bytes wherever it stands, a shorter one
// only where it stands as its own word, since a short community replaced
// everywhere ("public" inside "republic") would cut ordinary words apart.
// The longest is replaced first, so a longer secret holding a shorter one is
// masked whole.
func MaskValues(text string, secrets []string) string {
	ordered := append([]string(nil), secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, s := range ordered {
		if s == "" {
			continue
		}
		if len(s) >= netcfg.MinLearnedLength {
			text = strings.ReplaceAll(text, s, netcfg.Placeholder(s))
			continue
		}
		text = replaceWord(text, s, netcfg.Placeholder(s))
	}
	return text
}

// Mask is v with its value masked: replaced whole when its OID is in a
// secret table, and of each of secrets otherwise. A masked octet string
// keeps no raw bytes.
func Mask(v Varbind, secrets []string) Varbind {
	if v.Type != TypeOctetString && v.Type != TypeOpaque {
		return v
	}
	if SecretOID(v.OID) {
		if v.Text != "" {
			v.Text, v.Hex, v.Bytes = netcfg.Placeholder(v.Text), false, nil
		}
		return v
	}
	if masked := MaskValues(v.Text, secrets); masked != v.Text {
		v.Text, v.Hex, v.Bytes = masked, false, nil
	}
	return v
}

// replaceWord is text with each occurrence of word that has no letter or
// digit directly before or after it replaced by with.
func replaceWord(text, word, with string) string {
	var b strings.Builder
	for {
		i := strings.Index(text, word)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		end := i + len(word)
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[end:])
		b.WriteString(text[:i])
		if (i == 0 || !wordRune(before)) && (end == len(text) || !wordRune(after)) {
			b.WriteString(with)
		} else {
			b.WriteString(word)
		}
		text = text[end:]
	}
}

// wordRune reports whether r is a letter or a digit.
func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

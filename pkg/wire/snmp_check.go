// The one check an SNMP credential is held to on every write path.
package wire

import (
	"fmt"
	"strings"
	"unicode"
)

// An SNMPProblem is why an SNMP credential cannot be stored: the key it is
// about (a Secret* constant above) and what is wrong, as a clause following
// the key's name. It never repeats a value, since every caller's message
// reaches a terminal, an API response or a job's failure reason, where
// nothing masks it.
type SNMPProblem struct {
	// Key is the flattened key the problem is about.
	Key string
	// Problem says what is wrong with it.
	Problem string
}

// Error reads as the key followed by its problem.
func (p *SNMPProblem) Error() string { return p.Key + " " + p.Problem }

// snmpProblem builds an SNMPProblem.
func snmpProblem(key, text string) *SNMPProblem { return &SNMPProblem{Key: key, Problem: text} }

// Check refuses an SNMP credential no agent could be configured to accept,
// or one this platform could not mask, with an *SNMPProblem. It is the one
// check every write path runs: pleiades add-credential, the credential file
// on save and again on read, and the pleiades_snmp credential type on save
// and again at injection. allowShortCommunity is the operator's explicit
// acceptance of a community under MinSNMPCommunityLength, which is then
// masked by shape only.
//
// The rules, each refusing rather than guessing:
//
//   - A community with a USM user, protocol or passphrase. A community is
//     v1's and v2c's and a user is v3's; with both, a client would have to
//     guess which version the device answers, as a certificate and a bundle
//     together would leave it guessing which identity to present.
//   - A community under MinSNMPCommunityLength without allowShortCommunity,
//     and allowShortCommunity with no short community to allow.
//   - A protocol or passphrase with no USM user, since USM names the user in
//     every message.
//   - A user name over 32 characters or holding a control character (RFC
//     3414's SnmpAdminString (SIZE(1..32)); the name is shown in output).
//   - A user or protocol name beginning or ending with a space, refused
//     rather than trimmed (spaced says why), so every path reads one
//     alike; a community or passphrase is taken as written.
//   - A protocol outside the vocabulary, a protocol without its passphrase,
//     and a passphrase without its protocol.
//   - Privacy without authentication, which USM does not have.
//   - A passphrase under MinSNMPPassphraseLength.
func (c SNMPCredential) Check(allowShortCommunity bool) error {
	if p := c.problem(allowShortCommunity); p != nil {
		return p
	}
	return nil
}

// problem is Check's answer as a typed nil-able value, so Check returns a nil
// error rather than a nil *SNMPProblem inside a non-nil interface.
func (c SNMPCredential) problem(allowShortCommunity bool) *SNMPProblem {
	usm := c.User != "" || c.AuthProtocol != "" || c.AuthPassphrase != "" || c.PrivProtocol != "" || c.PrivPassphrase != ""
	if c.Community != "" && usm {
		return snmpProblem(SecretSNMPCommunity, "is SNMPv1 and v2c's, and this credential also holds an SNMPv3 user or protocol; "+
			"a credential holds one or the other, since a client could not tell which version the device answers")
	}
	switch {
	case c.ShortCommunity() && !allowShortCommunity:
		return snmpProblem(SecretSNMPCommunity, fmt.Sprintf("is shorter than %d characters, so it could only be masked by its shape (the "+
			"configuration lines that hold it), never by value; allow a short community explicitly to store it anyway", MinSNMPCommunityLength))
	case allowShortCommunity && !c.ShortCommunity():
		return snmpProblem(SecretSNMPCommunity, fmt.Sprintf("is not a community shorter than %d characters, so allowing a short one allows nothing", MinSNMPCommunityLength))
	}
	if !usm {
		return nil
	}
	if c.User == "" {
		return snmpProblem(SecretSNMPUser, "is empty, and an SNMPv3 protocol or passphrase needs the USM user it belongs to")
	}
	if spaced(c.User) {
		return snmpProblem(SecretSNMPUser, "begins or ends with a space, which an agent would compare byte for byte; remove it, or it names another user")
	}
	if len(c.User) > maxSNMPUserLength || strings.IndexFunc(c.User, unicode.IsControl) >= 0 {
		return snmpProblem(SecretSNMPUser, fmt.Sprintf("is over %d characters or holds a control character, which a USM user name may not", maxSNMPUserLength))
	}
	if p := pairProblem(SecretSNMPAuthProtocol, SecretSNMPAuthPassphrase, string(c.AuthProtocol), c.AuthPassphrase, c.AuthProtocol.Valid(), authNames()); p != nil {
		return p
	}
	if p := pairProblem(SecretSNMPPrivProtocol, SecretSNMPPrivPassphrase, string(c.PrivProtocol), c.PrivPassphrase, c.PrivProtocol.Valid(), privNames()); p != nil {
		return p
	}
	if c.PrivProtocol != "" && c.AuthProtocol == "" {
		return snmpProblem(SecretSNMPPrivProtocol, "is set without an authentication protocol, and SNMPv3 has no privacy without authentication (RFC 3414)")
	}
	return nil
}

// pairProblem holds one protocol and its passphrase to each other: both or
// neither, the protocol in vocabulary (named in names), the passphrase long
// enough.
func pairProblem(protocolKey, passphraseKey, protocol, passphrase string, valid bool, names string) *SNMPProblem {
	switch {
	case protocol == "" && passphrase == "":
		return nil
	case protocol == "":
		return snmpProblem(protocolKey, "is empty, and its passphrase is set; name the protocol the passphrase is for")
	case spaced(protocol):
		return snmpProblem(protocolKey, "begins or ends with a space; remove it, and name one of "+names)
	case !valid:
		return snmpProblem(protocolKey, "is not one of "+names)
	case passphrase == "":
		return snmpProblem(passphraseKey, "is empty, and its protocol is set; a protocol needs its passphrase")
	case len(passphrase) < MinSNMPPassphraseLength:
		return snmpProblem(passphraseKey, fmt.Sprintf("is shorter than %d characters, which RFC 3414 asks of every USM passphrase and net-snmp refuses", MinSNMPPassphraseLength))
	}
	return nil
}

// spaced reports whether s begins or ends with white space. A name is
// refused for it rather than trimmed: every write path runs Check, so
// refusing here holds them all to one reading with nothing to forget, and a
// trailing space is legal in a USM user name (SnmpAdminString), so a trim
// could store a name other than the one an agent was given.
func spaced(s string) bool { return strings.TrimSpace(s) != s }

// authNames is the authentication vocabulary as a comma list.
func authNames() string {
	names := make([]string, 0, len(SNMPAuthProtocols()))
	for _, p := range SNMPAuthProtocols() {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}

// privNames is the privacy vocabulary as a comma list.
func privNames() string {
	names := make([]string, 0, len(SNMPPrivProtocols()))
	for _, p := range SNMPPrivProtocols() {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}

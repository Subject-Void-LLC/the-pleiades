// The SNMP half of a credential on this platform's wire: the keys it
// flattens into, the closed vocabulary of SNMPv3 (USM) protocols, and the one
// check every write path holds it to.
//
// No SNMP client exists yet. Phase 97 builds it (pkg/snmp, on gosnmp), and
// proves each protocol named here against a real agent before any of them is
// called supported; until then these values are stored, carried and masked,
// and nothing sends them anywhere.
package wire

import (
	"encoding/json"
	"fmt"
)

// The SNMP keys of a flattened credential, beside SecretUsername and the rest
// (dispatch.go). They are their own family: a stored credential replaces all
// six together when SNMP is saved, and keeps them when a login is saved.
const (
	// SecretSNMPCommunity is an SNMPv1 or SNMPv2c community string: the
	// password of the community-based security model, sent in the clear in
	// every request.
	SecretSNMPCommunity = "snmp_community"

	// SecretSNMPUser is an SNMPv3 USM user name (RFC 3414's securityName).
	// It is an identifier, not a secret, like SecretUsername, and kept apart
	// from it: a device's SSH login and its SNMPv3 user are separate accounts
	// on every agent this platform targets (an IOS "snmp-server user", a
	// net-snmp "createUser"), and a template binding a machine credential and
	// an SNMP credential carries both in one map.
	SecretSNMPUser = "snmp_user"

	// SecretSNMPAuthProtocol names the USM authentication protocol (an
	// SNMPAuthProtocol). Not a secret: it is in the device's configuration
	// and it decides nothing an attacker lacks.
	SecretSNMPAuthProtocol = "snmp_auth_protocol"

	// SecretSNMPAuthPassphrase is the USM authentication passphrase, from
	// which the agent and the client each derive the same localized key
	// (RFC 3414's password-to-key).
	SecretSNMPAuthPassphrase = "snmp_auth_passphrase"

	// SecretSNMPPrivProtocol names the USM privacy protocol (an
	// SNMPPrivProtocol). Not a secret, for SecretSNMPAuthProtocol's reason.
	SecretSNMPPrivProtocol = "snmp_priv_protocol"

	// SecretSNMPPrivPassphrase is the USM privacy passphrase.
	SecretSNMPPrivPassphrase = "snmp_priv_passphrase"
)

// SNMPKeys returns every SNMP key of a flattened credential, the family a
// save replaces together.
func SNMPKeys() []string {
	return []string{
		SecretSNMPCommunity, SecretSNMPUser,
		SecretSNMPAuthProtocol, SecretSNMPAuthPassphrase,
		SecretSNMPPrivProtocol, SecretSNMPPrivPassphrase,
	}
}

// SNMPAuthProtocol is a USM authentication protocol, by the name this
// platform stores it under. The vocabulary is closed: a name outside it is
// refused by SNMPCredential.Check, never passed on for a client to guess at.
type SNMPAuthProtocol string

// The USM authentication protocols. Every one is accepted, the old ones
// included, because the devices that offer nothing newer still exist: MD5
// and SHA-1 are RFC 3414's, the SHA-2 family RFC 7860's.
const (
	SNMPAuthMD5    SNMPAuthProtocol = "md5"
	SNMPAuthSHA    SNMPAuthProtocol = "sha"
	SNMPAuthSHA224 SNMPAuthProtocol = "sha224"
	SNMPAuthSHA256 SNMPAuthProtocol = "sha256"
	SNMPAuthSHA384 SNMPAuthProtocol = "sha384"
	SNMPAuthSHA512 SNMPAuthProtocol = "sha512"
)

// SNMPPrivProtocol is a USM privacy protocol, by the name this platform
// stores it under, in a closed vocabulary like SNMPAuthProtocol's.
type SNMPPrivProtocol string

// The USM privacy protocols. DES is RFC 3414's, 3DES the Reeder draft's
// (draft-reeder-snmpv3-usm-3desede), AES-128 RFC 3826's.
//
// AES-192 and AES-256 have no RFC, and two incompatible drafts extend a
// passphrase's key to their length differently, so each is named twice and
// never confused with the other: aes192 and aes256 are the Blumenthal draft
// (draft-blumenthal-aes-usm-04), as net-snmp implements them; aes192-reeder
// and aes256-reeder are the Reeder draft (draft-reeder-snmpv3-usm-aes),
// often called the Cisco variant (gosnmp's AES192C and AES256C). A device
// configured for one answers a client using the other with a decryption
// error, so the name has to say which. Phase 97 proves each against a real
// agent; until then no client exists to use any of them.
const (
	SNMPPrivDES          SNMPPrivProtocol = "des"
	SNMPPriv3DES         SNMPPrivProtocol = "3des"
	SNMPPrivAES128       SNMPPrivProtocol = "aes128"
	SNMPPrivAES192       SNMPPrivProtocol = "aes192"
	SNMPPrivAES256       SNMPPrivProtocol = "aes256"
	SNMPPrivAES192Reeder SNMPPrivProtocol = "aes192-reeder"
	SNMPPrivAES256Reeder SNMPPrivProtocol = "aes256-reeder"
)

// SNMPAuthProtocols returns the authentication vocabulary, in the order a
// help text lists it.
func SNMPAuthProtocols() []SNMPAuthProtocol {
	return []SNMPAuthProtocol{SNMPAuthMD5, SNMPAuthSHA, SNMPAuthSHA224, SNMPAuthSHA256, SNMPAuthSHA384, SNMPAuthSHA512}
}

// SNMPPrivProtocols returns the privacy vocabulary, in the order a help text
// lists it.
func SNMPPrivProtocols() []SNMPPrivProtocol {
	return []SNMPPrivProtocol{SNMPPrivDES, SNMPPriv3DES, SNMPPrivAES128, SNMPPrivAES192, SNMPPrivAES256, SNMPPrivAES192Reeder, SNMPPrivAES256Reeder}
}

// Valid reports whether p is in the authentication vocabulary.
func (p SNMPAuthProtocol) Valid() bool {
	for _, known := range SNMPAuthProtocols() {
		if p == known {
			return true
		}
	}
	return false
}

// Valid reports whether p is in the privacy vocabulary.
func (p SNMPPrivProtocol) Valid() bool {
	for _, known := range SNMPPrivProtocols() {
		if p == known {
			return true
		}
	}
	return false
}

// SNMPSecurityLevel is an SNMPv3 security level, in RFC 3411's own words.
type SNMPSecurityLevel string

// The three security levels. There is no fourth: USM has no privacy without
// authentication (RFC 3414 section 3.1.1), which is why Check refuses a
// privacy protocol alone.
const (
	SNMPNoAuthNoPriv SNMPSecurityLevel = "noAuthNoPriv"
	SNMPAuthNoPriv   SNMPSecurityLevel = "authNoPriv"
	SNMPAuthPriv     SNMPSecurityLevel = "authPriv"
)

// MinSNMPPassphraseLength is the shortest USM passphrase accepted: RFC 3414
// section 11.2 asks for at least eight characters, and net-snmp refuses a
// shorter one outright, so a shorter one could not configure the agent it is
// meant for.
const MinSNMPPassphraseLength = 8

// MinSNMPCommunityLength is the shortest community accepted without the
// operator's explicit allowance. It is the length below which a value cannot
// be masked by value without scrubbing ordinary words from a run's output
// (internal/redact.MinLiteralLength and internal/credential.MinSecretLength,
// which a test in internal/credential holds equal to it): a community of
// "public" would turn "republic" into "re********". A shorter one is masked
// by shape only.
const MinSNMPCommunityLength = 8

// maxSNMPUserLength is RFC 3414's bound on a USM user name (usmUserName,
// SnmpAdminString (SIZE(1..32))).
const maxSNMPUserLength = 32

// SNMPCredential is the SNMP half of a credential: a community for SNMPv1
// and SNMPv2c, or a USM user for SNMPv3 with its protocols and passphrases.
// Which version a request speaks is not here: a community serves v1 and v2c
// alike, and which of the two a device answers is the device's to say.
//
// String, GoString and MarshalJSON print a marker for each secret, never its
// value, so a debug print or a log line cannot leak one.
type SNMPCredential struct {
	// Community is the v1/v2c community, or empty.
	Community string
	// User is the v3 USM user name, or empty.
	User string
	// AuthProtocol and AuthPassphrase are the user's authentication, both or
	// neither.
	AuthProtocol   SNMPAuthProtocol
	AuthPassphrase string
	// PrivProtocol and PrivPassphrase are the user's privacy, both or
	// neither, and only with authentication.
	PrivProtocol   SNMPPrivProtocol
	PrivPassphrase string
}

// SNMPOf reads the SNMP half of a flattened credential; a missing key is that
// field's zero value.
func SNMPOf(secrets map[string]string) SNMPCredential {
	return SNMPCredential{
		Community:      secrets[SecretSNMPCommunity],
		User:           secrets[SecretSNMPUser],
		AuthProtocol:   SNMPAuthProtocol(secrets[SecretSNMPAuthProtocol]),
		AuthPassphrase: secrets[SecretSNMPAuthPassphrase],
		PrivProtocol:   SNMPPrivProtocol(secrets[SecretSNMPPrivProtocol]),
		PrivPassphrase: secrets[SecretSNMPPrivPassphrase],
	}
}

// Secrets is c as flattened credential keys, an empty field absent rather
// than present and empty, as internal/credential.Flatten writes every key.
func (c SNMPCredential) Secrets() map[string]string {
	out := make(map[string]string, 6)
	for key, value := range map[string]string{
		SecretSNMPCommunity:      c.Community,
		SecretSNMPUser:           c.User,
		SecretSNMPAuthProtocol:   string(c.AuthProtocol),
		SecretSNMPAuthPassphrase: c.AuthPassphrase,
		SecretSNMPPrivProtocol:   string(c.PrivProtocol),
		SecretSNMPPrivPassphrase: c.PrivPassphrase,
	} {
		if value != "" {
			out[key] = value
		}
	}
	return out
}

// Empty reports whether c carries no SNMP field at all.
func (c SNMPCredential) Empty() bool { return c == SNMPCredential{} }

// ShortCommunity reports whether c's community is set and shorter than
// MinSNMPCommunityLength, so it can be masked by shape only.
func (c SNMPCredential) ShortCommunity() bool {
	return c.Community != "" && len(c.Community) < MinSNMPCommunityLength
}

// Level is the security level c's USM user authenticates at, or "" when c has
// no USM user. It is derived, never stored: no authentication is
// noAuthNoPriv, authentication alone authNoPriv, and both authPriv.
func (c SNMPCredential) Level() SNMPSecurityLevel {
	switch {
	case c.User == "":
		return ""
	case c.AuthProtocol == "":
		return SNMPNoAuthNoPriv
	case c.PrivProtocol == "":
		return SNMPAuthNoPriv
	default:
		return SNMPAuthPriv
	}
}

// snmpSecretMarker is what String, GoString and MarshalJSON print for a set
// secret, and snmpUnsetMarker for an unset one; neither varies with the value.
const (
	snmpSecretMarker = "<redacted, set>"
	snmpUnsetMarker  = "<not set>"
)

// snmpMarker is snmpSecretMarker for a set value and snmpUnsetMarker
// otherwise.
func snmpMarker(value string) string {
	if value != "" {
		return snmpSecretMarker
	}
	return snmpUnsetMarker
}

// String implements fmt.Stringer, printing the user and the protocols and a
// marker for each secret.
func (c SNMPCredential) String() string {
	return fmt.Sprintf("wire.SNMPCredential{Community:%s, User:%q, AuthProtocol:%q, AuthPassphrase:%s, PrivProtocol:%q, PrivPassphrase:%s}",
		snmpMarker(c.Community), c.User, c.AuthProtocol, snmpMarker(c.AuthPassphrase), c.PrivProtocol, snmpMarker(c.PrivPassphrase))
}

// GoString implements fmt.GoStringer, so %#v prints String's markers too.
func (c SNMPCredential) GoString() string { return c.String() }

// MarshalJSON implements json.Marshaler with String's markers, since
// encoding/json (and slog's JSON handler) would otherwise print every field.
func (c SNMPCredential) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Community      string
		User           string
		AuthProtocol   SNMPAuthProtocol
		AuthPassphrase string
		PrivProtocol   SNMPPrivProtocol
		PrivPassphrase string
	}{snmpMarker(c.Community), c.User, c.AuthProtocol, snmpMarker(c.AuthPassphrase), c.PrivProtocol, snmpMarker(c.PrivPassphrase)})
}

package filters

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/ssh"
)

// ParseJWTPayloadUnverified decodes token's payload claims WITHOUT
// verifying its signature, using golang-jwt/jwt/v5's own ParseUnverified
// (the "existing dependency's own unverified-parse mode" this phase's
// checklist names) rather than a hand-rolled base64.RawURLEncoding
// split. Returns nil if token is not even structurally a JWT (wrong
// number of dot-separated segments, unparseable base64, non-JSON
// payload).
//
// This function's name, and this paragraph, exist to make one thing
// unmistakable: a successful, non-nil result here means the payload was
// readable, and says nothing whatsoever about whether the token's
// signature is valid, whether it has expired, or whether it was ever
// issued by a trusted party. jwt.NewParser().ParseUnverified's own doc
// comment carries the identical warning for the same reason. Never use
// this result to authorize or authenticate anything; use it only to
// inspect claims from a token whose authenticity has already been
// established some other way (or does not need to be, e.g. a runbook
// author eyeballing a token's own expiry during a debugging session).
// This phase's own Pattern Entry Gate names exactly this confusion as
// its expected rejection.
func ParseJWTPayloadUnverified(token string) map[string]any {
	if len(token) > MaxStructuredInputBytes {
		return nil
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return nil
	}
	return map[string]any(claims)
}

// ParseX509Certificate parses a PEM-encoded X.509 certificate into its
// subject, issuer, validity window and subject alternative names.
// not_before/not_after are RFC 3339 strings in UTC, this Part's own
// established "ISO8601" convention (pkg/filters/timeconvert.go's
// parseISO8601), so a certificate's expiry can be handed straight to
// Phase 55's own IsPast/IsExpiringWithin without a reformat in between.
// Returns nil if pemCert is not a decodable PEM block or the block's
// contents do not parse as a certificate.
func ParseX509Certificate(pemCert string) map[string]any {
	if len(pemCert) > MaxStructuredInputBytes {
		return nil
	}
	block, _ := pem.Decode([]byte(pemCert))
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}

	dnsNames := make([]any, len(cert.DNSNames))
	for i, n := range cert.DNSNames {
		dnsNames[i] = n
	}
	ips := make([]any, len(cert.IPAddresses))
	for i, ip := range cert.IPAddresses {
		ips[i] = ip.String()
	}

	return map[string]any{
		"subject":       cert.Subject.String(),
		"issuer":        cert.Issuer.String(),
		"not_before":    cert.NotBefore.UTC().Format(time.RFC3339),
		"not_after":     cert.NotAfter.UTC().Format(time.RFC3339),
		"serial_number": cert.SerialNumber.String(),
		"dns_names":     dnsNames,
		"ip_addresses":  ips,
	}
}

// PEMToDER converts pem's first PEM block to its base64-encoded DER
// form. A trailing block or trailing non-PEM text after the first block
// is silently ignored, matching how a real PEM bundle (a certificate
// followed by its chain, say) is routinely handed to something that
// only wants the first entry. Returns "" if no PEM block decodes at
// all. DER itself is binary; base64 is this package's own established
// text encoding for binary output (matching IsValidBase64's own
// precedent), not raw CEL bytes, since every other filter in this Part
// represents binary data as an encoded string rather than introducing a
// second, cel.BytesType-based convention this codebase has never needed
// before.
func PEMToDER(pemStr string) string {
	if len(pemStr) > MaxStructuredInputBytes {
		return ""
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(block.Bytes)
}

// pemBlockTypePattern is the character class DERToPEM requires
// blockType to match: uppercase letters, digits, spaces and hyphens,
// covering every standard PEM label this codebase has any reason to
// produce ("CERTIFICATE", "PUBLIC KEY", "RSA PRIVATE KEY", "X509 CRL",
// ...). This is a real, load-bearing check, not a cosmetic one:
// verified directly against encoding/pem's own source
// (src/encoding/pem/pem.go's Encode) that pem.Block.Type is written into
// the output completely unvalidated -- Encode only checks that a
// Headers *key* contains no colon, never Type -- so a blockType
// containing a newline reaches pem.EncodeToMemory's output as a real
// newline, letting a caller smuggle an entire second "-----BEGIN ...
// -----" block (or an extra header line) into what a reader would
// otherwise trust as one clean PEM block. This was found by reading the
// stdlib's own source after writing an initial doc comment that assumed
// pem.Encode already refused this, which it does not; see
// FAILURE_PATTERNS.md.
var pemBlockTypePattern = regexp.MustCompile(`^[A-Z0-9 -]+$`)

// DERToPEM wraps base64-encoded DER bytes as a PEM block of the named
// type (e.g. "CERTIFICATE", "PUBLIC KEY"), the inverse of PEMToDER.
// Returns "" if der is not valid base64 or blockType does not match
// pemBlockTypePattern.
func DERToPEM(der, blockType string) string {
	if len(der) > MaxStructuredInputBytes || len(blockType) > MaxInputBytes {
		return ""
	}
	if !pemBlockTypePattern.MatchString(blockType) {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(der)
	if err != nil {
		return ""
	}
	// pem.EncodeToMemory returns nil only when pem.Encode's own single
	// validation fails: a Headers map key containing a colon. This call
	// never sets Headers, so that path is provably unreachable here; the
	// check stays because pem.EncodeToMemory's own documented contract
	// says it can return nil, and this codebase checks every documented
	// failure return rather than assuming one specific caller's inputs
	// can never trigger it.
	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: raw})
	if encoded == nil {
		return ""
	}
	return string(encoded)
}

// SSHPublicKeyToPEM converts an OpenSSH authorized_keys public key line
// (e.g. "ssh-ed25519 AAAA... comment") to PEM/PKIX form, the same format
// crypto/x509.ParsePKIXPublicKey reads. Returns "" if authorizedKey does
// not parse as an SSH public key, or parses as a key type with no
// underlying crypto.PublicKey representation (ssh.CryptoPublicKey is
// unimplemented for a certificate-wrapped key, in particular; this
// function converts a bare public key, not an SSH certificate).
func SSHPublicKeyToPEM(authorizedKey string) string {
	if len(authorizedKey) > MaxInputBytes {
		return ""
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey))
	if err != nil {
		return ""
	}
	cryptoKey, ok := pubKey.(ssh.CryptoPublicKey)
	if !ok {
		return ""
	}
	der, err := x509.MarshalPKIXPublicKey(cryptoKey.CryptoPublicKey())
	if err != nil {
		return ""
	}
	// Type is the fixed literal "PUBLIC KEY" and Headers is never set, so
	// pem.EncodeToMemory's own single failure mode (a colon in a Headers
	// key) can never trigger here; see DERToPEM's identical comment for
	// why the check stays anyway.
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if encoded == nil {
		return ""
	}
	return string(encoded)
}

// PEMToSSHPublicKey converts a PEM/PKIX public key to OpenSSH
// authorized_keys form, the inverse of SSHPublicKeyToPEM. Returns "" if
// pem does not decode, or its contents do not parse as one of the key
// types ssh.NewPublicKey accepts (RSA, DSA, ECDSA on the P-256/P-384/
// P-521 curves, or Ed25519).
func PEMToSSHPublicKey(pemStr string) string {
	if len(pemStr) > MaxInputBytes {
		return ""
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return ""
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return ""
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
}

// ParseDistinguishedName parses an Active Directory / LDAP distinguished
// name (RFC 4514 shaped, e.g. "CN=John Doe,OU=Sales,DC=example,DC=com")
// into a map from attribute type to the list of its values, in order:
// most DNs have one value per type, but "DC" routinely repeats (one
// component per domain label), so every value is collected, not just
// the last one.
//
// Explicit, documented scope, not a full RFC 4514 implementation: a
// multi-valued RDN (two attributeTypeAndValue pairs joined by an
// unescaped "+" within one comma-separated component, e.g.
// "OU=Sales+L=NYC") is rejected rather than mis-parsed as a single
// value containing a literal "+L=NYC" -- an honest refusal beats a
// plausible-looking wrong answer. A "#"-prefixed hex-encoded attribute
// value (RFC 4514's own escape for a raw BER-encoded value) is likewise
// rejected: this function reads a human-typed or directory-tool-printed
// DN, not that encoding's own edge case.
func ParseDistinguishedName(dn string) map[string]any {
	if len(dn) > MaxInputBytes {
		return nil
	}
	components, ok := splitDNComponents(dn)
	if !ok {
		return nil
	}

	// components always has at least one entry (splitDNComponents appends
	// one even for dn == ""), and every iteration below either adds a key
	// to result or returns nil immediately, so result always has at
	// least one entry by the time the loop finishes normally: there is no
	// "parsed successfully but found nothing" case to check for here.
	result := map[string]any{}
	for _, comp := range components {
		key, value, ok := splitDNComponent(comp)
		if !ok {
			return nil
		}
		existing, _ := result[key].([]any)
		result[key] = append(existing, value)
	}
	return result
}

// splitDNComponents splits dn on unescaped commas, preserving each
// component's own backslash escapes for splitDNComponent/unescapeDN to
// resolve afterward. Reports false on a trailing unescaped backslash,
// which has no following character to escape.
func splitDNComponents(dn string) ([]string, bool) {
	var components []string
	var current strings.Builder
	escaped := false
	for _, r := range dn {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			current.WriteRune(r)
			escaped = true
		case r == ',':
			components = append(components, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		return nil, false
	}
	components = append(components, current.String())
	return components, true
}

// splitDNComponent splits one "type=value" component on its first
// unescaped "=", trims surrounding whitespace from both sides, and
// unescapes value. Reports false if there is no unescaped "=" at all,
// the type is empty, value begins with "#" (a raw hex-encoded value,
// out of scope), or comp contains an unescaped "+" (a multi-valued RDN,
// also out of scope; see ParseDistinguishedName's own doc comment for
// why both are refused rather than mis-parsed).
func splitDNComponent(comp string) (key, value string, ok bool) {
	if containsUnescaped(comp, '+') {
		return "", "", false
	}
	idx := -1
	escaped := false
	for i, r := range comp {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '=':
			idx = i
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(comp[:idx])
	rawValue := strings.TrimSpace(comp[idx+1:])
	if key == "" || strings.HasPrefix(rawValue, "#") {
		return "", "", false
	}
	return key, unescapeDN(rawValue), true
}

// containsUnescaped reports whether comp contains target outside of any
// backslash escape sequence.
func containsUnescaped(comp string, target rune) bool {
	escaped := false
	for _, r := range comp {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == target {
			return true
		}
	}
	return false
}

// unescapeDN removes the backslash from every backslash-escaped
// character in s (RFC 4514's own escaping rule: a backslash escapes the
// single character that follows it, including another backslash).
func unescapeDN(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

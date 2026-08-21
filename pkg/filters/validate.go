package filters

import (
	"encoding/base64"
	"encoding/json"
	"net/mail"

	"github.com/google/uuid"
	yaml "go.yaml.in/yaml/v3"
)

// IsValidFQDN reports whether s is a syntactically valid fully qualified
// domain name: RFC 1035/1123 label rules (each label 1 to 63 characters,
// alphanumeric with an interior hyphen permitted, no leading or trailing
// hyphen), at least two labels (matching FQDNToHostname/HostnameToFQDN's
// own "a dot means FQDN-shaped" convention elsewhere in this package),
// and a total length of at most 253 bytes. This is a single, hand-rolled
// parser, not a regex approximation running alongside a second, separate
// notion of validity: IsValidFQDN's own answer and this parser's own
// success are the same check, so the two can never disagree at the
// margin. ASCII only; an internationalized domain name must be
// punycode-encoded by the caller first.
func IsValidFQDN(s string) bool {
	if len(s) > MaxInputBytes {
		return false
	}
	return parseFQDN(s)
}

func parseFQDN(s string) bool {
	// A single trailing dot is legal DNS root-zone syntax ("example.com.").
	if len(s) > 1 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	labelStart := 0
	labelCount := 0
	for i := 0; i <= len(s); i++ {
		if i < len(s) && s[i] != '.' {
			continue
		}
		if !isValidDNSLabel(s[labelStart:i]) {
			return false
		}
		labelCount++
		labelStart = i + 1
	}
	return labelCount >= 2
}

func isValidDNSLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if alnum {
			continue
		}
		if c == '-' && i != 0 && i != len(label)-1 {
			continue
		}
		return false
	}
	return true
}

// IsValidEmail reports whether s is a syntactically valid, UPN-shaped
// email address: a bare "user@domain" address, not a decorated RFC 5322
// mailbox carrying a display name, angle brackets or a comment. It
// parses s with the standard library's own net/mail.ParseAddress (the
// real RFC 5322 parser, not a hand-rolled approximation that could
// disagree with it at the margin), then additionally requires the parsed
// address to have no display name and to reproduce s exactly: net/mail
// happily accepts "Name <user@domain>" as a valid address, which is not
// UPN-shaped, and this second check is what rejects it without needing a
// second, independent grammar.
func IsValidEmail(s string) bool {
	if len(s) > MaxInputBytes {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return false
	}
	return addr.Name == "" && addr.Address == s
}

// IsValidUUID reports whether s parses as a valid UUID via
// github.com/google/uuid.Parse, the same real parser GenerateUUIDv4
// already depends on, accepting any of the RFC 4122 textual forms that
// package recognizes (canonical dashed, Microsoft GUID braces, URN, and
// the bare 32-hex-digit form).
func IsValidUUID(s string) bool {
	if len(s) > MaxInputBytes {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}

// IsValidBase64 reports whether s is valid standard, padded base64 (RFC
// 4648 section 4), decoded via the standard library's own
// encoding/base64.StdEncoding.DecodeString. Treated as document-shaped
// (MaxStructuredInputBytes, 1 MiB) rather than flat-scalar-shaped
// (MaxInputBytes, 4096 bytes): a legitimate base64 payload this filter
// is meant to check, a certificate or a small config blob, routinely
// exceeds 4 KiB.
func IsValidBase64(s string) bool {
	if len(s) > MaxStructuredInputBytes {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	return err == nil
}

// IsValidJSON reports whether s is syntactically valid JSON, via the
// standard library's own encoding/json.Valid: exactly the check
// encoding/json.Unmarshal performs internally before decoding, so
// IsValidJSON returning true is definitionally the same event as
// Unmarshal succeeding on the same input, not a separate approximation
// that could disagree with it. Treated as document-shaped
// (MaxStructuredInputBytes), matching this package's other JSON-handling
// functions (YAMLToJSON, JSONToYAML).
func IsValidJSON(s string) bool {
	if len(s) > MaxStructuredInputBytes {
		return false
	}
	return json.Valid([]byte(s))
}

// IsValidYAML reports whether s is syntactically valid YAML, decoded via
// the same go.yaml.in/yaml/v3 library and the same "unmarshal into a
// bare any" shape YAMLToJSON already uses elsewhere in this package, so
// there is exactly one YAML-parsing code path in this file family for
// the two to agree on. Treated as document-shaped
// (MaxStructuredInputBytes).
func IsValidYAML(s string) bool {
	if len(s) > MaxStructuredInputBytes {
		return false
	}
	var v any
	return yaml.Unmarshal([]byte(s), &v) == nil
}

// IsValidPort reports whether port falls in the valid TCP/UDP port
// range, 1 through 65535 (0 is reserved to mean "let the OS choose", not
// a real port a device could be listening on). Takes a Go int directly,
// the same convention ValidateVLAN/ValidateASN already use for a
// numeric-range predicate: a caller holding a string-typed port number
// pipes it through filters.safeInt first, the same composition this
// package's own cast filters were built for.
func IsValidPort(port int) bool {
	return port >= 1 && port <= 65535
}

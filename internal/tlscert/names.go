// Names: what a subject alternative name is allowed to be, checked at the
// edge where a caller can still be told which setting is wrong.
//
// A DNS name on a certificate is encoded as an IA5String, which is ASCII and
// nothing else. Hand x509.CreateCertificate a name with an accented letter
// in it and the failure arrives as "x509: SAN dNSName is malformed" from
// deep inside the signing call, naming neither the value nor the setting it
// came from. The operator who typed a hostname with a non-breaking space in
// it, or pasted an international domain in its display form, has no way to
// get from that message back to what they did.
//
// So the check happens here, once, before anything is signed, and it says
// what is wrong with which value. cmd/controller adds the name of the
// environment variable, which is the last piece an operator needs.
package tlscert

import (
	"fmt"
	"net"
	"strings"
	"unicode"
)

// maxDNSName and maxDNSLabel are the length limits a DNS name has to
// respect: 253 characters in total, 63 per dot-separated label.
const (
	maxDNSName  = 253
	maxDNSLabel = 63
)

// ValidateNames rejects every entry that could not be put on a certificate.
//
// Blank entries are skipped rather than rejected, matching what namesFrom
// does with them, so a caller can hand this the result of splitting an
// environment variable without pre-cleaning it: a value written as
// "a, b," is three entries and one of them is empty on purpose.
//
// An entry that parses as an IP address is accepted unconditionally, because
// every form net.ParseIP accepts is a form x509 can encode.
func ValidateNames(names []string) error {
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if net.ParseIP(trimmed) != nil {
			continue
		}
		if err := validateDNSName(trimmed); err != nil {
			// The raw entry, not the trimmed one, because an operator
			// looking for what to fix is looking at what they typed.
			return fmt.Errorf("%q is not a name a certificate can carry: %w", name, err)
		}
	}
	return nil
}

// validateDNSName reports why name cannot be a DNS subject alternative name,
// or nil when it can.
func validateDNSName(name string) error {
	if len(name) > maxDNSName {
		return fmt.Errorf("it is %d characters long and a DNS name stops at %d", len(name), maxDNSName)
	}
	for _, r := range name {
		if r > unicode.MaxASCII {
			return fmt.Errorf("it holds the non-ASCII character %q, and a certificate can only carry ASCII names; write an international domain in its punycode form (xn--...) instead", r)
		}
	}

	labels := strings.Split(name, ".")
	for i, label := range labels {
		// A wildcard is legitimate and is only ever the whole of the first
		// label: "*.example.test" is a name, "*x.example.test" and
		// "example.*.test" are not.
		if label == "*" {
			if i == 0 && len(labels) > 1 {
				continue
			}
			return fmt.Errorf("a wildcard is only allowed as the whole of the first label, as in *.%s", strings.Join(labels[1:], "."))
		}
		if err := validateDNSLabel(label); err != nil {
			return err
		}
	}
	return nil
}

// validateDNSLabel reports why one dot-separated part of a name is not
// usable.
func validateDNSLabel(label string) error {
	if label == "" {
		return fmt.Errorf("it has an empty part, so it has two dots in a row or a dot at one end")
	}
	if len(label) > maxDNSLabel {
		return fmt.Errorf("the part %q is %d characters long and a single part of a DNS name stops at %d", label, len(label), maxDNSLabel)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("the part %q starts or ends with a hyphen", label)
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			// Underscore is not valid in a hostname but is common in
			// internal names, and x509 encodes it happily, so rejecting it
			// would refuse a name that works.
		default:
			return fmt.Errorf("the part %q holds %q, and a name may only use letters, digits, hyphens and underscores", label, rune(c))
		}
	}
	return nil
}

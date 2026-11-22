// Package tlsfacts: the certificate's serial number as tls.certificate.serial
// records it, and the serials it leaves out.
package tlsfacts

import (
	"crypto/x509"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// MaxSerialDigits is the longest serial recorded, in hex digits of the
// number's bytes. RFC 5280 section 4.1.2.2 allows a serial 20 octets as
// encoded, and a 20-byte number whose top bit is set encodes as 21 (a 00 pad
// byte first, so it does not read as negative), which some CAs issue anyway:
// 40 digits keeps those. A longer one is left out with a warning, since a
// device writes its own certificate and a fact is kept for as long as its
// history is.
const MaxSerialDigits = 40

// serialText returns the serial number of cert (presented by the named
// device) as tls.certificate.serial records it: filters.SerialHex's form,
// lowercase hex of the number's bytes, two digits a byte ("00" for a zero
// serial, which RFC 5280 forbids and some devices write anyway). When the
// serial is not recorded it returns "" and
// the warning that says why: a serial longer than MaxSerialDigits, or a
// negative one, which Go's parser refuses unless GODEBUG holds
// x509negativeserial=1, and whose hex would read the same as the positive
// number of the same size.
func serialText(device string, cert *x509.Certificate) (string, string) {
	n := cert.SerialNumber
	switch {
	case n == nil:
		return "", fmt.Sprintf("device %q presented a certificate with no serial number, so no %s fact is recorded", device, FactSerial)
	case n.Sign() < 0:
		return "", fmt.Sprintf("device %q presented a certificate whose serial number is negative, which RFC 5280 forbids, so no %s fact is recorded", device, FactSerial)
	}
	// The one form, shared with parseX509Certificate's serial_hex.
	text := filters.SerialHex(n)
	if len(text) > MaxSerialDigits {
		return "", fmt.Sprintf("device %q presented a certificate whose serial number is %d hex digits, more than the %d recorded (RFC 5280 allows 20 encoded octets), so no %s fact is recorded", device, len(text), MaxSerialDigits, FactSerial)
	}
	return text, ""
}

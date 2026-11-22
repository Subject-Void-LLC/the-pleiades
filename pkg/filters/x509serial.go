// Package filters: a certificate serial number in hex, the form
// parseX509Certificate's serial_hex and the tls.certificate.serial fact
// share.
package filters

import (
	"encoding/hex"
	"math/big"
)

// SerialHex returns serial in lowercase hex of its bytes, two digits a byte,
// which is the form "openssl x509 -serial" prints, lowercased: 0x0a1b is
// "0a1b", 128 is "80" (no 00 pad byte before a first digit 8 to f), and zero
// is "00". It returns "" for nil or for a negative serial, whose hex would
// read the same as the positive number's; Go's certificate parser refuses a
// negative serial unless GODEBUG holds x509negativeserial=1.
func SerialHex(serial *big.Int) string {
	switch {
	case serial == nil || serial.Sign() < 0:
		return ""
	case serial.Sign() == 0:
		// big.Int's Bytes is empty for zero; OpenSSL prints it as 00.
		return "00"
	}
	return hex.EncodeToString(serial.Bytes())
}

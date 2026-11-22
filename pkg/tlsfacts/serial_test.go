// Tests for tls.certificate.serial over real handshakes on loopback, and on
// a certificate OpenSSL made: the serial's exact form, a serial too long to
// record, and the serials RFC 5280 forbids (zero, which is recorded, and
// negative, which is not).
package tlsfacts_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tlsfacts"
)

// serialOf returns the number whose big-endian bytes are b.
func serialOf(b []byte) *big.Int { return new(big.Int).SetBytes(b) }

// TestRecord_TheSerialIsLowercaseHexOfItsBytes: a certificate a real server
// presents has its serial recorded as a stat and a fact, two lowercase hex
// digits a byte as openssl x509 -serial prints it (checked by hand against
// OpenSSL 3.0.2: 0 is 00, 128 is 80, 0x0a1b is 0A1B), with no warning. A
// leading zero digit is kept, a top bit set adds no 00 byte, a zero serial
// reads 00, and a 20-byte serial (40 digits) fits even with its top bit
// set, which encodes as 21 octets (a 00 pad byte first).
func TestRecord_TheSerialIsLowercaseHexOfItsBytes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		serial *big.Int
		want   string
	}{
		{"a leading zero digit", big.NewInt(0x0A1B), "0a1b"},
		{"the top bit set", big.NewInt(128), "80"},
		{"zero, which RFC 5280 forbids", big.NewInt(0), "00"},
		{"twenty bytes, top bit set: 21 encoded octets", serialOf(bytes.Repeat([]byte{0xAB}, 20)), strings.Repeat("ab", 20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings, seen := seenOn(t, newServerWithSerial(t, tc.serial))
			rc := &recorder{stats: map[string]any{}, facts: map[string]any{}}
			warnings, err := tlsfacts.Record(rc, "api1", settings, seen, time.Now())
			if err != nil || warnings != nil {
				t.Fatalf("warnings %q, %v", warnings, err)
			}
			if rc.stats[tlsfacts.FactSerial] != tc.want || rc.facts[tlsfacts.FactSerial] != tc.want {
				t.Errorf("stat %v, fact %v, want %s", rc.stats[tlsfacts.FactSerial], rc.facts[tlsfacts.FactSerial], tc.want)
			}
		})
	}
}

// TestRecord_OpenSSLsOwnSerialReadsAsOpenSSLPrintsIt: the certificate
// OpenSSL made for pkg/devicetls's vector, a 20-byte serial whose top bit
// is clear (so 20 encoded octets), is
// recorded as "openssl x509 -noout -serial" prints it
// (serial=7264FB1A0BD9A9E5806469100D6BED4B4E08A337), in lowercase.
func TestRecord_OpenSSLsOwnSerialReadsAsOpenSSLPrintsIt(t *testing.T) {
	data, err := os.ReadFile("../devicetls/testdata/spki-vector.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("the vector holds no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	_, seen := devicetls.WithPresented(t.Context())
	seen.Record(tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{cert}})
	rc := &recorder{stats: map[string]any{}, facts: map[string]any{}}
	// The vector is valid until 2126, so it draws no warning.
	if _, err := tlsfacts.Record(rc, "api1", devicetls.Settings{}, seen, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := rc.facts[tlsfacts.FactSerial]; got != "7264fb1a0bd9a9e5806469100d6bed4b4e08a337" {
		t.Errorf("serial %v, want OpenSSL's own in lowercase", got)
	}
}

// TestRecord_ASerialTooLongIsLeftOutWithAWarning: a 21-byte serial (42
// digits), which Go's own parser accepts, is recorded neither as a stat nor
// as a fact, and says why, while the certificate's other facts are recorded.
func TestRecord_ASerialTooLongIsLeftOutWithAWarning(t *testing.T) {
	srv := newServerWithSerial(t, serialOf(bytes.Repeat([]byte{0x01}, 21)))
	settings, seen := seenOn(t, srv)
	rc := &recorder{stats: map[string]any{}, facts: map[string]any{}}
	warnings, err := tlsfacts.Record(rc, "api1", settings, seen, time.Now())
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "42 hex digits, more than the 40") {
		t.Fatalf("warnings %q, %v", warnings, err)
	}
	if _, ok := rc.facts[tlsfacts.FactSerial]; ok {
		t.Errorf("a 42-digit serial was recorded as a fact: %v", rc.facts[tlsfacts.FactSerial])
	}
	if _, ok := rc.stats[tlsfacts.FactSerial]; ok {
		t.Errorf("a 42-digit serial was recorded as a stat: %v", rc.stats[tlsfacts.FactSerial])
	}
	if len(rc.facts) != len(tlsfacts.FactKeys)-1 || rc.facts[tlsfacts.FactFingerprint] != devicetls.Fingerprint(srv.Certificate()) {
		t.Errorf("facts %v, want every other certificate fact", rc.facts)
	}
}

// TestRecord_ANegativeSerialIsLeftOutWithAWarning: Go's parser refuses a
// certificate with a negative serial (so a verified handshake never presents
// one, unless GODEBUG holds x509negativeserial=1), and a certificate that has
// one anyway, or none at all, is recorded with no serial and a warning, since
// a negative serial's hex would read as the positive number's.
func TestRecord_ANegativeSerialIsLeftOutWithAWarning(t *testing.T) {
	srv := newServerWithSerial(t, big.NewInt(5))
	cert := srv.Certificate()
	// The control: the same certificate with its serial's one byte (02 01 05,
	// after the version a0 03 02 01 02) made -5 does not parse.
	at := bytes.Index(cert.Raw, []byte{0xa0, 0x03, 0x02, 0x01, 0x02, 0x02, 0x01, 0x05})
	if at < 0 {
		t.Fatal("control: the serial 5 is not where the encoding puts it")
	}
	negative := bytes.Clone(cert.Raw)
	negative[at+7] = 0xfb
	if _, err := x509.ParseCertificate(negative); err == nil || !strings.Contains(err.Error(), "negative serial") {
		t.Fatalf("a negative serial parsed: %v", err)
	}
	for _, tc := range []struct {
		name, want string
		serial     *big.Int
	}{
		{"negative", "is negative, which RFC 5280 forbids", big.NewInt(-5)},
		{"none", "with no serial number", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			odd := *cert
			odd.SerialNumber = tc.serial
			_, seen := devicetls.WithPresented(t.Context())
			seen.Record(tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{&odd}})
			settings, _ := seenOn(t, srv)
			rc := &recorder{stats: map[string]any{}, facts: map[string]any{}}
			warnings, err := tlsfacts.Record(rc, "api1", settings, seen, time.Now())
			if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], tc.want) {
				t.Fatalf("warnings %q, %v", warnings, err)
			}
			if _, ok := rc.facts[tlsfacts.FactSerial]; ok || len(rc.facts) != len(tlsfacts.FactKeys)-1 {
				t.Errorf("facts %v, want every certificate fact but the serial", rc.facts)
			}
		})
	}
}

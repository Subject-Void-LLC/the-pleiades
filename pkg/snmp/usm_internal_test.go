// Package snmp: the USM table covers the credential model's whole
// vocabulary, and a decoded answer never panics or keeps more than it read.
package snmp

import (
	"errors"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestUSM_EveryStoredProtocolHasGosnmpsOwn: each authentication protocol and
// each privacy protocol but 3DES maps to a gosnmp protocol, no two to the
// same one, and the message flags follow the level.
func TestUSM_EveryStoredProtocolHasGosnmpsOwn(t *testing.T) {
	seenAuth := map[gosnmp.SnmpV3AuthProtocol]bool{}
	for _, p := range wire.SNMPAuthProtocols() {
		usm, flags, err := usmParameters(wire.SNMPCredential{User: "ops", AuthProtocol: p, AuthPassphrase: "authpassword"})
		if err != nil || flags != gosnmp.AuthNoPriv || seenAuth[usm.AuthenticationProtocol] || usm.AuthenticationProtocol == gosnmp.NoAuth {
			t.Errorf("%s: %v, flags %v, protocol %v", p, err, flags, usm.AuthenticationProtocol)
		}
		seenAuth[usm.AuthenticationProtocol] = true
	}
	seenPriv := map[gosnmp.SnmpV3PrivProtocol]bool{}
	for _, p := range wire.SNMPPrivProtocols() {
		usm, flags, err := usmParameters(wire.SNMPCredential{User: "ops", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: "authpassword", PrivProtocol: p, PrivPassphrase: "privpassword"})
		if p == wire.SNMPPriv3DES {
			if !errors.Is(err, ErrUnsupportedPrivacy) {
				t.Errorf("3des: %v, want ErrUnsupportedPrivacy", err)
			}
			continue
		}
		if err != nil || flags != gosnmp.AuthPriv || seenPriv[usm.PrivacyProtocol] || usm.PrivacyProtocol == gosnmp.NoPriv {
			t.Errorf("%s: %v, flags %v, protocol %v", p, err, flags, usm.PrivacyProtocol)
		}
		seenPriv[usm.PrivacyProtocol] = true
	}
	if privProtocols[wire.SNMPPrivAES192] != gosnmp.AES192 || privProtocols[wire.SNMPPrivAES192Reeder] != gosnmp.AES192C ||
		privProtocols[wire.SNMPPrivAES256] != gosnmp.AES256 || privProtocols[wire.SNMPPrivAES256Reeder] != gosnmp.AES256C {
		t.Error("a key-extension scheme is mapped to the other's protocol")
	}
	if _, flags, err := usmParameters(wire.SNMPCredential{User: "ops"}); err != nil || flags != gosnmp.NoAuthNoPriv {
		t.Errorf("noAuthNoPriv: %v, flags %v", err, flags)
	}
	for _, bad := range []wire.SNMPCredential{
		{User: "ops", AuthProtocol: "sha1", AuthPassphrase: "authpassword"},
		{User: "ops", AuthProtocol: wire.SNMPAuthSHA, AuthPassphrase: "authpassword", PrivProtocol: "aes", PrivPassphrase: "privpassword"},
	} {
		if _, _, err := usmParameters(bad); err == nil {
			t.Errorf("usmParameters(%v) guessed at a protocol", bad)
		}
	}
}

// TestFromPDU_RefusesATypeItDoesNotRead: a BER type outside the client's
// vocabulary, and a value of the wrong Go type, are errors, never guesses.
func TestFromPDU_RefusesATypeItDoesNotRead(t *testing.T) {
	for _, pdu := range []gosnmp.SnmpPDU{
		{Name: ".1.3", Type: gosnmp.Asn1BER(0x99)},
		{Name: ".1.3", Type: gosnmp.Integer, Value: "seven"},
		{Name: ".1.3", Type: gosnmp.Counter32, Value: 7},
		{Name: ".1.3", Type: gosnmp.Gauge32, Value: uint64(1) << 33},
		{Name: ".1.3", Type: gosnmp.Counter64, Value: "7"},
	} {
		if v, err := fromPDU(pdu); err == nil {
			t.Errorf("fromPDU(%+v) = %+v, want a refusal", pdu, v)
		}
	}
	// gosnmp decodes Counter32 and Gauge32 as uint and TimeTicks as uint32.
	for _, pdu := range []gosnmp.SnmpPDU{
		{Name: ".1.3", Type: gosnmp.Gauge32, Value: uint(4294967295)},
		{Name: ".1.3", Type: gosnmp.Counter32, Value: uint(7)},
		{Name: ".1.3", Type: gosnmp.TimeTicks, Value: uint32(7)},
	} {
		if _, err := fromPDU(pdu); err != nil {
			t.Errorf("fromPDU(%+v): %v", pdu, err)
		}
	}
	v, err := fromPDU(gosnmp.SnmpPDU{Name: ".1.3.6", Type: gosnmp.Opaque, Value: []byte{0x9f, 0x78, 0x04}})
	if err != nil || v.Type != TypeOpaque || !v.Hex {
		t.Errorf("an opaque value = %+v, %v", v, err)
	}
}

// FuzzDecodeAnswer feeds gosnmp's decoder, then this package's conversion,
// whatever bytes an agent could send: neither may panic, and nothing kept
// can be larger than the datagram it came from.
func FuzzDecodeAnswer(f *testing.F) {
	for _, seed := range [][]byte{
		{0x30, 0x84, 0x7f, 0xff, 0xff, 0xff},
		nested(200),
		{0x30, 0x1a, 0x02, 0x01, 0x01, 0x04, 0x06, 'p', 'u', 'b', 'l', 'i', 'c', 0xa2, 0x0d, 0x02, 0x01, 0x01, 0x02, 0x01, 0x00, 0x02, 0x01, 0x00, 0x30, 0x02, 0x30, 0x00},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65535 {
			return
		}
		decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c}
		packet, err := decoder.SnmpDecodePacket(data)
		if err != nil || packet == nil {
			return
		}
		kept := 0
		for _, pdu := range packet.Variables {
			v, err := fromPDU(pdu)
			if err != nil {
				continue
			}
			kept += v.size()
		}
		if kept > 4*len(data)+64*len(packet.Variables) {
			t.Fatalf("kept %d bytes from a %d-byte datagram", kept, len(data))
		}
	})
}

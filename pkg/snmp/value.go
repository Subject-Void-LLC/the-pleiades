// Package snmp: a variable binding as this platform reads and writes it, a
// typed value with one text form, so a value read can be written back
// exactly and a value recorded can be compared with the next one read.
package snmp

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
)

// Type is a variable binding's value type, by the name a runbook writes it.
type Type string

// The types. The first nine carry a value; the last three are an agent's
// answer that there is none (RFC 3416 section 4.2.1).
const (
	TypeInteger     Type = "integer"
	TypeOctetString Type = "octet-string"
	TypeOID         Type = "object-identifier"
	TypeIPAddress   Type = "ip-address"
	TypeCounter32   Type = "counter32"
	TypeGauge32     Type = "gauge32"
	TypeTimeTicks   Type = "timeticks"
	TypeCounter64   Type = "counter64"
	TypeOpaque      Type = "opaque"
	TypeNull        Type = "null"

	TypeNoSuchObject   Type = "no-such-object"
	TypeNoSuchInstance Type = "no-such-instance"
	TypeEndOfMibView   Type = "end-of-mib-view"
)

// SettableTypes are the types a SET may write, in the order a help text
// lists them. Counter32 and Counter64 are left out: RFC 2578 makes every
// counter read-only.
func SettableTypes() []Type {
	return []Type{TypeInteger, TypeOctetString, TypeOID, TypeIPAddress, TypeGauge32, TypeTimeTicks}
}

// Absent reports whether t says the agent holds no value.
func (t Type) Absent() bool {
	return t == TypeNoSuchObject || t == TypeNoSuchInstance || t == TypeEndOfMibView
}

// Varbind is one variable binding: an OID, its type and its value.
//
// Text is the value in one form for every type: a number in decimal, an OID
// dotted, an address dotted, and an octet string as itself when it is
// printable UTF-8, or as "0x" and lowercase hex when it is not (Hex says
// which), so any value read can be written back byte for byte. Bytes is the
// octet string's raw value.
type Varbind struct {
	OID   string
	Type  Type
	Text  string
	Hex   bool
	Bytes []byte
}

// size is how many bytes v's value holds, for the walk's bound.
func (v Varbind) size() int { return len(v.OID) + len(v.Text) + len(v.Bytes) }

// fromPDU converts one of gosnmp's variable bindings. An unknown type is
// refused rather than guessed at.
func fromPDU(pdu gosnmp.SnmpPDU) (Varbind, error) {
	v := Varbind{OID: strings.TrimPrefix(pdu.Name, ".")}
	switch pdu.Type {
	case gosnmp.Integer:
		n, ok := pdu.Value.(int)
		if !ok {
			return Varbind{}, errors.New("an integer with no value")
		}
		v.Type, v.Text = TypeInteger, strconv.Itoa(n)
	case gosnmp.OctetString, gosnmp.Opaque, gosnmp.BitString:
		b, _ := pdu.Value.([]byte)
		v.Type = TypeOctetString
		if pdu.Type != gosnmp.OctetString {
			v.Type = TypeOpaque
		}
		v.Bytes = append([]byte(nil), b...)
		v.Text, v.Hex = octetText(b)
	case gosnmp.ObjectIdentifier:
		s, _ := pdu.Value.(string)
		v.Type, v.Text = TypeOID, strings.TrimPrefix(s, ".")
	case gosnmp.IPAddress:
		s, _ := pdu.Value.(string)
		v.Type, v.Text = TypeIPAddress, s
	case gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Uinteger32:
		// gosnmp decodes Counter32 and Gauge32 as uint, TimeTicks and
		// Uinteger32 as uint32; a value past 32 bits is no such type.
		n, ok := unsigned(pdu.Value)
		if !ok || n > math.MaxUint32 {
			return Varbind{}, errors.New("an unsigned 32-bit value with no value, or past 32 bits")
		}
		v.Type, v.Text = map[gosnmp.Asn1BER]Type{gosnmp.Counter32: TypeCounter32, gosnmp.Gauge32: TypeGauge32,
			gosnmp.TimeTicks: TypeTimeTicks, gosnmp.Uinteger32: TypeGauge32}[pdu.Type], strconv.FormatUint(n, 10)
	case gosnmp.Counter64:
		n, ok := unsigned(pdu.Value)
		if !ok {
			return Varbind{}, errors.New("a counter64 with no value")
		}
		v.Type, v.Text = TypeCounter64, strconv.FormatUint(n, 10)
	case gosnmp.Null:
		v.Type = TypeNull
	case gosnmp.NoSuchObject:
		v.Type = TypeNoSuchObject
	case gosnmp.NoSuchInstance:
		v.Type = TypeNoSuchInstance
	case gosnmp.EndOfMibView:
		v.Type = TypeEndOfMibView
	default:
		return Varbind{}, fmt.Errorf("a value of BER type 0x%02x, which this client does not read", byte(pdu.Type))
	}
	return v, nil
}

// unsigned reads a decoded unsigned value, whichever unsigned Go type gosnmp
// gave it.
func unsigned(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint:
		return uint64(n), true
	case uint32:
		return uint64(n), true
	case uint64:
		return n, true
	}
	return 0, false
}

// octetText is b as printable text, or as "0x" and hex when it holds a byte
// that is not printable UTF-8 (a control character, a MAC address, an engine
// ID), and which of the two it is.
func octetText(b []byte) (string, bool) {
	if utf8.Valid(b) && !strings.ContainsFunc(string(b), func(r rune) bool { return !unicode.IsPrint(r) && r != ' ' }) {
		return string(b), false
	}
	return "0x" + hex.EncodeToString(b), true
}

// HexType is the type a runbook writes for an octet string given as hex:
// "0x" or nothing, then two hex digits a byte.
const HexType Type = "hex-string"

// Value builds the binding a SET writes: oid, of type t (one of
// SettableTypes, or HexType for an octet string written as hex), holding
// text. It refuses a value its type cannot hold.
func Value(oid string, t Type, text string) (Varbind, error) {
	canon, err := ParseOID(oid)
	if err != nil {
		return Varbind{}, err
	}
	v := Varbind{OID: canon, Type: t, Text: text}
	switch t {
	case TypeInteger:
		n, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			return Varbind{}, fmt.Errorf("an integer value is a whole number from %d to %d", math.MinInt32, math.MaxInt32)
		}
		v.Text = strconv.FormatInt(n, 10)
	case TypeGauge32, TypeTimeTicks:
		n, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return Varbind{}, fmt.Errorf("a %s value is a whole number from 0 to %d", t, uint32(math.MaxUint32))
		}
		v.Text = strconv.FormatUint(n, 10)
	case TypeOctetString:
		if len(text) > maxOctetString {
			return Varbind{}, fmt.Errorf("an octet-string value is at most %d bytes", maxOctetString)
		}
		v.Bytes = []byte(text)
		v.Text, v.Hex = octetText(v.Bytes)
	case HexType:
		b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X"))
		if err != nil || len(b) > maxOctetString {
			return Varbind{}, fmt.Errorf("a hex-string value is two hex digits a byte, at most %d bytes, optionally after 0x", maxOctetString)
		}
		v.Type, v.Bytes = TypeOctetString, b
		v.Text, v.Hex = octetText(b)
	case TypeOID:
		if v.Text, err = ParseOID(text); err != nil {
			return Varbind{}, fmt.Errorf("an object-identifier value: %w", err)
		}
	case TypeIPAddress:
		ip := net.ParseIP(text).To4()
		if ip == nil {
			return Varbind{}, errors.New("an ip-address value is an IPv4 address, dotted (SNMP's IpAddress holds four bytes)")
		}
		v.Text = ip.String()
	default:
		return Varbind{}, fmt.Errorf("type %q cannot be written; a SET writes one of %s", t, typeList())
	}
	return v, nil
}

// maxOctetString is the longest octet string a SET writes: RFC 2578's
// OCTET STRING (SIZE (0..65535)).
const maxOctetString = 65535

// typeList is the types a SET writes, for an error.
func typeList() string {
	names := make([]string, 0, len(SettableTypes())+1)
	for _, t := range SettableTypes() {
		names = append(names, string(t))
	}
	return strings.Join(append(names, string(HexType)), ", ")
}

// pdu is v as gosnmp sends it in a SET.
func (v Varbind) pdu() (gosnmp.SnmpPDU, error) {
	p := gosnmp.SnmpPDU{Name: "." + v.OID}
	switch v.Type {
	case TypeInteger:
		n, err := strconv.ParseInt(v.Text, 10, 32)
		if err != nil {
			return p, err
		}
		p.Type, p.Value = gosnmp.Integer, int(n)
	case TypeGauge32, TypeTimeTicks:
		n, err := strconv.ParseUint(v.Text, 10, 32)
		if err != nil {
			return p, err
		}
		p.Type, p.Value = map[Type]gosnmp.Asn1BER{TypeGauge32: gosnmp.Gauge32, TypeTimeTicks: gosnmp.TimeTicks}[v.Type], uint32(n)
	case TypeOctetString:
		p.Type, p.Value = gosnmp.OctetString, append([]byte(nil), v.Bytes...)
	case TypeOID:
		p.Type, p.Value = gosnmp.ObjectIdentifier, "."+v.Text
	case TypeIPAddress:
		ip := net.ParseIP(v.Text).To4()
		if ip == nil {
			return p, errors.New("not an IPv4 address")
		}
		p.Type, p.Value = gosnmp.IPAddress, []byte(ip)
	default:
		return p, fmt.Errorf("type %q cannot be written", v.Type)
	}
	return p, nil
}

// Same reports whether a and b hold the same typed value: a SET's read back
// against what it wrote, or its read before against what it would write.
func Same(a, b Varbind) bool {
	if a.Type != b.Type {
		return false
	}
	if a.Type == TypeOctetString || a.Type == TypeOpaque {
		return string(a.Bytes) == string(b.Bytes)
	}
	return a.Text == b.Text
}

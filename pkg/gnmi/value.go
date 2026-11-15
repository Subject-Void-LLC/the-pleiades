// Package gnmi: the values a device answers (gNMI's TypedValue), decoded
// into one form every caller reads alike, canonical JSON, and the values this
// client sends. A device's value is third-party input: a JSON value is held
// to a size and a nesting depth before it is parsed, a leaf-list to a count,
// and an answer breaking either is refused whole.
package gnmi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// The bounds on a value.
const (
	// MaxValueDepth is the deepest a JSON value may nest. A YANG tree of a
	// real device is a few dozen levels deep; ten thousand nested arrays
	// in a few kilobytes is an attack on the parser, not configuration.
	MaxValueDepth = 128
	// MaxLeafList bounds one leaf-list's elements.
	MaxLeafList = 1 << 16
)

// The types a Value came in, TypedValue's own member names.
const (
	TypeJSONIETF   = "json_ietf"
	TypeJSON       = "json"
	TypeString     = "string"
	TypeInt        = "int"
	TypeUint       = "uint"
	TypeBool       = "bool"
	TypeDouble     = "double"
	TypeFloat      = "float"
	TypeDecimal    = "decimal"
	TypeBytes      = "bytes"
	TypeASCII      = "ascii"
	TypeLeafList   = "leaflist"
	TypeProtoBytes = "proto_bytes"
	TypeAny        = "any"
	TypeNone       = "none"
)

// Value is one value a device answered.
type Value struct {
	// Type is the TypedValue member it came in (TypeJSONIETF and the rest),
	// TypeNone for an update that carried none.
	Type string
	// JSON is the value as canonical JSON: object members sorted, numbers
	// as written, no white space, so two reads of the same value compare
	// equal byte for byte. A string, bytes or ASCII value is a JSON string
	// (bytes in base64, as protojson writes them), a number a JSON number
	// (a float that JSON cannot write, NaN or an infinity, a string), a
	// leaf-list an array, and an Any its type URL and base64 bytes.
	JSON json.RawMessage
}

// decodeValue decodes tv, refusing a JSON value past the bounds or one that
// does not parse, and a leaf-list past MaxLeafList or nested in another.
func decodeValue(tv *gpb.TypedValue) (Value, error) {
	return decodeTyped(tv, false)
}

// decodeTyped is decodeValue, inLeafList saying whether tv is an element of
// a leaf-list, where a further leaf-list is refused.
func decodeTyped(tv *gpb.TypedValue, inLeafList bool) (Value, error) {
	if tv == nil || tv.GetValue() == nil {
		return Value{Type: TypeNone, JSON: json.RawMessage("null")}, nil
	}
	scalar := func(kind string, v any) (Value, error) {
		raw, err := marshal(v)
		return Value{Type: kind, JSON: raw}, err
	}
	switch v := tv.GetValue().(type) {
	case *gpb.TypedValue_JsonIetfVal:
		raw, err := CanonicalJSON(v.JsonIetfVal)
		return Value{Type: TypeJSONIETF, JSON: raw}, err
	case *gpb.TypedValue_JsonVal:
		raw, err := CanonicalJSON(v.JsonVal)
		return Value{Type: TypeJSON, JSON: raw}, err
	case *gpb.TypedValue_StringVal:
		return scalar(TypeString, v.StringVal)
	case *gpb.TypedValue_AsciiVal:
		return scalar(TypeASCII, v.AsciiVal)
	case *gpb.TypedValue_IntVal:
		return Value{Type: TypeInt, JSON: json.RawMessage(strconv.FormatInt(v.IntVal, 10))}, nil
	case *gpb.TypedValue_UintVal:
		return Value{Type: TypeUint, JSON: json.RawMessage(strconv.FormatUint(v.UintVal, 10))}, nil
	case *gpb.TypedValue_BoolVal:
		return Value{Type: TypeBool, JSON: json.RawMessage(strconv.FormatBool(v.BoolVal))}, nil
	case *gpb.TypedValue_DoubleVal:
		return floatValue(TypeDouble, v.DoubleVal, 64)
	case *gpb.TypedValue_FloatVal: // deprecated in gNMI 0.7.0, and still sent by some targets
		return floatValue(TypeFloat, float64(v.FloatVal), 32)
	case *gpb.TypedValue_DecimalVal: // deprecated in gNMI 0.7.0, and still sent by some targets
		return decimalValue(v.DecimalVal)
	case *gpb.TypedValue_BytesVal:
		return scalar(TypeBytes, base64.StdEncoding.EncodeToString(v.BytesVal))
	case *gpb.TypedValue_ProtoBytes:
		return scalar(TypeProtoBytes, base64.StdEncoding.EncodeToString(v.ProtoBytes))
	case *gpb.TypedValue_AnyVal:
		return scalar(TypeAny, map[string]string{"type_url": v.AnyVal.GetTypeUrl(), "value": base64.StdEncoding.EncodeToString(v.AnyVal.GetValue())})
	case *gpb.TypedValue_LeaflistVal:
		if inLeafList {
			return Value{}, errors.New("the device sent a leaf-list inside a leaf-list")
		}
		elements := v.LeaflistVal.GetElement()
		if len(elements) > MaxLeafList {
			return Value{}, fmt.Errorf("the device sent a leaf-list of %d elements, more than %d", len(elements), MaxLeafList)
		}
		parts := make([]json.RawMessage, 0, len(elements))
		for _, element := range elements {
			decoded, err := decodeTyped(element, true)
			if err != nil {
				return Value{}, err
			}
			parts = append(parts, decoded.JSON)
		}
		return scalar(TypeLeafList, parts)
	}
	return Value{}, fmt.Errorf("the device sent a value of a type this client does not know (%T)", tv.GetValue())
}

// floatValue writes f as a JSON number at its own precision, or as a string
// for a value JSON has no number for.
func floatValue(kind string, f float64, bits int) (Value, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		raw, err := marshal(strconv.FormatFloat(f, 'g', -1, bits))
		return Value{Type: kind, JSON: raw}, err
	}
	return Value{Type: kind, JSON: json.RawMessage(strconv.FormatFloat(f, 'g', -1, bits))}, nil
}

// decimalValue writes gNMI's deprecated Decimal64 (digits scaled by ten to
// the minus precision) as an exact JSON number.
func decimalValue(d *gpb.Decimal64) (Value, error) {
	precision := d.GetPrecision()
	if precision > 18 {
		return Value{}, fmt.Errorf("the device sent a decimal of precision %d, more than the 18 a decimal64 has", precision)
	}
	digits := strconv.FormatInt(d.GetDigits(), 10)
	negative := digits[0] == '-'
	if negative {
		digits = digits[1:]
	}
	for len(digits) <= int(precision) {
		digits = "0" + digits
	}
	text := digits
	if precision > 0 {
		text = digits[:len(digits)-int(precision)] + "." + digits[len(digits)-int(precision):]
	}
	if negative {
		text = "-" + text
	}
	return Value{Type: TypeDecimal, JSON: json.RawMessage(text)}, nil
}

// CanonicalJSON reads raw, one JSON value, and returns it canonical: object
// members sorted, numbers as written, no white space, HTML characters left as
// they are. It refuses more than MaxMessageBytes, nesting deeper than
// MaxValueDepth (checked before anything is decoded), and anything that is
// not exactly one JSON value.
func CanonicalJSON(raw []byte) (json.RawMessage, error) {
	if len(raw) > MaxMessageBytes {
		return nil, fmt.Errorf("the JSON value is larger than %d bytes", MaxMessageBytes)
	}
	if err := checkDepth(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("the value is not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the value holds more than one JSON value")
	}
	return marshal(v)
}

// checkDepth refuses JSON nesting deeper than MaxValueDepth, reading tokens
// only, so a hostile document is refused before a tree is built for it.
func checkDepth(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the value is not JSON: %w", err)
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
			if depth > MaxValueDepth {
				return fmt.Errorf("the JSON value nests deeper than %d", MaxValueDepth)
			}
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
}

// marshal is v as compact JSON with no HTML escaping, maps sorted by key.
func marshal(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(b.Bytes(), "\n")), nil
}

// typedJSON is raw, one JSON value already checked by CanonicalJSON, as the
// TypedValue member enc names: json_ietf_val or json_val.
func typedJSON(raw []byte, enc Encoding) (*gpb.TypedValue, error) {
	switch enc {
	case EncodingJSONIETF:
		return &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: raw}}, nil
	case EncodingJSON:
		return &gpb.TypedValue{Value: &gpb.TypedValue_JsonVal{JsonVal: raw}}, nil
	}
	return nil, fmt.Errorf("a value is sent as json or json_ietf, not %s", enc)
}

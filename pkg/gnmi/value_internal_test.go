// Package gnmi: decoding every TypedValue member a device may send, and the
// bounds a hostile one is refused by.
package gnmi

import (
	"math"
	"strings"
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/protobuf/types/known/anypb"
)

// TestDecodeValue_EveryMember decodes each member into its canonical JSON.
func TestDecodeValue_EveryMember(t *testing.T) {
	for _, tc := range []struct {
		in   *gpb.TypedValue
		kind string
		json string
	}{
		{nil, TypeNone, "null"},
		{&gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(` {"b": 1, "a": [true, "x<y"]} `)}}, TypeJSONIETF, `{"a":[true,"x<y"],"b":1}`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_JsonVal{JsonVal: []byte(`12345678901234567890`)}}, TypeJSON, `12345678901234567890`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: `UP "now"`}}, TypeString, `"UP \"now\""`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_AsciiVal{AsciiVal: "show"}}, TypeASCII, `"show"`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_IntVal{IntVal: -7}}, TypeInt, `-7`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: math.MaxUint64}}, TypeUint, `18446744073709551615`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_BoolVal{BoolVal: true}}, TypeBool, `true`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: 0.1}}, TypeDouble, `0.1`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: math.Inf(1)}}, TypeDouble, `"+Inf"`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_FloatVal{FloatVal: 1.5}}, TypeFloat, `1.5`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_DecimalVal{DecimalVal: &gpb.Decimal64{Digits: -1234, Precision: 2}}}, TypeDecimal, `-12.34`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_DecimalVal{DecimalVal: &gpb.Decimal64{Digits: 5, Precision: 3}}}, TypeDecimal, `0.005`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_DecimalVal{DecimalVal: &gpb.Decimal64{Digits: 42}}}, TypeDecimal, `42`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_BytesVal{BytesVal: []byte{0, 1}}}, TypeBytes, `"AAE="`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_ProtoBytes{ProtoBytes: []byte{2}}}, TypeProtoBytes, `"Ag=="`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_AnyVal{AnyVal: &anypb.Any{TypeUrl: "x/y", Value: []byte{3}}}}, TypeAny, `{"type_url":"x/y","value":"Aw=="}`},
		{&gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: &gpb.ScalarArray{Element: []*gpb.TypedValue{
			{Value: &gpb.TypedValue_StringVal{StringVal: "a"}}, {Value: &gpb.TypedValue_UintVal{UintVal: 2}},
		}}}}, TypeLeafList, `["a",2]`},
	} {
		got, err := decodeValue(tc.in)
		if err != nil || got.Type != tc.kind || string(got.JSON) != tc.json {
			t.Errorf("decodeValue(%v) = %s %s, %v, want %s %s", tc.in, got.Type, got.JSON, err, tc.kind, tc.json)
		}
	}
}

// TestDecodeValue_RefusesWhatBreaksABound refuses each hostile value whole.
func TestDecodeValue_RefusesWhatBreaksABound(t *testing.T) {
	deep := strings.Repeat("[", MaxValueDepth+1) + strings.Repeat("]", MaxValueDepth+1)
	nested := &gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: &gpb.ScalarArray{Element: []*gpb.TypedValue{
		{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: &gpb.ScalarArray{}}},
	}}}}
	long := &gpb.TypedValue{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: &gpb.ScalarArray{Element: make([]*gpb.TypedValue, MaxLeafList+1)}}}
	for name, tv := range map[string]*gpb.TypedValue{
		"too deep":     {Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(deep)}},
		"not json":     {Value: &gpb.TypedValue_JsonVal{JsonVal: []byte(`{"a":`)}},
		"two values":   {Value: &gpb.TypedValue_JsonVal{JsonVal: []byte(`1 2`)}},
		"nested list":  nested,
		"long list":    long,
		"precision 19": {Value: &gpb.TypedValue_DecimalVal{DecimalVal: &gpb.Decimal64{Digits: 1, Precision: 19}}},
	} {
		if v, err := decodeValue(tv); err == nil {
			t.Errorf("%s: decodeValue = %s, want it refused", name, v.JSON)
		}
	}
	// The depth bound is not the byte bound: the deep document is tiny.
	if len(deep) > 1024 {
		t.Fatalf("the deep fixture is %d bytes; it must be small to test depth alone", len(deep))
	}
	// Exactly at the bound is accepted.
	at := strings.Repeat("[", MaxValueDepth) + strings.Repeat("]", MaxValueDepth)
	if _, err := CanonicalJSON([]byte(at)); err != nil {
		t.Errorf("CanonicalJSON at depth %d = %v", MaxValueDepth, err)
	}
}

// TestEncodings_NameEveryMember names each encoding and reads it back.
func TestEncodings_NameEveryMember(t *testing.T) {
	for i, name := range encodingNames {
		e, err := ParseEncoding(strings.ToUpper(name))
		if err != nil || int(e) != i || e.String() != name || int32(e.proto()) != int32(i) {
			t.Errorf("ParseEncoding(%q) = %v, %v", name, e, err)
		}
	}
	if _, err := ParseEncoding("xml"); err == nil {
		t.Error("ParseEncoding(xml) = nil")
	}
	if Encoding(9).String() != "unknown(9)" {
		t.Errorf("Encoding(9) = %s", Encoding(9))
	}
	if _, err := typedJSON([]byte("1"), EncodingASCII); err == nil {
		t.Error("typedJSON in ASCII = nil")
	}
}

// TestReadCapabilities_DropsWhatItCannotKeep keeps a server's good entries
// and counts the rest.
func TestReadCapabilities_DropsWhatItCannotKeep(t *testing.T) {
	got := readCapabilities(&gpb.CapabilityResponse{
		GNMIVersion:        "0.7.0",
		SupportedEncodings: []gpb.Encoding{gpb.Encoding_PROTO, gpb.Encoding_JSON, gpb.Encoding_PROTO, gpb.Encoding(42)},
		SupportedModels: []*gpb.ModelData{
			{Name: "b", Version: "1"}, {Name: "a", Version: "2"}, {Name: "a", Version: "2"},
			{Name: ""}, {Name: "bad\x1bname"}, {Name: strings.Repeat("x", 2000)},
		},
	})
	if got.Version != "0.7.0" || len(got.Encodings) != 2 || got.Encodings[0] != EncodingJSON {
		t.Errorf("version and encodings = %+v", got)
	}
	if len(got.Models) != 2 || got.Models[0].Name != "a" || got.Dropped != 4 || len(got.DroppedSamples) != 4 {
		t.Errorf("models = %+v, dropped %d %v", got.Models, got.Dropped, got.DroppedSamples)
	}
}

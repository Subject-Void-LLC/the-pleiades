// Package gnmi: fuzzing the decoders that read what a person or a device
// wrote (a path as text, a path and a value as protobuf), and benchmarking a
// subscription's per-update cost, the one operation here that runs
// continuously rather than once.
package gnmi

import (
	"encoding/json"
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/protobuf/proto"
)

// FuzzParsePath holds the parser to never panicking, and to reading back
// whatever it accepted, through String, as the same path: a value it accepts
// can never become another element when written out again.
func FuzzParsePath(f *testing.F) {
	for _, seed := range []string{
		"/", "/a/b[k=v]/c", "openconfig:/system/config", `/a[k=x\]y][j=\\]`, "/a[k=*]/.../b",
		"/interfaces/interface[name=ethernet-1/1]/subinterfaces/subinterface[index=0]",
		"/a//b", "/a[k=v", "x:/", "/Cisco-IOS-XE-native:native/hostname",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParsePath(s)
		if err != nil {
			return
		}
		back, err := ParsePath(p.String())
		if err != nil {
			t.Fatalf("ParsePath(%q) accepted it as %q, which does not parse: %v", s, p.String(), err)
		}
		if !back.Equal(p) {
			t.Fatalf("ParsePath(%q) = %q, read back as %q", s, p, back)
		}
		if p.Validate(true) != nil {
			t.Fatalf("ParsePath(%q) returned a path its own Validate refuses", s)
		}
	})
}

// FuzzPathFromProto feeds arbitrary protobuf bytes as a device's Path, and
// holds whatever is accepted to the text parser's rules: written out, it
// parses back as the same path.
func FuzzPathFromProto(f *testing.F) {
	for _, seed := range []*gpb.Path{
		{},
		{Origin: "openconfig", Elem: []*gpb.PathElem{{Name: "interfaces"}, {Name: "interface", Key: map[string]string{"name": "e]1\\"}}}},
		{Element: []string{"a", "b"}},
	} {
		raw, err := proto.Marshal(seed)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var pp gpb.Path
		if proto.Unmarshal(raw, &pp) != nil {
			return
		}
		p, err := pathFromProto(&pp)
		if err != nil {
			return
		}
		back, err := ParsePath(p.String())
		if err != nil || !back.Equal(p) {
			t.Fatalf("a device's path %q does not read back as itself: %q, %v", p.String(), back, err)
		}
	})
}

// FuzzDecodeTypedValue feeds arbitrary protobuf bytes as a device's
// TypedValue, and holds every accepted value to being valid JSON, and its
// canonical form to being stable: canonical JSON read again is itself.
func FuzzDecodeTypedValue(f *testing.F) {
	for _, seed := range []*gpb.TypedValue{
		{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"openconfig-interfaces:interface":[{"name":"e1"}]}`)}},
		{Value: &gpb.TypedValue_StringVal{StringVal: "UP"}},
		{Value: &gpb.TypedValue_DecimalVal{DecimalVal: &gpb.Decimal64{Digits: -5, Precision: 3}}},
		{Value: &gpb.TypedValue_LeaflistVal{LeaflistVal: &gpb.ScalarArray{Element: []*gpb.TypedValue{{Value: &gpb.TypedValue_IntVal{IntVal: 1}}}}}},
		{Value: &gpb.TypedValue_JsonVal{JsonVal: []byte(`[[[[[[1]]]]]]`)}},
	} {
		raw, err := proto.Marshal(seed)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var tv gpb.TypedValue
		if proto.Unmarshal(raw, &tv) != nil {
			return
		}
		v, err := decodeValue(&tv)
		if err != nil {
			return
		}
		if !json.Valid(v.JSON) {
			t.Fatalf("decodeValue accepted %v as %q, which is not JSON", &tv, v.JSON)
		}
		again, err := CanonicalJSON(v.JSON)
		if err != nil || string(again) != string(v.JSON) {
			t.Fatalf("canonical JSON %q is not stable: %q, %v", v.JSON, again, err)
		}
	})
}

// streamUpdate is one SubscribeResponse in Stratum's shape: one counter
// leaf of one interface, the update a STREAM subscription repeats.
func streamUpdate(b *testing.B) []byte {
	b.Helper()
	raw, err := proto.Marshal(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: &gpb.Notification{
		Timestamp: 1794769560822892516,
		Update: []*gpb.Update{{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "interfaces"}, {Name: "interface", Key: map[string]string{"name": "s1-eth1"}},
				{Name: "state"}, {Name: "counters"}, {Name: "in-broadcast-pkts"},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 12345}},
		}},
	}}})
	if err != nil {
		b.Fatal(err)
	}
	return raw
}

// BenchmarkStreamUpdate_Decode is the client's cost per STREAM update, from
// the bytes gRPC hands over to the decoded notification a handler gets: the
// protobuf unmarshal gRPC does, then this package's path and value decoding.
// Run with -benchmem; allocs/op is allocations per update.
func BenchmarkStreamUpdate_Decode(b *testing.B) {
	raw := streamUpdate(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		var resp gpb.SubscribeResponse
		if err := proto.Unmarshal(raw, &resp); err != nil {
			b.Fatal(err)
		}
		if _, err := decodeNotification(resp.GetUpdate()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStreamUpdate_DecodeOnly is BenchmarkStreamUpdate_Decode without
// the protobuf unmarshal, which is gRPC's cost rather than this package's.
func BenchmarkStreamUpdate_DecodeOnly(b *testing.B) {
	raw := streamUpdate(b)
	var resp gpb.SubscribeResponse
	if err := proto.Unmarshal(raw, &resp); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := decodeNotification(resp.GetUpdate()); err != nil {
			b.Fatal(err)
		}
	}
}

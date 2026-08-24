package datastore_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// TestEncoding_NamesAreStableAndComplete guards the closed set. A new
// encoding added without a String case would render as "Encoding(4)" in
// every error message that mentioned it, which is exactly the failure
// an iota type is supposed to prevent rather than cause.
func TestEncoding_NamesAreStableAndComplete(t *testing.T) {
	want := map[datastore.Encoding]string{
		datastore.EncodingXML:      "xml",
		datastore.EncodingJSON:     "json",
		datastore.EncodingJSONIETF: "json_ietf",
		datastore.EncodingProtobuf: "protobuf",
	}
	for enc, name := range want {
		if got := enc.String(); got != name {
			t.Errorf("Encoding(%d).String() = %q, want %q", uint8(enc), got, name)
		}
		if !enc.Valid() {
			t.Errorf("Encoding(%d).Valid() = false, want true", uint8(enc))
		}
	}

	// One past the last declared value. Go does not make an iota type
	// closed at the language level, so a caller's uint8 conversion can
	// produce this and an implementation has to be able to say so.
	beyond := datastore.Encoding(uint8(datastore.EncodingProtobuf) + 1)
	if beyond.Valid() {
		t.Error("an undeclared Encoding reported itself Valid")
	}
	if got := beyond.String(); !strings.Contains(got, "Encoding(") {
		t.Errorf("an undeclared Encoding rendered as %q, want something that names it as undeclared", got)
	}
}

// TestOperation_MergeIsTheZeroValue pins a safety property rather than
// a naming one: a caller that forgets to set an operation gets the
// least destructive of the three, not a surprising one.
func TestOperation_MergeIsTheZeroValue(t *testing.T) {
	var zero datastore.Operation
	if zero != datastore.OperationMerge {
		t.Fatalf("the zero Operation is %s, want merge: a forgotten field must default to the least destructive behavior", zero)
	}
}

func TestOperation_NamesMatchNetconfsOwnVocabulary(t *testing.T) {
	want := map[datastore.Operation]string{
		datastore.OperationMerge:   "merge",
		datastore.OperationReplace: "replace",
		datastore.OperationDelete:  "delete",
	}
	for op, name := range want {
		if got := op.String(); got != name {
			t.Errorf("Operation(%d).String() = %q, want %q", uint8(op), got, name)
		}
		if !op.Valid() {
			t.Errorf("Operation(%d).Valid() = false, want true", uint8(op))
		}
	}
	if datastore.Operation(99).Valid() {
		t.Error("an undeclared Operation reported itself Valid")
	}
}

func TestParseOperation(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want datastore.Operation
	}{
		{"merge", datastore.OperationMerge},
		{"replace", datastore.OperationReplace},
		{"delete", datastore.OperationDelete},
		{"  MERGE  ", datastore.OperationMerge},
		{"Replace", datastore.OperationReplace},
	} {
		got, err := datastore.ParseOperation(tc.in)
		if err != nil {
			t.Errorf("ParseOperation(%q) error = %v, want nil", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseOperation(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}

	// A misspelling must be refused at the boundary with the valid
	// names listed, never defaulted to merge somewhere further in: a
	// runbook that meant "replace" and silently got "merge" leaves
	// configuration the author believed they had removed.
	for _, bad := range []string{"", "remove", "MERGE!", "mergedelete", "0"} {
		_, err := datastore.ParseOperation(bad)
		if err == nil {
			t.Errorf("ParseOperation(%q) error = nil, want a refusal", bad)
			continue
		}
		for _, name := range []string{"merge", "replace", "delete"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("ParseOperation(%q) error = %q, want it to list %q as a valid name", bad, err, name)
			}
		}
	}
}

func TestPath_RootAndRendering(t *testing.T) {
	var root datastore.Path
	if !root.IsRoot() {
		t.Error("the zero Path is not IsRoot, but every implementation must accept it as the whole datastore")
	}
	if got := root.String(); got != "/" {
		t.Errorf("Path{}.String() = %q, want %q", got, "/")
	}

	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "native", Namespace: "http://cisco.com/ns/yang/Cisco-IOS-XE-native"},
		{Name: "interface"},
		{Name: "Loopback", Keys: map[string]string{"name": "8990"}},
	}}
	if p.IsRoot() {
		t.Error("a Path with elements reported IsRoot")
	}
	if got, want := p.String(), "/native/interface/Loopback[name=8990]"; got != want {
		t.Errorf("Path.String() = %q, want %q", got, want)
	}
}

// TestPath_StringIsStableAcrossCalls matters because the rendering goes
// into error messages: a message that changed shape between two runs of
// the same failure would be needlessly hard to search for.
func TestPath_StringIsStableAcrossCalls(t *testing.T) {
	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "entry", Keys: map[string]string{"zeta": "1", "alpha": "2", "mid": "3", "beta": "4"}},
	}}
	first := p.String()
	for i := 0; i < 100; i++ {
		if got := p.String(); got != first {
			t.Fatalf("Path.String() is not stable: %q then %q", first, got)
		}
	}
	if want := "/entry[alpha=2,beta=4,mid=3,zeta=1]"; first != want {
		t.Errorf("Path.String() = %q, want %q", first, want)
	}
}

func TestPayload_IsEmpty(t *testing.T) {
	if !(datastore.Payload{}).IsEmpty() {
		t.Error("the zero Payload is not IsEmpty")
	}
	if !(datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte{}}).IsEmpty() {
		t.Error("a Payload with a zero-length byte slice is not IsEmpty")
	}
	if (datastore.Payload{Bytes: []byte("<a/>")}).IsEmpty() {
		t.Error("a Payload with bytes reported IsEmpty")
	}
}

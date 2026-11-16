// Package gnmi_test: the client through pkg/datastore's port, against
// gnmitest's server: each operation applied and read back, and what the port
// cannot carry to gNMI refused before anything is sent.
package gnmi_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// TestDatastore_EachOperationThroughThePort merges, replaces and deletes one
// node through the port, reading each change back through it.
func TestDatastore_EachOperationThroughThePort(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	c, _ := clientOf(t, s)
	var store datastore.Store = c
	ctx := ctxOf(t)
	p := datastore.Path{Elem: []datastore.PathElem{{Name: "interface", Keys: map[string]string{"name": "lo5"}}}}
	read := func() string {
		t.Helper()
		got, err := store.GetConfig(ctx, p)
		if err != nil || got.Encoding != datastore.EncodingJSONIETF {
			t.Fatalf("GetConfig = %+v, %v", got, err)
		}
		return string(got.Bytes)
	}
	if got := read(); got != "{}" {
		t.Errorf("before = %s, want nothing", got)
	}
	if err := store.SetConfig(ctx, p, datastore.Payload{Encoding: datastore.EncodingJSONIETF, Bytes: []byte(`{"description":"a","mtu":1500}`)}, datastore.OperationMerge); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.Contains(got, `"description":"a"`) {
		t.Errorf("after a merge = %s", got)
	}
	if err := store.SetConfig(ctx, p, datastore.Payload{Encoding: datastore.EncodingJSON, Bytes: []byte(`{"mtu":9000}`)}, datastore.OperationReplace); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, "description") || !strings.Contains(got, "9000") {
		t.Errorf("after a replace = %s", got)
	}
	if err := store.SetConfig(ctx, p, datastore.Payload{}, datastore.OperationDelete); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "{}" {
		t.Errorf("after a delete = %s", got)
	}
}

// TestDatastore_RefusesWhatGNMICannotCarry refuses a namespace, an XML or
// protobuf payload, a payload on a delete and an unknown operation, with
// nothing sent.
func TestDatastore_RefusesWhatGNMICannotCarry(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	plain := datastore.Path{Elem: []datastore.PathElem{{Name: "system"}}}
	spaced := datastore.Path{Elem: []datastore.PathElem{{Name: "system", Namespace: "urn:ietf:params:xml:ns:yang:ietf-system"}}}
	doc := datastore.Payload{Encoding: datastore.EncodingJSONIETF, Bytes: []byte(`{}`)}
	for name, err := range map[string]error{
		"namespace, get":   func() error { _, err := c.GetConfig(ctx, spaced); return err }(),
		"namespace, set":   c.SetConfig(ctx, spaced, doc, datastore.OperationMerge),
		"xml":              c.SetConfig(ctx, plain, datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte("<a/>")}, datastore.OperationMerge),
		"protobuf":         c.SetConfig(ctx, plain, datastore.Payload{Encoding: datastore.EncodingProtobuf, Bytes: []byte{1}}, datastore.OperationMerge),
		"delete, payload":  c.SetConfig(ctx, plain, doc, datastore.OperationDelete),
		"unknown":          c.SetConfig(ctx, plain, doc, datastore.Operation(9)),
		"bad element name": c.SetConfig(ctx, datastore.Path{Elem: []datastore.PathElem{{Name: "a b"}}}, doc, datastore.OperationMerge),
	} {
		if err == nil {
			t.Errorf("%s: nil, want a refusal", name)
		}
	}
	if rpcs := s.RPCs(); len(rpcs) != 0 {
		t.Errorf("server saw %v", rpcs)
	}
}

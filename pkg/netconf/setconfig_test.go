package netconf

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// okDevice answers every RPC with <ok/>, echoing the message-id.
func okDevice(hello string) *fakeDevice {
	return &fakeDevice{
		hello: hello,
		respond: func(req string) string {
			return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` +
				messageIDOf(req) + `"><ok/></rpc-reply>`
		},
	}
}

const loopbackXML = `<Loopback><name>8990</name><description>pleiades</description></Loopback>`

// TestSetConfig_RequestsRollbackOnErrorWhenTheDeviceOffersIt is the
// safety property, not a cosmetic one: RFC 6241's default is
// stop-on-error, which leaves a rejected batch half applied on a device
// that has no candidate datastore to stage it in. A real Cisco IOS XE
// device is exactly that device and does advertise the capability.
func TestSetConfig_RequestsRollbackOnErrorWhenTheDeviceOffersIt(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "native", Namespace: "http://cisco.com/ns/yang/Cisco-IOS-XE-native"},
		{Name: "interface"},
	}}
	payload := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte(loopbackXML)}

	if err := s.SetConfig(context.Background(), p, payload, datastore.OperationMerge); err != nil {
		t.Fatalf("SetConfig() error = %v, want nil", err)
	}
	if req := d.lastRequest(t); !strings.Contains(req, "<error-option>rollback-on-error</error-option>") {
		t.Errorf("request =\n%s\nwant it to request rollback-on-error", req)
	}
}

func TestSetConfig_OmitsRollbackOnErrorWhenTheDeviceDoesNotOfferIt(t *testing.T) {
	hello := `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
		`<capability>urn:ietf:params:netconf:capability:writable-running:1.0</capability>` +
		`</capabilities><session-id>2</session-id></hello>`
	d := okDevice(hello)
	s := openAgainst(t, d, Options{})

	payload := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte(`<native/>`)}
	if err := s.SetConfig(context.Background(), datastore.Path{}, payload, datastore.OperationMerge); err != nil {
		t.Fatalf("SetConfig() error = %v, want nil", err)
	}
	if req := d.lastRequest(t); strings.Contains(req, "rollback-on-error") {
		t.Errorf("request =\n%s\nwant no rollback-on-error: the device never advertised it, and asking for an unadvertised capability is how a whole edit gets rejected", req)
	}
}

// TestSetConfig_ScopesTheOperationToTheInnermostElement is the most
// consequential construction detail in this package. With
// default-operation="replace" and a payload wrapped in ancestor
// containers, a device applies replace semantics TO THOSE CONTAINERS:
// replacing one interface's configuration would replace every
// interface. The only correct construction is default-operation="none"
// with an explicit operation attribute on the element the caller
// actually named.
func TestSetConfig_ScopesTheOperationToTheInnermostElement(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "native", Namespace: "http://cisco.com/ns/yang/Cisco-IOS-XE-native"},
		{Name: "interface"},
	}}
	payload := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte(loopbackXML)}

	if err := s.SetConfig(context.Background(), p, payload, datastore.OperationReplace); err != nil {
		t.Fatalf("SetConfig() error = %v, want nil", err)
	}

	req := d.lastRequest(t)
	if !strings.Contains(req, "<default-operation>none</default-operation>") {
		t.Errorf("request =\n%s\nwant default-operation none, so the ancestor containers this payload is wrapped in are NOT themselves replaced", req)
	}
	if !strings.Contains(req, `nc:operation="replace"`) {
		t.Errorf("request =\n%s\nwant an explicit nc:operation on the innermost element", req)
	}
	// The attribute must sit on <interface>, the element the caller
	// named, and not on <native>, its parent.
	if !strings.Contains(req, `<interface xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace">`) {
		t.Errorf("request =\n%s\nwant the operation attribute on <interface>, the deepest element the path names", req)
	}
}

func TestSetConfig_RootPayloadCarriesTheOperationAsTheDefault(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	payload := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte(`<native xmlns="urn:x"/>`)}
	if err := s.SetConfig(context.Background(), datastore.Path{}, payload, datastore.OperationReplace); err != nil {
		t.Fatalf("SetConfig() error = %v, want nil", err)
	}
	req := d.lastRequest(t)
	if !strings.Contains(req, "<default-operation>replace</default-operation>") {
		t.Errorf("request =\n%s\nwant default-operation replace: with a root payload there are no ancestor wrappers for it to affect", req)
	}
	if strings.Contains(req, "nc:operation") {
		t.Errorf("request =\n%s\nwant no per-element operation attribute for a root payload", req)
	}
}

func TestSetConfig_DeleteSendsAnEmptyElementWithTheOperationAttribute(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "native", Namespace: "http://cisco.com/ns/yang/Cisco-IOS-XE-native"},
		{Name: "interface"},
		{Name: "Loopback", Keys: map[string]string{"name": "8990"}},
	}}
	if err := s.SetConfig(context.Background(), p, datastore.Payload{}, datastore.OperationDelete); err != nil {
		t.Fatalf("SetConfig() error = %v, want nil", err)
	}

	req := d.lastRequest(t)
	if !strings.Contains(req, `nc:operation="delete"`) {
		t.Errorf("request =\n%s\nwant nc:operation delete", req)
	}
	// The list key must still be present inside the deleted element:
	// without it the device is being told to delete every Loopback.
	if !strings.Contains(req, "<name>8990</name>") {
		t.Errorf("request =\n%s\nwant the list key inside the deleted element, or the device is being asked to delete every entry in the list", req)
	}
}

func TestSetConfig_RefusesContradictoryArguments(t *testing.T) {
	nonEmpty := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte("<a/>")}
	somePath := datastore.Path{Elem: []datastore.PathElem{{Name: "native"}}}

	cases := []struct {
		name    string
		path    datastore.Path
		payload datastore.Payload
		op      datastore.Operation
		want    string
	}{
		{
			name: "delete with a payload", path: somePath, payload: nonEmpty, op: datastore.OperationDelete,
			want: "refusing rather than silently discarding",
		},
		{
			name: "delete at the root", path: datastore.Path{}, payload: datastore.Payload{}, op: datastore.OperationDelete,
			want: "refusing to delete the root",
		},
		{
			name: "merge with no payload", path: somePath, payload: datastore.Payload{}, op: datastore.OperationMerge,
			want: "needs a payload",
		},
		{
			name: "a non-XML payload", path: somePath, op: datastore.OperationMerge,
			payload: datastore.Payload{Encoding: datastore.EncodingJSONIETF, Bytes: []byte(`{}`)},
			want:    "NETCONF carries XML",
		},
		{
			name: "an undeclared operation", path: somePath, payload: nonEmpty, op: datastore.Operation(99),
			want: "unknown operation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := okDevice(realHelloPrelude)
			s := openAgainst(t, d, Options{})

			err := s.SetConfig(context.Background(), tc.path, tc.payload, tc.op)
			if err == nil {
				t.Fatalf("SetConfig() error = nil, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("SetConfig() error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestSetConfig_RefusesWritingToRunningWithoutTheCapability(t *testing.T) {
	hello := `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
		`<capability>urn:ietf:params:netconf:capability:candidate:1.0</capability>` +
		`</capabilities><session-id>3</session-id></hello>`
	d := okDevice(hello)
	s := openAgainst(t, d, Options{})

	payload := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte("<native/>")}
	err := s.SetConfig(context.Background(), datastore.Path{}, payload, datastore.OperationMerge)
	if err == nil {
		t.Fatal("SetConfig() error = nil, want a refusal: this device offers candidate but not writable-running")
	}
	for _, want := range []string{CapabilityWritableRunning, "candidate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// TestDatastoreFamily_IsRefusedWhenTheDeviceLacksCandidate covers the
// operations the shared pkg/datastore.Store port deliberately does not
// carry. They live on this concrete type precisely because they are not
// universal, and this is what "not universal" looks like on the one
// real device available.
func TestDatastoreFamily_IsRefusedWhenTheDeviceLacksCandidate(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	for name, call := range map[string]func() error{
		"Commit":         func() error { return s.Commit(context.Background()) },
		"DiscardChanges": func() error { return s.DiscardChanges(context.Background()) },
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatalf("%s() error = nil, want a refusal", name)
			}
			if !strings.Contains(err.Error(), CapabilityCandidate) {
				t.Errorf("%s() error = %q, want it to name the missing capability", name, err)
			}
		})
	}
}

func TestLockAndUnlock_TargetTheSessionDatastore(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	if err := s.Lock(context.Background()); err != nil {
		t.Fatalf("Lock() error = %v, want nil", err)
	}
	if req := d.lastRequest(t); !strings.Contains(req, "<lock><target><running/></target></lock>") {
		t.Errorf("request = %q, want a lock on running", req)
	}
	if err := s.Unlock(context.Background()); err != nil {
		t.Fatalf("Unlock() error = %v, want nil", err)
	}
	if req := d.lastRequest(t); !strings.Contains(req, "<unlock><target><running/></target></unlock>") {
		t.Errorf("request = %q, want an unlock on running", req)
	}
}

func TestSetConfig_ReportsANonOKReplyRatherThanAssumingSuccess(t *testing.T) {
	d := &fakeDevice{
		hello: realHelloPrelude,
		respond: func(req string) string {
			// Neither <ok/> nor <rpc-error>: a well-formed reply that
			// answers nothing. Treating it as success is the failure that
			// makes a failed runbook look like a successful one.
			return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` +
				messageIDOf(req) + `"><something-else/></rpc-reply>`
		},
	}
	s := openAgainst(t, d, Options{})

	payload := datastore.Payload{Encoding: datastore.EncodingXML, Bytes: []byte("<native/>")}
	err := s.SetConfig(context.Background(), datastore.Path{}, payload, datastore.OperationMerge)
	if err == nil {
		t.Fatal("SetConfig() error = nil, want a refusal of a reply that was neither ok nor an error")
	}
}

// TestEditConfig_HonorsAnExplicitErrorOption proves the automatic
// rollback-on-error choice is a default and not a lock-in: a caller
// that genuinely wants partial application can ask for it by name. What
// there is no way to get is that behavior by accident.
func TestEditConfig_HonorsAnExplicitErrorOption(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	err := s.EditConfig(context.Background(), EditConfigRequest{
		Config:      "<native/>",
		ErrorOption: ErrorOptionContinueOnError,
	})
	if err != nil {
		t.Fatalf("EditConfig() error = %v, want nil", err)
	}
	req := d.lastRequest(t)
	if !strings.Contains(req, "<error-option>continue-on-error</error-option>") {
		t.Errorf("request =\n%s\nwant the caller's own error-option", req)
	}
	if strings.Contains(req, "rollback-on-error") {
		t.Errorf("request =\n%s\nwant the automatic rollback-on-error NOT to override an explicit choice", req)
	}
}

func TestEditConfig_RefusesAnErrorOptionTheDeviceCannotHonor(t *testing.T) {
	hello := `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.1</capability>` +
		`<capability>urn:ietf:params:netconf:capability:writable-running:1.0</capability>` +
		`</capabilities><session-id>4</session-id></hello>`
	d := okDevice(hello)
	s := openAgainst(t, d, Options{})

	err := s.EditConfig(context.Background(), EditConfigRequest{
		Config:      "<native/>",
		ErrorOption: ErrorOptionRollbackOnError,
	})
	if err == nil {
		t.Fatal("EditConfig() error = nil, want a refusal: asking for an unadvertised capability is how a whole edit gets rejected by the device instead")
	}
	if !strings.Contains(err.Error(), CapabilityRollbackOnError) {
		t.Errorf("error = %q, want it to name the missing capability", err)
	}
}

func TestEditConfig_RefusesUnknownVocabulary(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	for _, tc := range []struct {
		name string
		req  EditConfigRequest
		want string
	}{
		{"an unknown default-operation", EditConfigRequest{Config: "<a/>", DefaultOperation: "delete"}, "RFC 6241 defines merge, replace and none"},
		{"an unknown error-option", EditConfigRequest{Config: "<a/>", ErrorOption: "retry"}, "RFC 6241 defines stop-on-error"},
		{"no configuration at all", EditConfigRequest{Config: "   "}, "needs configuration to apply"},
		{"a malformed payload", EditConfigRequest{Config: "<a>"}, "not usable"},
		{"a DOCTYPE", EditConfigRequest{Config: "<!DOCTYPE a><a/>"}, "DOCTYPE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.EditConfig(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("EditConfig() error = nil, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestEditConfig_RefusesAMalformedPayloadBeforeSendingAnything is the
// half of the previous test that matters most: the device must not have
// seen a byte of it. A device that has begun applying an edit when it
// discovers the payload is broken is the worst version of this failure.
func TestEditConfig_RefusesAMalformedPayloadBeforeSendingAnything(t *testing.T) {
	d := okDevice(realHelloPrelude)
	s := openAgainst(t, d, Options{})

	if err := s.EditConfig(context.Background(), EditConfigRequest{Config: "<native><hostname>"}); err == nil {
		t.Fatal("EditConfig() error = nil, want a refusal")
	}

	d.mu.Lock()
	got := len(d.requests)
	d.mu.Unlock()
	if got != 0 {
		t.Fatalf("the device received %d request(s) for a payload that never should have left this process", got)
	}
}

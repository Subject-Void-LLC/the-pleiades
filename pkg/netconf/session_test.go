package netconf

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// realHelloPrelude is the opening of the <hello> a real Cisco IOS XE
// 17.12 device sends, verbatim from
// pkg/remoteexec/live_subsystem_probe_test.go. The real message is
// 48,790 bytes; the remainder is several hundred more <capability>
// elements naming YANG modules, which are structurally identical to the
// ones kept here and are elided only to keep this file readable. Every
// capability this client reasons about is present, including the two
// facts that shaped the design: base:1.0 AND base:1.1 are both
// advertised, and :candidate is NOT.
const realHelloPrelude = `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
<capabilities>
<capability>urn:ietf:params:netconf:base:1.0</capability>
<capability>urn:ietf:params:netconf:base:1.1</capability>
<capability>urn:ietf:params:netconf:capability:writable-running:1.0</capability>
<capability>urn:ietf:params:netconf:capability:rollback-on-error:1.0</capability>
<capability>urn:ietf:params:netconf:capability:validate:1.0</capability>
<capability>urn:ietf:params:netconf:capability:validate:1.1</capability>
<capability>urn:ietf:params:netconf:capability:xpath:1.0</capability>
<capability>urn:ietf:params:netconf:capability:notification:1.0</capability>
<capability>urn:ietf:params:netconf:capability:interleave:1.0</capability>
<capability>urn:ietf:params:netconf:capability:with-defaults:1.0?basic-mode=explicit&amp;also-supported=report-all-tagged,report-all</capability>
<capability>urn:ietf:params:xml:ns:yang:smiv2:IP-MIB?module=IP-MIB&amp;revision=2006-02-02</capability>
</capabilities>
<session-id>1987</session-id></hello>`

// realUnknownElementError is the <rpc-error> a real device returned for
// an <rpc> naming an operation it does not implement. It carries NO
// <error-message>, which is the shape a client keyed on that field
// renders as an empty reason.
const realUnknownElementError = `<?xml version="1.0" encoding="UTF-8"?>
<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><rpc-error>
<error-type>protocol</error-type>
<error-tag>unknown-element</error-tag>
<error-severity>error</error-severity>
<error-path>
    /rpc
  </error-path><error-info><bad-element>pleiades-no-such-operation</bad-element>
</error-info>
</rpc-error>
</rpc-reply>`

// realCandidateRefusal is the same device refusing the candidate
// datastore. Unlike the error above it DOES carry an <error-message>,
// which is what makes the pair together prove the optional-field
// handling rather than either one alone.
const realCandidateRefusal = `<?xml version="1.0" encoding="UTF-8"?>
<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="%s"><rpc-error>
<error-type>protocol</error-type>
<error-tag>invalid-value</error-tag>
<error-severity>error</error-severity>
<error-message xml:lang="en">Unsupported capability :candidate</error-message><error-info><bad-element>candidate</bad-element>
</error-info>
</rpc-error>
</rpc-reply>`

// fakeDevice is the server side of a NETCONF session over net.Pipe.
//
// It drives its own side with this package's own reader and writer, and
// that is defensible only because framing_test.go pins BOTH of them
// independently against literal bytes captured from a real device: a
// matched pair of framing bugs would fail there before it could hide
// here. What this fixture exists to exercise is the session layer,
// which it does over a real full-duplex connection with no substituted
// behavior in the path.
type fakeDevice struct {
	hello   string
	respond func(request string) string

	mu       sync.Mutex
	requests []string
}

// start runs the device and returns the client's end of the connection.
func (d *fakeDevice) start(t *testing.T) io.ReadWriteCloser {
	t.Helper()
	clientConn, serverConn := net.Pipe()

	go func() {
		defer serverConn.Close()

		w := newWriter(serverConn)
		r := newReader(serverConn, 1<<20)

		if err := w.writeMessage([]byte(d.hello)); err != nil {
			return
		}
		// The client's hello. Both sides switch framing only after it,
		// exactly as RFC 6242 section 4.1 requires.
		if _, err := r.readMessage(); err != nil {
			return
		}
		if strings.Contains(d.hello, CapabilityBase11) {
			r.setFraming(FramingChunked)
			w.setFraming(FramingChunked)
		}

		for {
			raw, err := r.readMessage()
			if err != nil {
				return
			}
			d.mu.Lock()
			d.requests = append(d.requests, string(raw))
			d.mu.Unlock()

			if d.respond == nil {
				return
			}
			reply := d.respond(string(raw))
			if reply == "" {
				return
			}
			if err := w.writeMessage([]byte(reply)); err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() { clientConn.Close() })
	return clientConn
}

func (d *fakeDevice) lastRequest(t *testing.T) string {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.requests) == 0 {
		t.Fatal("the device received no requests")
	}
	return d.requests[len(d.requests)-1]
}

// messageIDOf pulls the message-id attribute out of a request so a
// scripted reply can echo it, which is what the client checks.
func messageIDOf(request string) string {
	const marker = `message-id="`
	i := strings.Index(request, marker)
	if i < 0 {
		return ""
	}
	rest := request[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func openAgainst(t *testing.T, d *fakeDevice, opts Options) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	s, err := Open(ctx, d.start(t), opts)
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	return s
}

func TestOpen_NegotiatesAgainstARealDeviceHello(t *testing.T) {
	d := &fakeDevice{hello: realHelloPrelude}
	s := openAgainst(t, d, Options{})

	if s.Framing() != FramingChunked {
		t.Errorf("Framing() = %v, want chunked: the device advertises base:1.1 and RFC 6241 section 8.1 selects the highest common version", s.Framing())
	}
	if s.SessionID() != "1987" {
		t.Errorf("SessionID() = %q, want %q", s.SessionID(), "1987")
	}
	if !s.HasCapability(CapabilityWritableRunning) {
		t.Error("HasCapability(:writable-running) = false, want true")
	}
	if s.HasCapability(CapabilityCandidate) {
		t.Error("HasCapability(:candidate) = true, want false: this device does not offer a candidate datastore, and treating it as if it did is exactly the assumption this fixture exists to prevent")
	}
	// The device advertises with-defaults WITH query parameters
	// attached. A caller must not have to reproduce that exact parameter
	// string to get a true answer.
	if !s.HasCapability("urn:ietf:params:netconf:capability:with-defaults:1.0") {
		t.Error("HasCapability(:with-defaults) = false, want true: query parameters are part of the advertisement, not of the capability's identity")
	}
}

func TestOpen_RefusesAHelloWithNoBaseCapability(t *testing.T) {
	d := &fakeDevice{hello: `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		`<capability>urn:ietf:params:netconf:capability:candidate:1.0</capability>` +
		`</capabilities><session-id>1</session-id></hello>`}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Open(ctx, d.start(t), Options{})
	if err == nil {
		t.Fatal("Open() error = nil, want a refusal: with no base capability there is no agreed framing")
	}
	if !strings.Contains(err.Error(), "refusing rather than guessing") {
		t.Errorf("Open() error = %q, want it to say it refuses rather than guessing at a framing", err)
	}
}

func TestOpen_FallsBackToEndOfMessageWhenOnly10IsOffered(t *testing.T) {
	d := &fakeDevice{hello: `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		`<capability>urn:ietf:params:netconf:base:1.0</capability>` +
		`</capabilities><session-id>7</session-id></hello>`}
	s := openAgainst(t, d, Options{})

	if s.Framing() != FramingEndOfMessage {
		t.Errorf("Framing() = %v, want end-of-message", s.Framing())
	}
}

// TestOpen_RefusesAnUnsupportedTargetImmediately is the difference
// between a named failure at connect time and a confusing one at the
// first write. The device in this fixture is the real one, which does
// not offer :candidate.
func TestOpen_RefusesAnUnsupportedTargetImmediately(t *testing.T) {
	d := &fakeDevice{hello: realHelloPrelude}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Open(ctx, d.start(t), Options{Target: Candidate})
	if err == nil {
		t.Fatal("Open(Target: candidate) error = nil, want a refusal")
	}
	for _, want := range []string{"candidate", CapabilityCandidate, "running"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Open() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestOpen_BoundsTheHelloBeforeParsingIt(t *testing.T) {
	// A hello that never ends. Nothing has been negotiated at this
	// point, so the hello bound is the only thing standing between this
	// and unbounded memory growth.
	flood := `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities>` +
		strings.Repeat(`<capability>urn:ietf:params:netconf:base:1.0</capability>`, 10000)

	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close()
		_, _ = io.WriteString(serverConn, flood)
	}()
	t.Cleanup(func() { clientConn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Open(ctx, clientConn, Options{MaxHelloBytes: 1024})
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("Open() error = %v, want it to wrap ErrMessageTooLarge", err)
	}
}

func TestGetConfig_ReturnsTheDataSubtreeVerbatim(t *testing.T) {
	const data = `<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><hostname>Cat8kv</hostname></native>`

	d := &fakeDevice{
		hello: realHelloPrelude,
		respond: func(req string) string {
			return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` +
				messageIDOf(req) + `"><data>` + data + `</data></rpc-reply>`
		},
	}
	s := openAgainst(t, d, Options{})

	got, err := s.GetConfig(context.Background(), datastore.Path{})
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want nil", err)
	}
	if got.Encoding != datastore.EncodingXML {
		t.Errorf("Encoding = %v, want xml", got.Encoding)
	}
	if string(got.Bytes) != data {
		t.Errorf("Bytes = %q, want %q", got.Bytes, data)
	}
	if req := d.lastRequest(t); !strings.Contains(req, "<source><running/></source>") {
		t.Errorf("request = %q, want it to read from the running datastore", req)
	}
}

func TestGetConfig_BuildsASubtreeFilterFromThePath(t *testing.T) {
	d := &fakeDevice{
		hello: realHelloPrelude,
		respond: func(req string) string {
			return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="` +
				messageIDOf(req) + `"><data/></rpc-reply>`
		},
	}
	s := openAgainst(t, d, Options{})

	p := datastore.Path{Elem: []datastore.PathElem{
		{Name: "native", Namespace: "http://cisco.com/ns/yang/Cisco-IOS-XE-native"},
		{Name: "interface"},
		{Name: "Loopback", Keys: map[string]string{"name": "8990"}},
	}}
	if _, err := s.GetConfig(context.Background(), p); err != nil {
		t.Fatalf("GetConfig() error = %v, want nil", err)
	}

	req := d.lastRequest(t)
	want := `<filter type="subtree"><native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><interface><Loopback><name>8990</name></Loopback></interface></native></filter>`
	if !strings.Contains(req, want) {
		t.Errorf("request =\n%s\nwant it to contain\n%s", req, want)
	}
}

func TestGetConfig_ReportsARealDeviceErrorWithNoErrorMessage(t *testing.T) {
	d := &fakeDevice{
		hello: realHelloPrelude,
		respond: func(req string) string {
			return strings.Replace(realUnknownElementError, "%s", messageIDOf(req), 1)
		},
	}
	s := openAgainst(t, d, Options{})

	_, err := s.GetConfig(context.Background(), datastore.Path{})
	if err == nil {
		t.Fatal("GetConfig() error = nil, want the device's rpc-error")
	}

	// This device sends NO <error-message> for this condition, so
	// everything useful in the rendering has to come from the other
	// fields. An error that rendered as "netconf: rpc-error: " would
	// technically be non-nil and practically useless.
	for _, want := range []string{"unknown-element", "type=protocol", "path=/rpc", "bad-element=pleiades-no-such-operation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("error = %q, want no embedded newline: the device pretty-prints <error-path> and an untrimmed value breaks a log line", err)
	}

	var rpcErr RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("errors.As(err, &RPCError{}) = false, want true so a caller can branch on the tag")
	}
	if rpcErr.Tag != "unknown-element" {
		t.Errorf("Tag = %q, want %q", rpcErr.Tag, "unknown-element")
	}
	if rpcErr.Message != "" {
		t.Errorf("Message = %q, want empty: this device sends none for this condition", rpcErr.Message)
	}
}

func TestGetConfig_ReportsARealDeviceErrorWithAnErrorMessage(t *testing.T) {
	d := &fakeDevice{
		hello: realHelloPrelude,
		respond: func(req string) string {
			return strings.Replace(realCandidateRefusal, "%s", messageIDOf(req), 1)
		},
	}
	s := openAgainst(t, d, Options{})

	_, err := s.GetConfig(context.Background(), datastore.Path{})
	if err == nil {
		t.Fatal("GetConfig() error = nil, want the device's rpc-error")
	}
	for _, want := range []string{"invalid-value", "Unsupported capability :candidate", "bad-element=candidate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

// TestDo_RefusesAReplyForADifferentMessageID pins the correlation rule.
// Accepting a mismatched reply would attribute one RPC's result to
// another, and no amount of later parsing recovers from that.
func TestDo_RefusesAReplyForADifferentMessageID(t *testing.T) {
	d := &fakeDevice{
		hello: realHelloPrelude,
		respond: func(req string) string {
			return `<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="9999"><data/></rpc-reply>`
		},
	}
	s := openAgainst(t, d, Options{})

	_, err := s.GetConfig(context.Background(), datastore.Path{})
	if err == nil {
		t.Fatal("GetConfig() error = nil, want a refusal of the mismatched reply")
	}
	if !strings.Contains(err.Error(), "no longer synchronized") {
		t.Errorf("error = %q, want it to say the session is no longer synchronized", err)
	}
}

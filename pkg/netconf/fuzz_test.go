package netconf

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
)

// FuzzChunkedReader feeds arbitrary bytes to RFC 6242's chunked framing
// decoder, which is this package's most exposed parser: it runs on
// every byte a device sends, before any XML parser sees them, and its
// chunk-size header is a length prefix an attacker controls.
//
// The assertion is "never panics and never returns a message alongside
// an error", following this repository's established fuzzing convention
// (pkg/remoteexec/fuzz_test.go, internal/engine/dag_fuzz_test.go). A
// clean error is a correct outcome for almost every input here; what
// must never happen is a partial or fabricated message being handed
// upward as if it were complete, because the layer above would parse it
// as configuration.
func FuzzChunkedReader(f *testing.F) {
	f.Add([]byte(realChunkedReply))
	f.Add([]byte("\n#4\nabcd\n##\n"))
	f.Add([]byte("\n##\n"))
	f.Add([]byte("\n#0\n\n##\n"))
	f.Add([]byte("\n#04\nabcd\n##\n"))
	f.Add([]byte("\n#99999999999999999999\n"))
	f.Add([]byte("\n#4294967295\n"))
	f.Add([]byte("\n#5\nab"))
	f.Add([]byte("\n#1\n\xff\n##\n"))
	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte("\n#"))
	f.Add([]byte{0x00, 0xff, '\n', '#', '1', '\n', 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := newReader(bytes.NewReader(data), 1<<16)
		r.setFraming(FramingChunked)

		msg, err := r.readMessage()
		if err != nil && msg != nil {
			t.Fatalf("readMessage() returned %d bytes alongside error %v: a partial message must never be usable", len(msg), err)
		}
		if err == nil && len(msg) > 1<<16 {
			t.Fatalf("readMessage() returned %d bytes, past the %d-byte bound it was given", len(msg), 1<<16)
		}
	})
}

// FuzzEndOfMessageReader covers the other framing. It is the one used
// for the <hello> exchange, so it runs before anything at all has been
// negotiated with the peer.
func FuzzEndOfMessageReader(f *testing.F) {
	f.Add([]byte("<hello/>]]>]]>"))
	f.Add([]byte("]]>]]>"))
	f.Add([]byte("]]>]]"))
	f.Add([]byte("]]>]]>]]>]]>"))
	f.Add([]byte("no delimiter at all"))
	f.Add([]byte(""))
	f.Add([]byte{0xff, 0xfe, ']', ']', '>', ']', ']', '>'})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := newReader(bytes.NewReader(data), 1<<16)
		msg, err := r.readMessage()
		if err != nil && msg != nil {
			t.Fatalf("readMessage() returned %d bytes alongside error %v", len(msg), err)
		}
	})
}

// FuzzOpenAgainstAHostileHello drives the whole negotiation, not just
// the framing, against arbitrary bytes presented as a server's hello.
// This is the exchange with the weakest position in the protocol:
// nothing has been agreed, the peer is unauthenticated as far as this
// package is concerned, and a framing decision is about to be made on
// what it says.
//
// Open must always either return a Session or a clean error, never
// panic and never hang. A hang would be the interesting failure, so the
// context carries a deadline and the fuzz body fails if it is what
// stopped the call.
func FuzzOpenAgainstAHostileHello(f *testing.F) {
	f.Add(realHelloPrelude)
	f.Add(`<hello><capabilities></capabilities></hello>`)
	f.Add(`<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><capabilities><capability>urn:ietf:params:netconf:base:1.0</capability><capability>urn:ietf:params:netconf:base:1.1</capability></capabilities><session-id>1</session-id></hello>`)
	f.Add(`<hello><capabilities><capability></capability></capabilities></hello>`)
	f.Add(`<hello><capabilities><capability>?</capability></capabilities></hello>`)
	f.Add(`<hello><session-id>not-a-number</session-id></hello>`)
	f.Add(`<not-a-hello/>`)
	f.Add(``)
	f.Add(`<hello`)
	f.Add("\x00\xff")

	f.Fuzz(func(t *testing.T, hello string) {
		// A fixed, already-closed pipe end would race the writer; a
		// bytes.Reader with a no-op Close gives the decoder the same
		// input deterministically and with no goroutine at all.
		rwc := nopCloser{strings.NewReader(hello + endOfMessageDelimiter)}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		s, err := Open(ctx, rwc, Options{MaxHelloBytes: 1 << 16})
		if ctx.Err() != nil {
			t.Fatalf("Open() did not return before its deadline for hello %q", hello)
		}
		if err == nil && s == nil {
			t.Fatal("Open() returned neither a Session nor an error")
		}
		if err == nil {
			// A Session that opened must have made a real framing
			// decision, never been left at some zero value by accident.
			if s.Framing() != FramingChunked && s.Framing() != FramingEndOfMessage {
				t.Fatalf("Open() succeeded with framing %v, which is neither of the two RFC 6242 framings", s.Framing())
			}
		}
	})
}

// FuzzSetConfigPayload feeds arbitrary bytes as the configuration
// payload a runbook supplies. It asserts the payload check never panics
// and never lets an unparseable document reach the wire, which matters
// because a device that has begun applying an edit when it discovers
// the problem is the worst version of this failure.
func FuzzSetConfigPayload(f *testing.F) {
	f.Add([]byte(loopbackXML))
	f.Add([]byte(`<a/>`))
	f.Add([]byte(`<a>`))
	f.Add([]byte(`<!DOCTYPE a><a/>`))
	f.Add([]byte(`<a>&undefined;</a>`))
	f.Add([]byte(``))
	f.Add([]byte(strings.Repeat("<a>", 2000)))
	f.Add([]byte{0xff, 0xfe, '<', 'a', '/', '>'})

	f.Fuzz(func(t *testing.T, payload []byte) {
		err := checkWellFormed(payload, defaultMaxDepth)
		if err != nil {
			return
		}
		// Anything checkWellFormed accepted must survive a second full
		// parse: a payload that passed the gate and then failed to
		// tokenize would mean the gate and the wire disagree.
		if depthErr := checkDepth(payload, defaultMaxDepth); depthErr != nil {
			t.Fatalf("checkWellFormed accepted a payload checkDepth then rejected: %v", depthErr)
		}
	})
}

// FuzzPathConstruction feeds arbitrary element and key names through
// the XML construction path. Every input either produces a document
// whose element names came only from validated names, or is refused;
// producing markup from an unvalidated name is the failure this
// package's whole name-refusal mechanism exists to prevent.
func FuzzPathConstruction(f *testing.F) {
	f.Add("native", "name", "8990")
	f.Add("native><evil", "name", "8990")
	f.Add("native", "name><evil", "8990")
	f.Add("native", "name", `8990"><evil/>`)
	f.Add("", "", "")
	f.Add("a", "b", "<>&\"'")
	f.Add("\x00", "\x00", "\x00")

	f.Fuzz(func(t *testing.T, elem, key, value string) {
		p := datastore.Path{Elem: []datastore.PathElem{
			{Name: elem, Keys: map[string]string{key: value}},
		}}
		got, err := subtreeFilter(p)
		if err != nil {
			return
		}
		// It was accepted, so both names passed validation and the value
		// was escaped. The document must therefore tokenize cleanly: an
		// accepted input that produces malformed XML would mean a value
		// escaped its way out of character data.
		if err := checkWellFormed([]byte(got), defaultMaxDepth); err != nil {
			t.Fatalf("subtreeFilter(elem=%q key=%q value=%q) produced unparseable XML %q: %v", elem, key, value, got, err)
		}
	})
}

// nopCloser adapts a reader into the io.ReadWriteCloser Open takes.
// Writes are discarded: the client hello has nowhere to go in a fuzz
// iteration and its content is not what is under test here.
type nopCloser struct{ io.Reader }

func (nopCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopCloser) Close() error                { return nil }

package netconf

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// realChunkedReply is the exact byte sequence a real Cisco IOS XE 17.12
// device sent in answer to a subtree-filtered get-config, captured by
// pkg/remoteexec/live_subsystem_probe_test.go. Every framing assertion
// in this file is anchored to it rather than to a hand-authored example
// of what RFC 6242 section 4.2 is understood to say.
const realChunkedReply = "\n#237\n" +
	`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="101"><data><native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><hostname>Cat8kv</hostname></native></data></rpc-reply>` +
	"\n##\n"

// realChunkedPayload is what realChunkedReply decodes to. Its length is
// asserted against the chunk header the device itself wrote, so a change
// to either constant that broke their relationship fails loudly.
const realChunkedPayload = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<rpc-reply xmlns="urn:ietf:params:xml:ns:netconf:base:1.0" message-id="101"><data><native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native"><hostname>Cat8kv</hostname></native></data></rpc-reply>`

func TestFraming_DecodesARealDeviceChunkedReply(t *testing.T) {
	if len(realChunkedPayload) != 237 {
		t.Fatalf("the captured payload is %d bytes, but the device framed it as 237: one of the two constants has drifted", len(realChunkedPayload))
	}

	r := newReader(strings.NewReader(realChunkedReply), 1<<20)
	r.setFraming(FramingChunked)

	got, err := r.readMessage()
	if err != nil {
		t.Fatalf("readMessage() error = %v, want nil", err)
	}
	if string(got) != realChunkedPayload {
		t.Errorf("readMessage() = %q, want %q", got, realChunkedPayload)
	}
}

// TestFraming_EncodesWhatARealDeviceWouldAccept closes the loop the
// other way: the writer must produce the exact bytes the device
// produced for the same payload. Testing the reader against the writer
// alone would let a matched pair of bugs pass, which is why both are
// pinned to one captured literal instead of to each other.
func TestFraming_EncodesWhatARealDeviceWouldAccept(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf)
	w.setFraming(FramingChunked)

	if err := w.writeMessage([]byte(realChunkedPayload)); err != nil {
		t.Fatalf("writeMessage() error = %v, want nil", err)
	}
	if buf.String() != realChunkedReply {
		t.Errorf("writeMessage() produced\n%q\nwant\n%q", buf.String(), realChunkedReply)
	}
}

func TestFraming_EndOfMessageRoundTrips(t *testing.T) {
	const payload = `<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"/>`

	var buf bytes.Buffer
	w := newWriter(&buf)
	if err := w.writeMessage([]byte(payload)); err != nil {
		t.Fatalf("writeMessage() error = %v, want nil", err)
	}
	if !strings.HasSuffix(buf.String(), endOfMessageDelimiter) {
		t.Fatalf("writeMessage() produced %q, want it to end with %q", buf.String(), endOfMessageDelimiter)
	}

	r := newReader(&buf, 1<<20)
	got, err := r.readMessage()
	if err != nil {
		t.Fatalf("readMessage() error = %v, want nil", err)
	}
	if string(got) != payload {
		t.Errorf("readMessage() = %q, want %q", got, payload)
	}
}

// TestFraming_ReadsTwoMessagesArrivingInOneWrite is the framing
// equivalent of Shell's carry-over case: a peer is free to put two whole
// messages in a single write, and a reader that discarded whatever
// followed the first delimiter would silently lose the second.
func TestFraming_ReadsTwoMessagesArrivingInOneWrite(t *testing.T) {
	both := "<a/>" + endOfMessageDelimiter + "<b/>" + endOfMessageDelimiter
	r := newReader(strings.NewReader(both), 1<<20)

	for _, want := range []string{"<a/>", "<b/>"} {
		got, err := r.readMessage()
		if err != nil {
			t.Fatalf("readMessage() error = %v, want nil", err)
		}
		if string(got) != want {
			t.Errorf("readMessage() = %q, want %q", got, want)
		}
	}
}

func TestFraming_ChunkedRejectsMalformedHeaders(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no leading newline", "#4\nabcd\n##\n", "expected a newline"},
		{"no hash", "\nX4\nabcd\n##\n", "expected '#'"},
		{"non-digit size", "\n#x\nabcd\n##\n", "expected a decimal chunk size"},
		{"zero size", "\n#0\n\n##\n", "may not begin with '0'"},
		{"leading zero", "\n#04\nabcd\n##\n", "may not begin with '0'"},
		{"garbage in size", "\n#4x\nabcd\n##\n", "expected a digit or a newline"},
		{"size beyond the RFC maximum", "\n#99999999999\nabcd\n##\n", "exceeds RFC 6242's maximum"},
		{"malformed end marker", "\n#4\nabcd\n#X\n", "expected a decimal chunk size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReader(strings.NewReader(tc.input), 1<<20)
			r.setFraming(FramingChunked)

			_, err := r.readMessage()
			if err == nil {
				t.Fatalf("readMessage() error = nil, want one containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("readMessage() error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestFraming_TruncatedChunkIsNeverAWholeMessage is the property the
// chaos test exercises against a real severed connection, asserted here
// deterministically: a chunk header promising more bytes than arrive
// must be an error, never a short message handed upward as if it were
// complete. Silently accepting it would turn a severed connection into
// a truncated configuration that parsed.
func TestFraming_TruncatedChunkIsNeverAWholeMessage(t *testing.T) {
	r := newReader(strings.NewReader("\n#100\nonly-a-few-bytes"), 1<<20)
	r.setFraming(FramingChunked)

	got, err := r.readMessage()
	if err == nil {
		t.Fatalf("readMessage() = %q with a nil error, want a failure: the chunk promised 100 bytes and 17 arrived", got)
	}
	if got != nil {
		t.Errorf("readMessage() returned %q alongside its error, want nil: a partial message must not be usable", got)
	}
}

func TestFraming_BoundsAreEnforcedInBothFramings(t *testing.T) {
	t.Run("end-of-message", func(t *testing.T) {
		// No delimiter anywhere, so the only thing that can stop this is
		// the bound.
		r := newReader(strings.NewReader(strings.Repeat("x", 4096)), 64)
		_, err := r.readMessage()
		if !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("readMessage() error = %v, want it to wrap ErrMessageTooLarge", err)
		}
	})

	t.Run("chunked, declared size alone", func(t *testing.T) {
		// The bound must fire on the DECLARED size, before the bytes are
		// read and before a slice is allocated on the strength of it.
		r := newReader(strings.NewReader("\n#1000000\n"), 64)
		r.setFraming(FramingChunked)
		_, err := r.readMessage()
		if !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("readMessage() error = %v, want it to wrap ErrMessageTooLarge", err)
		}
	})

	t.Run("chunked, accumulated across chunks", func(t *testing.T) {
		// Each chunk is under the bound; together they are not. A reader
		// that only checked per chunk would let this through.
		chunk := "\n#40\n" + strings.Repeat("y", 40)
		r := newReader(strings.NewReader(chunk+chunk+chunk+"\n##\n"), 64)
		r.setFraming(FramingChunked)
		_, err := r.readMessage()
		if !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("readMessage() error = %v, want it to wrap ErrMessageTooLarge", err)
		}
	})
}

func TestFraming_CleanEndOfStreamIsEOF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		framing Framing
	}{
		{"end-of-message", FramingEndOfMessage},
		{"chunked", FramingChunked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReader(strings.NewReader(""), 1<<20)
			r.setFraming(tc.framing)
			if _, err := r.readMessage(); !errors.Is(err, io.EOF) {
				t.Fatalf("readMessage() on a closed stream = %v, want io.EOF: a peer closing between messages is an ordinary end of session", err)
			}
		})
	}
}

func TestFraming_MultiChunkMessageIsReassembledInOrder(t *testing.T) {
	input := "\n#5\nhello\n#1\n \n#5\nworld\n##\n"
	r := newReader(strings.NewReader(input), 1<<20)
	r.setFraming(FramingChunked)

	got, err := r.readMessage()
	if err != nil {
		t.Fatalf("readMessage() error = %v, want nil", err)
	}
	if string(got) != "hello world" {
		t.Errorf("readMessage() = %q, want %q", got, "hello world")
	}
}

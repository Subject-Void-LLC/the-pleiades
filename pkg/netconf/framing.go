package netconf

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// Framing is one of RFC 6242's two message framings. Which one a
// session uses is decided once, by capability negotiation, and never
// guessed: see negotiateFraming.
type Framing uint8

const (
	// FramingEndOfMessage is RFC 6242 section 4.3's end-of-message
	// framing: messages are separated by the six-byte sequence
	// "]]>]]>". It is what base:1.0 uses, and it is what BOTH sides use
	// for the <hello> exchange regardless of what they go on to
	// negotiate, because the negotiation has not happened yet when the
	// hellos are sent.
	//
	// It is also structurally unsound and the RFC says so: the
	// delimiter is not an illegal byte sequence inside XML character
	// data, so a configuration value that happens to contain it ends
	// the message early. That is precisely why base:1.1 exists, and why
	// this package negotiates up to chunked framing whenever the server
	// offers it.
	FramingEndOfMessage Framing = iota

	// FramingChunked is RFC 6242 section 4.2's chunked framing, used by
	// base:1.1. Each chunk is "\n#" followed by a decimal length, a
	// newline, and exactly that many bytes; the message ends with
	// "\n##\n". Verified on the wire against a real Cisco IOS XE
	// device, which frames a 237-byte reply as
	// "\n#237\n<...237 bytes...>\n##\n".
	FramingChunked
)

// String renders a Framing for error messages.
func (f Framing) String() string {
	switch f {
	case FramingEndOfMessage:
		return "end-of-message (base:1.0)"
	case FramingChunked:
		return "chunked (base:1.1)"
	default:
		return fmt.Sprintf("Framing(%d)", uint8(f))
	}
}

// endOfMessageDelimiter is RFC 6242 section 4.3's separator.
const endOfMessageDelimiter = "]]>]]>"

// maxChunkSize is RFC 6242 section 4.2's own upper bound on a single
// chunk's declared length (chunk-size is 1 to 4294967295). A larger
// declared length is a protocol violation, refused before any
// allocation is made on the strength of it, which is the whole point of
// checking it here rather than letting the message bound catch it after
// the fact.
const maxChunkSize = 4294967295

// ErrMessageTooLarge is returned when a peer's message exceeds the
// session's byte bound. It is a distinct, comparable error because a
// caller may reasonably want to tell "this device sent more
// configuration than we are willing to hold" apart from "this device
// speaks the protocol incorrectly": the first is a sizing decision, the
// second is a bug or an attack.
var ErrMessageTooLarge = errors.New("netconf: message exceeds the configured byte bound")

// reader reads whole framed NETCONF messages off a byte stream.
//
// It returns each message as a []byte rather than as a bounded
// io.Reader, and that is a deliberate, bounded choice rather than an
// oversight. Every consumer above this either parses the message into a
// Go value or captures a subtree verbatim as bytes to hand back as a
// datastore.Payload, so the message is materialized regardless; handing
// out a streaming reader while the layer above buffers it anyway would
// be the appearance of streaming rather than the fact of it. What makes
// that safe is that the bound is mandatory, not that the buffer is
// small: see maxMessageBytes on Options.
type reader struct {
	br       *bufio.Reader
	framing  Framing
	maxBytes int
}

func newReader(r io.Reader, maxBytes int) *reader {
	return &reader{
		// The buffer size is a read-efficiency choice only; a message
		// larger than it is read across several fills, and maxBytes,
		// not this, is what bounds a message.
		br:       bufio.NewReaderSize(r, 32*1024),
		framing:  FramingEndOfMessage,
		maxBytes: maxBytes,
	}
}

// setFraming switches the framing used for subsequent reads. Called
// exactly once per session, immediately after the hello exchange.
func (r *reader) setFraming(f Framing) { r.framing = f }

// readMessage reads and returns one whole message's payload, with its
// framing removed.
func (r *reader) readMessage() ([]byte, error) {
	if r.framing == FramingChunked {
		return r.readChunked()
	}
	return r.readEndOfMessage()
}

// readEndOfMessage reads until the "]]>]]>" separator.
func (r *reader) readEndOfMessage() ([]byte, error) {
	var buf []byte
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			if err == io.EOF && len(buf) == 0 {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("netconf: reading an end-of-message-framed message: %w", err)
		}
		buf = append(buf, b)

		if len(buf) >= len(endOfMessageDelimiter) &&
			string(buf[len(buf)-len(endOfMessageDelimiter):]) == endOfMessageDelimiter {
			return buf[:len(buf)-len(endOfMessageDelimiter)], nil
		}
		if len(buf) > r.maxBytes {
			return nil, fmt.Errorf("netconf: read %d bytes with no %q separator: %w",
				len(buf), endOfMessageDelimiter, ErrMessageTooLarge)
		}
	}
}

// readChunked reads one chunked-framed message: a run of
// "\n#<size>\n<size bytes>" chunks terminated by "\n##\n".
func (r *reader) readChunked() ([]byte, error) {
	var buf []byte
	first := true
	for {
		size, end, err := r.readChunkHeader(first)
		if err != nil {
			return nil, err
		}
		first = false
		if end {
			return buf, nil
		}
		if len(buf)+size > r.maxBytes {
			return nil, fmt.Errorf("netconf: chunked message reached %d bytes: %w",
				len(buf)+size, ErrMessageTooLarge)
		}

		chunk := make([]byte, size)
		if _, err := io.ReadFull(r.br, chunk); err != nil {
			return nil, fmt.Errorf("netconf: reading a %d-byte chunk: %w", size, err)
		}
		buf = append(buf, chunk...)
	}
}

// readChunkHeader reads one "\n#<size>\n" chunk header, or the "\n##\n"
// end-of-chunks marker, reporting which it found.
//
// first distinguishes the very first header of a message, where an
// immediate EOF means the peer closed cleanly between messages rather
// than truncating one, which is an ordinary end of session and not an
// error.
func (r *reader) readChunkHeader(first bool) (size int, end bool, err error) {
	b, err := r.br.ReadByte()
	if err != nil {
		if err == io.EOF && first {
			return 0, false, io.EOF
		}
		return 0, false, fmt.Errorf("netconf: reading a chunk header: %w", err)
	}
	if b != '\n' {
		return 0, false, fmt.Errorf("netconf: malformed chunk header: expected a newline, got %q", b)
	}
	if b, err = r.br.ReadByte(); err != nil {
		return 0, false, fmt.Errorf("netconf: reading a chunk header: %w", err)
	}
	if b != '#' {
		return 0, false, fmt.Errorf("netconf: malformed chunk header: expected '#', got %q", b)
	}

	b, err = r.br.ReadByte()
	if err != nil {
		return 0, false, fmt.Errorf("netconf: reading a chunk header: %w", err)
	}
	if b == '#' {
		// End-of-chunks marker: the trailing newline of "\n##\n".
		if b, err = r.br.ReadByte(); err != nil {
			return 0, false, fmt.Errorf("netconf: reading the end-of-chunks marker: %w", err)
		}
		if b != '\n' {
			return 0, false, fmt.Errorf("netconf: malformed end-of-chunks marker: expected a newline, got %q", b)
		}
		return 0, true, nil
	}

	// A chunk size. Accumulated digit by digit against maxChunkSize as
	// it goes, rather than collected into a string and parsed at the
	// end: a peer that sends a million digits would otherwise get to
	// allocate a million-byte string before anything objected, which is
	// the allocation this bound exists to prevent.
	if b < '0' || b > '9' {
		return 0, false, fmt.Errorf("netconf: malformed chunk header: expected a decimal chunk size, got %q", b)
	}
	// RFC 6242's chunk-size is 1*10DIGIT with a minimum value of 1, so a
	// chunk declaring zero bytes, with or without leading zeros, is a
	// protocol violation. Refusing it matters beyond pedantry: a stream
	// of zero-length chunks is a message that never ends and never
	// grows, so nothing the byte bound checks would ever fire.
	if b == '0' {
		return 0, false, errors.New("netconf: malformed chunk header: a chunk size may not begin with '0' (RFC 6242 requires a size of at least 1)")
	}
	value := int64(b - '0')
	for {
		b, err = r.br.ReadByte()
		if err != nil {
			return 0, false, fmt.Errorf("netconf: reading a chunk size: %w", err)
		}
		if b == '\n' {
			break
		}
		if b < '0' || b > '9' {
			return 0, false, fmt.Errorf("netconf: malformed chunk size: expected a digit or a newline, got %q", b)
		}
		value = value*10 + int64(b-'0')
		if value > maxChunkSize {
			return 0, false, fmt.Errorf("netconf: chunk size exceeds RFC 6242's maximum of %d", maxChunkSize)
		}
	}
	if value > int64(r.maxBytes) {
		return 0, false, fmt.Errorf("netconf: chunk declares %d bytes: %w", value, ErrMessageTooLarge)
	}
	return int(value), false, nil
}

// writer frames outgoing NETCONF messages.
type writer struct {
	w       io.Writer
	framing Framing
}

func newWriter(w io.Writer) *writer {
	return &writer{w: w, framing: FramingEndOfMessage}
}

func (w *writer) setFraming(f Framing) { w.framing = f }

// writeMessage frames and writes one message.
//
// It writes the frame in as few Write calls as the framing allows, by
// building the framed bytes first. Over an SSH channel each Write
// becomes a channel data message, and a chunked frame split into four
// of them is four round trips' worth of window accounting for one
// logical message.
func (w *writer) writeMessage(payload []byte) error {
	var framed []byte
	if w.framing == FramingChunked {
		header := fmt.Sprintf("\n#%d\n", len(payload))
		framed = make([]byte, 0, len(header)+len(payload)+4)
		framed = append(framed, header...)
		framed = append(framed, payload...)
		framed = append(framed, "\n##\n"...)
	} else {
		framed = make([]byte, 0, len(payload)+len(endOfMessageDelimiter))
		framed = append(framed, payload...)
		framed = append(framed, endOfMessageDelimiter...)
	}
	if _, err := w.w.Write(framed); err != nil {
		return fmt.Errorf("netconf: writing a %s message: %w", w.framing, err)
	}
	return nil
}

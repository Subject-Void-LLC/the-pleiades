// Package pcapsum: HTTP/1.x in the clear (RFC 9112), read message by
// message only as far as its header lines, so that a header carrying a
// credential (Authorization: Basic, Cookie) is reported by its name. A
// body is skipped by its Content-Length or its chunks, never searched.
package pcapsum

import (
	"bytes"
	"strconv"
	"strings"
)

// maxH1Line is the longest HTTP/1.x line read; a longer one stops the
// direction's headers being searched.
const maxH1Line = 8 << 10

// The HTTP/1.x parser's states.
const (
	h1Start     = iota // a request or status line
	h1Header           // a header line, or the empty line ending them
	h1Body             // a body of known length
	h1ChunkSize        // a chunk's size line
	h1ChunkData        // a chunk's data and the line end after it
	h1Trailer          // a trailer line after the last chunk
	h1Rest             // a body read until the connection closes
	h1Lost             // the framing is lost: nothing more is searched
)

// h1Parser reads one direction of HTTP/1.x. Every byte is cleartext; the
// parsing is for finding credentials in header lines.
type h1Parser struct {
	state int
	line  []byte
	// left is the body or chunk bytes still to skip.
	left uint64
	// Of the message being read: whether it is a response and its status,
	// its Content-Length (-1 when it has none), and whether it is chunked.
	response bool
	status   int
	length   int64
	chunked  bool
	sink     headerSink
}

// newH1Parser is a parser for one direction, telling sink what its headers
// held.
func newH1Parser(sink headerSink) *h1Parser {
	return &h1Parser{sink: sink}
}

// feed implements parser.
func (h *h1Parser) feed(p []byte, _ bool) kind {
	for len(p) > 0 {
		switch h.state {
		case h1Body, h1ChunkData:
			n := min(uint64(len(p)), h.left)
			h.left -= n
			p = p[n:]
			if h.left == 0 {
				h.bodyDone()
			}
		case h1Rest, h1Lost:
			return kindCleartext
		default:
			i := bytes.IndexByte(p, '\n')
			n := len(p)
			if i >= 0 {
				n = i + 1
			}
			if len(h.line)+n > maxH1Line {
				h.lose()
				return kindCleartext
			}
			h.line = append(h.line, p[:n]...)
			p = p[n:]
			if i >= 0 {
				line := strings.TrimRight(string(h.line), "\r\n")
				h.line = h.line[:0]
				h.readLine(line)
			}
		}
	}
	return kindCleartext
}

// lose stops reading: the headers after here are unsearched.
func (h *h1Parser) lose() {
	if h.state != h1Lost {
		h.state = h1Lost
		h.sink.unsearched()
	}
}

// readLine reads one whole line in the state it arrived in.
func (h *h1Parser) readLine(line string) {
	switch h.state {
	case h1Start:
		h.startLine(line)
	case h1Header, h1Trailer:
		if line == "" {
			h.headersDone()
			return
		}
		h.header(line)
	case h1ChunkSize:
		size, _, _ := strings.Cut(line, ";")
		n, err := strconv.ParseUint(strings.TrimSpace(size), 16, 63)
		switch {
		case err != nil:
			h.lose()
		case n == 0:
			h.state = h1Trailer
		default:
			// The chunk's data and the CRLF after it.
			h.state, h.left = h1ChunkData, n+2
		}
	}
}

// startLine reads a request line (METHOD target HTTP/1.x) or a status line
// (HTTP/1.x code reason). An empty line between messages is allowed.
func (h *h1Parser) startLine(line string) {
	if line == "" {
		return
	}
	h.response, h.status, h.length, h.chunked = false, 0, -1, false
	parts := strings.SplitN(line, " ", 3)
	switch {
	case len(parts) >= 2 && (parts[0] == "HTTP/1.0" || parts[0] == "HTTP/1.1"):
		code, err := strconv.Atoi(parts[1])
		if err != nil || code < 100 || code > 999 {
			h.lose()
			return
		}
		h.response, h.status = true, code
	case len(parts) == 3 && (parts[2] == "HTTP/1.0" || parts[2] == "HTTP/1.1") && h1Method(parts[0]):
		h.sink.sawTarget(parts[1])
	default:
		h.lose()
		return
	}
	h.state = h1Header
}

// h1Method reports whether m is a request method this parser knows.
func h1Method(m string) bool {
	for _, known := range h1Methods {
		if known == m+" " {
			return true
		}
	}
	return false
}

// header reads one header line: its name is searched, and Content-Length
// and Transfer-Encoding say how the body is framed.
func (h *h1Parser) header(line string) {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		if line[0] == ' ' || line[0] == '\t' {
			// A folded continuation of the header before it.
			return
		}
		h.lose()
		return
	}
	value = strings.TrimSpace(value)
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "content-length":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			h.lose()
			return
		}
		h.length = n
	case "transfer-encoding":
		h.chunked = strings.Contains(strings.ToLower(value), "chunked")
	}
	credential(h.sink, lower, value != "", false)
}

// headersDone ends a message's headers (or its trailers) and sets how its
// body is read.
func (h *h1Parser) headersDone() {
	noBody := h.response && (h.status < 200 || h.status == 204 || h.status == 304)
	switch {
	case h.state == h1Trailer:
		h.state = h1Start
	case h.response && h.status == 101:
		// The connection switched to another protocol, not read here.
		h.state = h1Rest
		h.sink.unsearched()
	case noBody:
		h.state = h1Start
	case h.chunked:
		h.state = h1ChunkSize
	case h.length > 0:
		h.state, h.left = h1Body, uint64(h.length)
	case h.response && h.length < 0:
		// A response with no length ends when the connection does.
		h.state = h1Rest
	default:
		h.state = h1Start
	}
}

// bodyDone ends a body or a chunk.
func (h *h1Parser) bodyDone() {
	if h.state == h1ChunkData {
		h.state = h1ChunkSize
		return
	}
	h.state = h1Start
}

// gap implements parser: a gap inside a body of known length is skipped;
// one anywhere else loses the framing.
func (h *h1Parser) gap(n uint64) {
	switch {
	case h.state == h1Rest || h.state == h1Lost:
	case (h.state == h1Body || h.state == h1ChunkData) && n <= h.left:
		h.left -= n
		if h.left == 0 {
			h.bodyDone()
		}
	default:
		h.lose()
	}
}

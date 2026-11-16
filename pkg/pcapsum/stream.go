// Package pcapsum: what one direction of a TCP flow carries, read from its
// bytes in sequence order. Each parser is told the bytes that arrived and
// the bytes the capture lost, and says of every byte whether it was key
// exchange, encrypted, or readable by anyone on the wire. A byte it cannot
// account for is cleartext: nothing is called encrypted that was not seen to
// be.
package pcapsum

import "bytes"

// kind is what a packet's bytes were, as bits a packet's parts combine in.
type kind uint8

// The kinds. A packet holding any cleartext byte is cleartext; else any
// encrypted byte, encrypted; else key exchange. kindNone is a packet that
// carried nothing to judge (a bare ACK), or a frame carrying no IP.
const (
	kindNone        kind = 0
	kindKeyExchange kind = 1 << 0
	kindEncrypted   kind = 1 << 1
	kindCleartext   kind = 1 << 2
)

// final is the one kind a packet whose bytes were k is counted as.
func (k kind) final() kind {
	switch {
	case k&kindCleartext != 0:
		return kindCleartext
	case k&kindEncrypted != 0:
		return kindEncrypted
	case k&kindKeyExchange != 0:
		return kindKeyExchange
	}
	return kindNone
}

// parser reads one direction of a flow. feed is given the next bytes in
// sequence; fresh is set when they begin a segment as it was sent, the only
// place a parser that lost its place may find it again. gap is told how
// many bytes the capture lacks before the next ones fed.
type parser interface {
	feed(p []byte, fresh bool) kind
	gap(n uint64)
}

// opening is what a direction's first bytes say it is.
type opening int

// The openings a direction is recognized by.
const (
	openPlain      opening = iota // none below: cleartext
	openSSH                       // an SSH-2.0 (or 1.99) banner
	openTLSHello                  // a TLS handshake record holding a ClientHello
	openTLSReply                  // a TLS handshake record holding a ServerHello
	openH2Client                  // HTTP/2's client connection preface
	openH1Request                 // an HTTP/1.x request line
	openH1Response                // an HTTP/1.x status line
)

// h1Methods are the request methods an HTTP/1.x request line is known by,
// each with the space that follows it.
var h1Methods = []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "PATCH ", "CONNECT ", "TRACE "}

// h2Preface is the HTTP/2 client connection preface (RFC 9113 section 3.4).
const h2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

// openingLen is the most bytes open needs to decide.
const openingLen = len(h2Preface)

// open decides what a direction is from its first bytes, at most
// openingLen of them.
func open(p []byte) opening {
	switch {
	case bytes.HasPrefix(p, []byte("SSH-2.0-")), bytes.HasPrefix(p, []byte("SSH-1.99-")):
		return openSSH
	case bytes.HasPrefix(p, []byte(h2Preface)):
		return openH2Client
	case len(p) >= 6 && tlsHeader(p) && p[0] == tlsHandshake:
		// The handshake message's own type, the record's first byte.
		switch p[5] {
		case 1:
			return openTLSHello
		case 2:
			return openTLSReply
		}
	case bytes.HasPrefix(p, []byte("HTTP/1.0 ")), bytes.HasPrefix(p, []byte("HTTP/1.1 ")):
		return openH1Response
	}
	for _, m := range h1Methods {
		if bytes.HasPrefix(p, []byte(m)) {
			return openH1Request
		}
	}
	return openPlain
}

// The bounds on calling bytes that were framed as encrypted text instead:
// a run of at least textMinBytes captured bytes (a TLS application data
// record's body, or a block of what SSH sent after NEWKEYS) of which at
// least textNum/textDen are printable ASCII. Encrypted bytes are uniform, so
// about 38% of them are printable (98 of 256 values); 48 printable of 64 is
// 6 standard deviations above that (a chance of about 3 in a billion for one
// run, and less for a longer one), and the real sessions this package is
// tested on never come close (TestText_RealSessionsAreNeverText).
const (
	textMinBytes = 64
	textBlock    = 256
	textNum      = 3
	textDen      = 4
)

// textCheck counts how many of a run of bytes are printable ASCII.
type textCheck struct {
	n, printable int
}

// add counts p.
func (c *textCheck) add(p []byte) {
	c.n += len(p)
	for _, b := range p {
		if (b >= 0x20 && b < 0x7f) || b == '\t' || b == '\n' || b == '\r' {
			c.printable++
		}
	}
}

// text reports whether the run counted is long enough and printable enough
// to be text, not ciphertext.
func (c *textCheck) text() bool {
	return c.n >= textMinBytes && c.printable*textDen >= c.n*textNum
}

// textual is a parser that can say bytes it framed as encrypted read as
// text: TLS with a NULL cipher or a forged record, SSH's none cipher.
type textual interface {
	sawText() bool
}

// plain is a direction nothing was recognized in: every byte is cleartext.
type plain struct{}

// feed implements parser.
func (plain) feed([]byte, bool) kind { return kindCleartext }

// gap implements parser.
func (plain) gap(uint64) {}

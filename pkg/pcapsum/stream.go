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
	openPlain    opening = iota // none below: cleartext
	openSSH                     // an SSH-2.0 (or 1.99) banner
	openTLSHello                // a TLS handshake record holding a ClientHello
	openTLSReply                // a TLS handshake record holding a ServerHello
	openH2Client                // HTTP/2's client connection preface
)

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
	}
	return openPlain
}

// plain is a direction nothing was recognized in: every byte is cleartext.
type plain struct{}

// feed implements parser.
func (plain) feed([]byte, bool) kind { return kindCleartext }

// gap implements parser.
func (plain) gap(uint64) {}

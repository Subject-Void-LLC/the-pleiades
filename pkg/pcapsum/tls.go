// Package pcapsum: one direction of TLS, read record by record from each
// record's header (RFC 8446 section 5.1, RFC 5246 section 6.2): handshake
// records are key exchange, application data is encrypted.
package pcapsum

import "encoding/binary"

// The TLS record content types (RFC 8446 section 5.1).
const (
	tlsChangeCipherSpec = 0x14
	tlsAlert            = 0x15
	tlsHandshake        = 0x16
	tlsApplicationData  = 0x17
)

// tlsMaxRecord is the longest record TLS 1.2 allows on the wire, 2^14 bytes
// and 2048 of expansion (RFC 5246 section 6.2.3); TLS 1.3's is shorter.
const tlsMaxRecord = 1<<14 + 2048

// tlsHeader reports whether p begins with a record header: a content type
// of change cipher spec, alert, handshake or application data, a version of
// 0x0301 to 0x0304, and a length TLS allows (only application data may be
// empty).
func tlsHeader(p []byte) bool {
	if len(p) < 5 || p[0] < tlsChangeCipherSpec || p[0] > tlsApplicationData || p[1] != 3 || p[2] < 1 || p[2] > 4 {
		return false
	}
	n := binary.BigEndian.Uint16(p[3:])
	return n <= tlsMaxRecord && (n > 0 || p[0] == tlsApplicationData)
}

// tlsParser reads one direction of TLS record by record.
type tlsParser struct {
	hdr       [5]byte
	hdrN      int
	remaining uint64
	typ       byte
	// encrypted is set once this direction sent change cipher spec or
	// application data: an alert after it is encrypted too.
	encrypted bool
	// lost is set when a byte was not where a record header should be; the
	// direction is read again only from a segment that begins with one.
	lost bool
	// body counts the printable bytes of the application data record being
	// read, and text is set once one read as text rather than ciphertext.
	body textCheck
	text bool
}

// recordKind is the kind of a record of the type being read.
func (t *tlsParser) recordKind() kind {
	switch t.typ {
	case tlsApplicationData:
		return kindEncrypted
	case tlsAlert:
		if t.encrypted {
			return kindEncrypted
		}
	}
	return kindKeyExchange
}

// feed implements parser.
func (t *tlsParser) feed(p []byte, fresh bool) kind {
	if t.lost {
		if !fresh || !tlsHeader(p) {
			return kindCleartext
		}
		t.lost, t.hdrN, t.remaining = false, 0, 0
	}
	var k kind
	for len(p) > 0 {
		if t.hdrN < len(t.hdr) {
			n := copy(t.hdr[t.hdrN:], p)
			t.hdrN += n
			p = p[n:]
			if t.hdrN < len(t.hdr) {
				// Only a header's first bytes: what they begin is not known
				// until the rest arrives, and a header itself is no secret.
				k |= kindKeyExchange
				continue
			}
			if !tlsHeader(t.hdr[:]) {
				t.hdrN = 0
				t.lost = true
				return k | kindCleartext
			}
			t.typ = t.hdr[0]
			t.remaining = uint64(binary.BigEndian.Uint16(t.hdr[3:]))
			t.body = textCheck{}
			k |= t.recordKind()
			if t.remaining == 0 {
				t.end()
			}
			continue
		}
		n := min(uint64(len(p)), t.remaining)
		k |= t.recordKind()
		if t.typ == tlsApplicationData {
			t.body.add(p[:n])
		}
		t.remaining -= n
		p = p[n:]
		if t.remaining == 0 {
			t.end()
		}
	}
	return k
}

// end ends a record.
func (t *tlsParser) end() {
	t.judgeBody()
	t.hdrN = 0
	if t.typ == tlsChangeCipherSpec || t.typ == tlsApplicationData {
		t.encrypted = true
	}
}

// gap implements parser: a gap inside a record's body is skipped, since its
// length is known; one anywhere else loses the framing until a segment
// begins with a record header.
func (t *tlsParser) gap(n uint64) {
	if t.lost {
		return
	}
	if t.hdrN == len(t.hdr) && n <= t.remaining {
		t.remaining -= n
		if t.remaining == 0 {
			t.end()
		}
		return
	}
	t.judgeBody()
	t.lost = true
}

// judgeBody sets text when the application data record read so far is text.
func (t *tlsParser) judgeBody() {
	if t.hdrN == len(t.hdr) && t.typ == tlsApplicationData && t.body.text() {
		t.text = true
	}
}

// sawText implements textual: an application data record whose body was
// text (a NULL cipher suite, or a record forged around cleartext), the one
// the capture ends inside included.
func (t *tlsParser) sawText() bool {
	t.judgeBody()
	return t.text
}

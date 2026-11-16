// Package pcapsum: one direction of SSH (RFC 4253), read only as far as
// telling its banner and key exchange from what NEWKEYS put under the new
// keys.
package pcapsum

import (
	"bytes"
	"encoding/binary"
)

// The SSH limits: a banner line is at most 255 bytes (RFC 4253 section 4.2),
// and a packet before NEWKEYS at most OpenSSH's own limit, 256 KiB.
const (
	sshMaxBanner = 255
	sshMaxPacket = 256 << 10
)

// The SSH parser's states.
const (
	sshBanner    = iota // reading the version line
	sshPacket           // reading binary packets in the clear
	sshEncrypted        // past this direction's NEWKEYS
	sshLost             // something no SSH peer sends: the rest is unjudged
)

// sshParser reads one direction of SSH: its banner, then binary packets in
// the clear (KEXINIT, the key exchange method's own messages, NEWKEYS),
// then, after NEWKEYS, bytes under the new keys, whose packet lengths may be
// encrypted too, so they are not framed at all.
type sshParser struct {
	state     int
	bannerLen int
	// hdr holds a packet's first six bytes: its length, its padding length
	// and its message type.
	hdr  [6]byte
	hdrN int
	// remaining is the packet's bytes after hdr not yet seen.
	remaining uint64
	// msg is the kind of the packet being read, newkeys whether it is
	// NEWKEYS.
	msg     kind
	newkeys bool
}

// feed implements parser.
func (s *sshParser) feed(p []byte, _ bool) kind {
	var k kind
	for len(p) > 0 {
		switch s.state {
		case sshBanner:
			i := bytes.IndexByte(p, '\n')
			n := len(p)
			if i >= 0 {
				n = i + 1
			}
			s.bannerLen += n
			if s.bannerLen > sshMaxBanner {
				s.state = sshLost
				continue
			}
			k |= kindKeyExchange
			p = p[n:]
			if i >= 0 {
				s.state = sshPacket
			}
		case sshPacket:
			if s.hdrN < len(s.hdr) {
				n := copy(s.hdr[s.hdrN:], p)
				s.hdrN += n
				p = p[n:]
				if s.hdrN < len(s.hdr) {
					k |= kindKeyExchange
					continue
				}
				if !s.begin() {
					// The header just read is no SSH packet's: it, and all
					// after it, is unjudged.
					s.state = sshLost
					k |= kindCleartext
					continue
				}
				k |= s.msg
				if s.remaining == 0 {
					s.end()
				}
				continue
			}
			n := min(uint64(len(p)), s.remaining)
			k |= s.msg
			s.remaining -= n
			p = p[n:]
			if s.remaining == 0 {
				s.end()
			}
		case sshEncrypted:
			return k | kindEncrypted
		default:
			return k | kindCleartext
		}
	}
	return k
}

// begin reads a packet's header from hdr, false for one no SSH peer sends
// before NEWKEYS: a length out of bounds or not a multiple of 8 with its
// length field, padding under 4 bytes or leaving no payload, or a message
// type that has no place before encryption.
func (s *sshParser) begin() bool {
	length := binary.BigEndian.Uint32(s.hdr[:])
	pad := uint32(s.hdr[4])
	if length < 12 || length > sshMaxPacket || (length+4)%8 != 0 || pad < 4 || pad+2 > length {
		return false
	}
	typ := s.hdr[5]
	switch {
	case typ == 20 || typ == 21 || (typ >= 30 && typ <= 49):
		// KEXINIT, NEWKEYS, and the key exchange methods' own range.
		s.msg = kindKeyExchange
	case typ >= 1 && typ <= 4:
		// DISCONNECT, IGNORE, UNIMPLEMENTED, DEBUG: sent, and readable, in
		// the clear.
		s.msg = kindCleartext
	default:
		return false
	}
	s.newkeys = typ == 21
	s.remaining = uint64(length) - 2
	return true
}

// end ends a packet: after NEWKEYS, everything this direction sends is
// under the new keys.
func (s *sshParser) end() {
	s.hdrN = 0
	if s.newkeys {
		s.state = sshEncrypted
	}
}

// gap implements parser: a gap inside a packet's body is skipped, since its
// length is known; one anywhere else loses the framing.
func (s *sshParser) gap(n uint64) {
	switch {
	case s.state == sshEncrypted || s.state == sshLost:
	case s.state == sshPacket && s.hdrN == len(s.hdr) && n <= s.remaining:
		s.remaining -= n
		if s.remaining == 0 {
			s.end()
		}
	default:
		s.state = sshLost
	}
}

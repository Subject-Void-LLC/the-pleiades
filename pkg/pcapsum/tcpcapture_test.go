// A TCP capture writer for the flow tests: the bytes a real session sent,
// each way in the order they crossed, written as the segments a capture of
// that session holds (the handshake, data cut at a 1448-byte MSS, an ACK for
// each, the FIN), every frame padded to Ethernet's 60-byte minimum as a
// switch pads it. Each segment can then be lost, repeated, swapped or cut at
// a snapshot length, as a real capture's are.
package pcapsum_test

import (
	"encoding/binary"
	"net"
	"sync"
)

// chunk is bytes one side sent: from the client when up is set.
type chunk struct {
	up   bool
	data []byte
}

// recorder is a client's net.Conn that keeps what it sent and what it read,
// in the order each happened.
type recorder struct {
	net.Conn
	mu     sync.Mutex
	chunks []chunk
}

// Read implements net.Conn, keeping what the server sent.
func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	if n > 0 {
		r.keep(false, p[:n])
	}
	return n, err
}

// Write implements net.Conn, keeping what the client sent.
func (r *recorder) Write(p []byte) (int, error) {
	r.keep(true, p)
	return r.Conn.Write(p)
}

// keep records one chunk.
func (r *recorder) keep(up bool, p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, chunk{up: up, data: append([]byte(nil), p...)})
}

// recorded is what the recorder kept.
func (r *recorder) recorded() []chunk {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]chunk(nil), r.chunks...)
}

// pkt is one TCP segment of a capture being written.
type pkt struct {
	up       bool
	flags    byte
	seq, ack uint32
	data     []byte
	// snap, when set, cuts the frame at this many bytes as a snapshot
	// length does; the IP header still gives the length on the wire.
	snap int
}

// The two hosts and their ports.
var (
	client = [4]byte{192, 0, 2, 10}
	server = [4]byte{198, 51, 100, 20}
)

// flowPackets are the segments of a session of chunks between the client's
// cport and the server's sport.
func flowPackets(chunks []chunk) []pkt {
	const mss = 1448
	cseq, sseq := uint32(1000), uint32(500000)
	out := []pkt{
		{up: true, flags: 0x02, seq: cseq},
		{up: false, flags: 0x12, seq: sseq, ack: cseq + 1},
		{up: true, flags: 0x10, seq: cseq + 1, ack: sseq + 1},
	}
	cseq, sseq = cseq+1, sseq+1
	// What one side sent before the other answered is one stream to its
	// TCP, cut at the MSS wherever its bytes fall, not where the reader's
	// reads did.
	var merged []chunk
	for _, c := range chunks {
		if n := len(merged); n > 0 && merged[n-1].up == c.up {
			merged[n-1].data = append(merged[n-1].data, c.data...)
			continue
		}
		merged = append(merged, chunk{up: c.up, data: append([]byte(nil), c.data...)})
	}
	for _, c := range merged {
		for d := c.data; len(d) > 0; {
			n := min(len(d), mss)
			if c.up {
				out = append(out, pkt{up: true, flags: 0x18, seq: cseq, ack: sseq, data: d[:n]})
				cseq += uint32(n)
				out = append(out, pkt{up: false, flags: 0x10, seq: sseq, ack: cseq})
			} else {
				out = append(out, pkt{up: false, flags: 0x18, seq: sseq, ack: cseq, data: d[:n]})
				sseq += uint32(n)
				out = append(out, pkt{up: true, flags: 0x10, seq: cseq, ack: sseq})
			}
			d = d[n:]
		}
	}
	return append(out,
		pkt{up: true, flags: 0x11, seq: cseq, ack: sseq},
		pkt{up: false, flags: 0x11, seq: sseq, ack: cseq + 1},
		pkt{up: true, flags: 0x10, seq: cseq + 1, ack: sseq + 1})
}

// tcpFrames are the Ethernet frames of segments between client:cport and
// server:sport.
func tcpFrames(segs []pkt, cport, sport uint16) [][]byte {
	out := make([][]byte, 0, len(segs))
	for _, s := range segs {
		src, dst, sp, dp := client, server, cport, sport
		if !s.up {
			src, dst, sp, dp = server, client, sport, cport
		}
		h := make([]byte, 20)
		binary.BigEndian.PutUint16(h, sp)
		binary.BigEndian.PutUint16(h[2:], dp)
		binary.BigEndian.PutUint32(h[4:], s.seq)
		binary.BigEndian.PutUint32(h[8:], s.ack)
		h[12], h[13] = 5<<4, s.flags
		binary.BigEndian.PutUint16(h[14:], 65535)
		f := eth(0x0800, ipv4(src, dst, 6, append(h, s.data...)), false)
		for len(f) < 60 {
			// Ethernet's minimum frame: a switch pads a bare ACK with zeros,
			// which are no part of the segment.
			f = append(f, 0)
		}
		if s.snap > 0 && s.snap < len(f) {
			f = f[:s.snap]
		}
		out = append(out, f)
	}
	return out
}

// sessionCapture is a pcap of a session of chunks between the client's
// cport and the server's sport.
func sessionCapture(chunks []chunk, cport, sport uint16) []byte {
	return pcapFile(binary.LittleEndian, tcpFrames(flowPackets(chunks), cport, sport))
}

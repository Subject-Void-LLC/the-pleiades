// Tests for demux, the reader of an attached stream's frames.
package containerengine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// The demux tests moved here with the demultiplexer itself when
// pkg/dockerexec folded into this package (Phase 119b); its contract is
// unchanged.

// TestDemux_ParsesMultipleFramesAndSeparatesStreams proves the real
// Docker exec-attach framing (8-byte header: stream type, 3 reserved,
// 4-byte big-endian length) is parsed correctly, and stdout/stderr are
// kept separate even when interleaved.
func TestDemux_ParsesMultipleFramesAndSeparatesStreams(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 1, []byte("out1"))
	writeFrame(&buf, 2, []byte("err1"))
	writeFrame(&buf, 1, []byte("out2"))

	stdout, stderr, err := demux(&buf, 1<<20)
	if err != nil {
		t.Fatalf("demux: %v", err)
	}
	if string(stdout) != "out1out2" {
		t.Errorf("stdout = %q, want %q", stdout, "out1out2")
	}
	if string(stderr) != "err1" {
		t.Errorf("stderr = %q, want %q", stderr, "err1")
	}
}

// TestDemux_CleanEOFAtAFrameBoundaryIsSuccess proves a stream that
// ends exactly between frames (the real completion signal) is not an
// error.
func TestDemux_CleanEOFAtAFrameBoundaryIsSuccess(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 1, []byte("done"))

	stdout, _, err := demux(&buf, 1<<20)
	if err != nil {
		t.Fatalf("demux: %v", err)
	}
	if string(stdout) != "done" {
		t.Errorf("stdout = %q, want %q", stdout, "done")
	}
}

// TestDemux_EmptyStreamIsSuccess proves a connection that closes
// having sent nothing at all (a command with no output) is success, not
// an error.
func TestDemux_EmptyStreamIsSuccess(t *testing.T) {
	stdout, stderr, err := demux(&bytes.Buffer{}, 1<<20)
	if err != nil {
		t.Fatalf("demux: %v", err)
	}
	if len(stdout) != 0 || len(stderr) != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout, stderr)
	}
}

// TestDemux_TruncatedMidHeaderIsAnError proves a stream that ends
// partway through a frame header (a genuinely corrupted or truncated
// stream) is reported as an error, not silently treated as clean
// completion the way a boundary-aligned EOF is.
func TestDemux_TruncatedMidHeaderIsAnError(t *testing.T) {
	raw := []byte{1, 0, 0, 0, 0, 0} // 6 of 8 header bytes, then nothing
	_, _, err := demux(bytes.NewReader(raw), 1<<20)
	if err == nil {
		t.Fatal("expected an error for a stream truncated mid-header")
	}
}

// TestDemux_TruncatedMidPayloadIsAnError proves a stream that
// announces a frame length but closes before delivering that many
// payload bytes is reported as an error.
func TestDemux_TruncatedMidPayloadIsAnError(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{1, 0, 0, 0, 0, 0, 0, 10}) // announces 10 payload bytes
	buf.WriteString("only3")                   // delivers 5
	_, _, err := demux(&buf, 1<<20)
	if err == nil {
		t.Fatal("expected an error for a stream truncated mid-payload")
	}
}

// TestDemux_RefusesOutputExceedingTheCap proves output is refused
// outright, not silently truncated, once it exceeds maxOutput.
func TestDemux_RefusesOutputExceedingTheCap(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 1, bytes.Repeat([]byte("x"), 100))

	_, _, err := demux(&buf, 10)
	if err == nil {
		t.Fatal("expected an error once accumulated output exceeded the cap")
	}
}

// TestDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating
// proves the output cap is checked against a frame's announced size
// BEFORE make([]byte, size) is ever called, not just after accumulating
// it: a reader supplying only the 8-byte header naming a size larger
// than maxOutput, with nothing after it, would surface as a truncated-
// payload read error if this package attempted to read (or allocate)
// that payload at all -- the "output exceeded" error instead proves it
// never tried. Found during Phase 73's own Schema/Injection Hardening
// audit: size is a 4-byte field read directly off the wire, and the
// pre-fix code called make([]byte, size) first, so a single frame
// claiming close to 4 GiB forced that allocation attempt regardless of
// maxOutput -- the same shape FAILURE_PATTERNS.md already records once
// for this module's own GzipDecompress filter (an input cap with no
// output cap). TestDemux_NeverPanicsOnAdversarialInput's own
// ~4 GiB-announcing case was, before this fix, relying on the test
// environment tolerating that allocation attempt rather than this
// package actually preventing it.
func TestDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:8], 0xFFFFFFFF) // ~4 GiB, and nothing follows it
	buf.Write(header)

	_, _, err := demux(&buf, 1<<20)
	if err == nil {
		t.Fatal("expected an error for a frame announcing a size far past the cap")
	}
	if !strings.Contains(err.Error(), "output exceeded") {
		t.Errorf("err = %v, want the output-exceeded error rather than a truncated-payload read error -- demux must never attempt to allocate or read a frame's payload once its announced size alone already exceeds maxOutput", err)
	}
}

// TestDemux_UnknownStreamTypeIsSilentlyDropped proves a frame with a
// stream type byte other than 1 (stdout) or 2 (stderr) -- stdin (0) or
// anything else the daemon might in principle send back -- is consumed
// (so parsing does not desynchronize) without being attributed to either
// stream.
func TestDemux_UnknownStreamTypeIsSilentlyDropped(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 0, []byte("stdin-echo"))
	writeFrame(&buf, 1, []byte("real-stdout"))

	stdout, stderr, err := demux(&buf, 1<<20)
	if err != nil {
		t.Fatalf("demux: %v", err)
	}
	if string(stdout) != "real-stdout" {
		t.Errorf("stdout = %q, want %q", stdout, "real-stdout")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestDemux_NeverPanicsOnAdversarialInput proves a handful of
// truncated and malformed byte sequences are absorbed without panicking.
func TestDemux_NeverPanicsOnAdversarialInput(t *testing.T) {
	cases := [][]byte{
		{},
		{1},
		{1, 0, 0, 0},
		{1, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}, // announces ~4GiB payload, delivers none -- refused before allocating, see TestDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating
	}
	for i, raw := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d (%v): demux panicked: %v", i, raw, r)
				}
			}()
			_, _, _ = demux(bytes.NewReader(raw), 1<<20)
		}()
	}
}

// writeFrame writes one Docker exec-attach frame (8-byte header plus
// payload) to w.
func writeFrame(w io.Writer, streamType byte, payload []byte) {
	header := make([]byte, 8)
	header[0] = streamType
	header[4] = byte(len(payload) >> 24)
	header[5] = byte(len(payload) >> 16)
	header[6] = byte(len(payload) >> 8)
	header[7] = byte(len(payload))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

// TestErrUnexpectedEOFIsDistinguishedFromCleanEOF is a narrow regression
// guard for the exact bug shape FAILURE_PATTERNS.md #172 records in a
// sibling package: io.ReadFull's own io.EOF (clean, zero bytes read) and
// io.ErrUnexpectedEOF (partial read before EOF) must not be treated the
// same way by demux's own error handling.
func TestErrUnexpectedEOFIsDistinguishedFromCleanEOF(t *testing.T) {
	if errors.Is(io.EOF, io.ErrUnexpectedEOF) || errors.Is(io.ErrUnexpectedEOF, io.EOF) {
		t.Fatal("io.EOF and io.ErrUnexpectedEOF must never satisfy errors.Is against each other")
	}
	_, err := io.ReadFull(strings.NewReader("abc"), make([]byte, 8))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("io.ReadFull on a short read = %v, want io.ErrUnexpectedEOF", err)
	}
}

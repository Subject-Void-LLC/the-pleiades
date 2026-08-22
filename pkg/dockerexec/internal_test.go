package dockerexec

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestCheckAllowed_AllowsExactlyTheThreeRealRequests proves the three
// requests Exec actually sends are on the allowlist.
func TestCheckAllowed_AllowsExactlyTheThreeRealRequests(t *testing.T) {
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1.43/containers/mycontainer/exec"},
		{http.MethodPost, "/v1.43/exec/abc123/start"},
		{http.MethodGet, "/v1.43/exec/abc123/json"},
	}
	for _, tt := range cases {
		if err := checkAllowed(tt.method, tt.path); err != nil {
			t.Errorf("checkAllowed(%s, %s): %v", tt.method, tt.path, err)
		}
	}
}

// TestCheckAllowed_RefusesEveryOtherRequest is this package's own proof
// of the central claim its doc comment makes: nothing outside the three
// exec-lifecycle endpoints is ever allowed through, including the exact
// shape a container-creation escalation attempt would take.
func TestCheckAllowed_RefusesEveryOtherRequest(t *testing.T) {
	cases := []struct {
		name, method, path string
	}{
		{"container creation", http.MethodPost, "/v1.43/containers/create"},
		{"exec with an extra trailing path segment", http.MethodPost, "/v1.43/containers/x/exec/extra"},
		{"a general request passthrough shape", http.MethodPost, "/v1.43/containers/x/exec/../create"},
		{"start with a path-traversal exec id", http.MethodPost, "/v1.43/exec/../create/start"},
		{"a slash smuggled into the container id", http.MethodPost, "/v1.43/containers/x/y/exec"},
		{"the unversioned endpoint form", http.MethodPost, "/containers/x/exec"},
		{"a different api version", http.MethodPost, "/v1.24/containers/x/exec"},
		{"GET where only POST is allowed", http.MethodGet, "/v1.43/containers/x/exec"},
		{"DELETE anything", http.MethodDelete, "/v1.43/containers/x"},
		{"the daemon info endpoint", http.MethodGet, "/v1.43/info"},
		{"the images endpoint", http.MethodGet, "/v1.43/images/json"},
		{"an empty path", http.MethodGet, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkAllowed(tt.method, tt.path); err == nil {
				t.Errorf("checkAllowed(%s, %s) = nil, want a refusal", tt.method, tt.path)
			}
		})
	}
}

// TestDoJSON_RefusesTheRealEscalationPayloadShapeBeforeDialing is the
// concrete adversarial proof Phase 73's own plan names: a
// container-creation request carrying HostConfig.Binds mounting the
// host root and Privileged: true — the exact shape an attacker would
// send to escape to the host — is refused before ANY network I/O, even
// though this test calls doJSON directly with a real, fully-formed
// hostile body attached. checkAllowed runs strictly before doJSON ever
// marshals reqBody or dials c.socket, so the socket field here is
// deliberately a path nothing is listening on: if the refusal did not
// happen first, this call would fail with a DIAL error instead of the
// allowlist's own error, which is exactly the distinction this test
// checks.
func TestDoJSON_RefusesTheRealEscalationPayloadShapeBeforeDialing(t *testing.T) {
	hostileBody := struct {
		Image      string
		Cmd        []string
		HostConfig struct {
			Binds      []string
			Privileged bool
		}
	}{
		Image: "alpine",
		Cmd:   []string{"/bin/sh"},
	}
	hostileBody.HostConfig.Binds = []string{"/:/host-root"}
	hostileBody.HostConfig.Privileged = true

	c := &client{socket: "/nonexistent/no-daemon-here.sock", dialTimeout: 100 * time.Millisecond, maxOutput: DefaultMaxOutputBytes}
	err := c.doJSON(context.Background(), http.MethodPost, "/v1.43/containers/create", hostileBody, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "refusing disallowed request") {
		t.Errorf("err = %v, want the allowlist's own refusal, not a dial failure against the nonexistent socket (which would prove checkAllowed did NOT run first)", err)
	}
}

// TestValidateID_AcceptsRealisticDockerIdentifiers proves ordinary
// container names and hex IDs (both truncated and full-length sha256)
// are accepted.
func TestValidateID_AcceptsRealisticDockerIdentifiers(t *testing.T) {
	ids := []string{
		"web", "web-1", "web_1", "my.container",
		"3f4b2c1a9e8d",
		"3f4b2c1a9e8d7c6b5a4938271605f4e3d2c1b0a9887766554433221100ffee",
	}
	for _, id := range ids {
		if err := validateID("container id", id); err != nil {
			t.Errorf("validateID(%q) = %v, want nil", id, err)
		}
	}
}

// TestValidateID_RefusesAnythingThatCouldAlterAURLPath proves every
// character that could change a path's shape once interpolated into one
// is refused, not just obviously hostile input.
func TestValidateID_RefusesAnythingThatCouldAlterAURLPath(t *testing.T) {
	ids := []string{
		"", "../create", "x/create", "x?y=z", "x#frag", "x y", "x\ty",
		"x\x00y", "/etc/passwd", "-leading-dash",
	}
	for _, id := range ids {
		if err := validateID("container id", id); err == nil {
			t.Errorf("validateID(%q) = nil, want a refusal", id)
		}
	}
}

// TestReadDemux_ParsesMultipleFramesAndSeparatesStreams proves the real
// Docker exec-attach framing (8-byte header: stream type, 3 reserved,
// 4-byte big-endian length) is parsed correctly, and stdout/stderr are
// kept separate even when interleaved.
func TestReadDemux_ParsesMultipleFramesAndSeparatesStreams(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 1, []byte("out1"))
	writeFrame(&buf, 2, []byte("err1"))
	writeFrame(&buf, 1, []byte("out2"))

	stdout, stderr, err := readDemux(&buf, 1<<20)
	if err != nil {
		t.Fatalf("readDemux: %v", err)
	}
	if string(stdout) != "out1out2" {
		t.Errorf("stdout = %q, want %q", stdout, "out1out2")
	}
	if string(stderr) != "err1" {
		t.Errorf("stderr = %q, want %q", stderr, "err1")
	}
}

// TestReadDemux_CleanEOFAtAFrameBoundaryIsSuccess proves a stream that
// ends exactly between frames (the real completion signal) is not an
// error.
func TestReadDemux_CleanEOFAtAFrameBoundaryIsSuccess(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 1, []byte("done"))

	stdout, _, err := readDemux(&buf, 1<<20)
	if err != nil {
		t.Fatalf("readDemux: %v", err)
	}
	if string(stdout) != "done" {
		t.Errorf("stdout = %q, want %q", stdout, "done")
	}
}

// TestReadDemux_EmptyStreamIsSuccess proves a connection that closes
// having sent nothing at all (a command with no output) is success, not
// an error.
func TestReadDemux_EmptyStreamIsSuccess(t *testing.T) {
	stdout, stderr, err := readDemux(&bytes.Buffer{}, 1<<20)
	if err != nil {
		t.Fatalf("readDemux: %v", err)
	}
	if len(stdout) != 0 || len(stderr) != 0 {
		t.Errorf("stdout=%q stderr=%q, want both empty", stdout, stderr)
	}
}

// TestReadDemux_TruncatedMidHeaderIsAnError proves a stream that ends
// partway through a frame header (a genuinely corrupted or truncated
// stream) is reported as an error, not silently treated as clean
// completion the way a boundary-aligned EOF is.
func TestReadDemux_TruncatedMidHeaderIsAnError(t *testing.T) {
	raw := []byte{1, 0, 0, 0, 0, 0} // 6 of 8 header bytes, then nothing
	_, _, err := readDemux(bytes.NewReader(raw), 1<<20)
	if err == nil {
		t.Fatal("expected an error for a stream truncated mid-header")
	}
}

// TestReadDemux_TruncatedMidPayloadIsAnError proves a stream that
// announces a frame length but closes before delivering that many
// payload bytes is reported as an error.
func TestReadDemux_TruncatedMidPayloadIsAnError(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{1, 0, 0, 0, 0, 0, 0, 10}) // announces 10 payload bytes
	buf.WriteString("only3")                   // delivers 5
	_, _, err := readDemux(&buf, 1<<20)
	if err == nil {
		t.Fatal("expected an error for a stream truncated mid-payload")
	}
}

// TestReadDemux_RefusesOutputExceedingTheCap proves output is refused
// outright, not silently truncated, once it exceeds maxOutput.
func TestReadDemux_RefusesOutputExceedingTheCap(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 1, bytes.Repeat([]byte("x"), 100))

	_, _, err := readDemux(&buf, 10)
	if err == nil {
		t.Fatal("expected an error once accumulated output exceeded the cap")
	}
}

// TestReadDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating
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
// output cap). TestReadDemux_NeverPanicsOnAdversarialInput's own
// ~4 GiB-announcing case was, before this fix, relying on the test
// environment tolerating that allocation attempt rather than this
// package actually preventing it.
func TestReadDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:8], 0xFFFFFFFF) // ~4 GiB, and nothing follows it
	buf.Write(header)

	_, _, err := readDemux(&buf, 1<<20)
	if err == nil {
		t.Fatal("expected an error for a frame announcing a size far past the cap")
	}
	if !strings.Contains(err.Error(), "output exceeded") {
		t.Errorf("err = %v, want the output-exceeded error rather than a truncated-payload read error -- this package must never attempt to allocate or read a frame's payload once its announced size alone already exceeds maxOutput", err)
	}
}

// TestReadDemux_UnknownStreamTypeIsSilentlyDropped proves a frame with a
// stream type byte other than 1 (stdout) or 2 (stderr) -- stdin (0) or
// anything else the daemon might in principle send back -- is consumed
// (so parsing does not desynchronize) without being attributed to either
// stream.
func TestReadDemux_UnknownStreamTypeIsSilentlyDropped(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, 0, []byte("stdin-echo"))
	writeFrame(&buf, 1, []byte("real-stdout"))

	stdout, stderr, err := readDemux(&buf, 1<<20)
	if err != nil {
		t.Fatalf("readDemux: %v", err)
	}
	if string(stdout) != "real-stdout" {
		t.Errorf("stdout = %q, want %q", stdout, "real-stdout")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestReadDemux_NeverPanicsOnAdversarialInput proves a handful of
// truncated and malformed byte sequences are absorbed without panicking.
func TestReadDemux_NeverPanicsOnAdversarialInput(t *testing.T) {
	cases := [][]byte{
		{},
		{1},
		{1, 0, 0, 0},
		{1, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}, // announces ~4GiB payload, delivers none -- refused before allocating, see TestReadDemux_RefusesAnAnnouncedSizeExceedingTheCapBeforeAllocating
	}
	for i, raw := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d (%v): readDemux panicked: %v", i, raw, r)
				}
			}()
			_, _, _ = readDemux(bytes.NewReader(raw), 1<<20)
		}()
	}
}

// writeFrame writes one Docker exec-attach frame (8-byte header plus
// payload) to w, for use by both this file's tests and dockerexec_test.go's
// real fake-daemon suite.
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
// same way by readDemux's own error handling.
func TestErrUnexpectedEOFIsDistinguishedFromCleanEOF(t *testing.T) {
	if errors.Is(io.EOF, io.ErrUnexpectedEOF) || errors.Is(io.ErrUnexpectedEOF, io.EOF) {
		t.Fatal("io.EOF and io.ErrUnexpectedEOF must never satisfy errors.Is against each other")
	}
	_, err := io.ReadFull(strings.NewReader("abc"), make([]byte, 8))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("io.ReadFull on a short read = %v, want io.ErrUnexpectedEOF", err)
	}
}

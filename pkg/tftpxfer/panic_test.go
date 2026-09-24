// Tests that a panic inside pin/tftp comes back as an error, and that a
// panic in the caller's own reader or writer does not.
package tftpxfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	tftp "github.com/pin/tftp/v3"
)

// heldPort returns a real UDP port held open and never answered, so a
// request that should never be sent has somewhere harmless to go.
func heldPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// overlongRequest is the request pin/tftp v3.2.0 was measured panicking
// on: a block size option plus a name too long for its 516-byte buffer.
var overlongRequest = struct {
	name string
	opts Options
}{strings.Repeat("a", 600), Options{BlockSize: 1024, Timeout: 100 * time.Millisecond, Retries: 1}}

// TestReceive_LibraryPanicComesBackAsAnError hands the library that request
// past the refusals that now stop it, and requires an error, not a crash.
func TestReceive_LibraryPanicComesBackAsAnError(t *testing.T) {
	var got bytes.Buffer
	n, err := receive("127.0.0.1", heldPort(t), overlongRequest.opts, overlongRequest.name, &got)
	if !errors.Is(err, errLibraryPanic) {
		t.Fatalf("receive() error = %v, want errLibraryPanic (if pin/tftp now bounds-checks its request, this test needs another way to make it panic)", err)
	}
	if n != 0 || got.Len() != 0 {
		t.Errorf("receive() reported %d bytes and wrote %d, want none", n, got.Len())
	}
}

// TestSend_LibraryPanicComesBackAsAnError is the same proof for an upload.
func TestSend_LibraryPanicComesBackAsAnError(t *testing.T) {
	n, err := send("127.0.0.1", heldPort(t), overlongRequest.opts, overlongRequest.name, strings.NewReader("image"))
	if !errors.Is(err, errLibraryPanic) {
		t.Fatalf("send() error = %v, want errLibraryPanic (if pin/tftp now bounds-checks its request, this test needs another way to make it panic)", err)
	}
	if n != 0 {
		t.Errorf("send() reported %d bytes, want none", n)
	}
}

// errCallerBug is the value the caller's reader and writer below panic
// with, so a test can tell that exact value from anything else.
var errCallerBug = errors.New("a bug in the caller's own reader or writer")

type panickingWriter struct{}

func (panickingWriter) Write([]byte) (int, error) { panic(errCallerBug) }

type panickingReader struct{}

func (panickingReader) Read([]byte) (int, error) { panic(errCallerBug) }

// serveOneFile starts a real pin/tftp server that serves file for any read
// and discards any write. Its short timeout lets Shutdown return promptly
// after a client that stopped mid-transfer.
func serveOneFile(t *testing.T, file []byte) int {
	t.Helper()
	srv := tftp.NewServer(
		func(_ string, rf io.ReaderFrom) error {
			_, err := rf.ReadFrom(bytes.NewReader(file))
			return err
		},
		func(_ string, wt io.WriterTo) error {
			_, err := wt.WriteTo(io.Discard)
			return err
		},
	)
	srv.SetTimeout(100 * time.Millisecond)
	srv.SetRetries(1)
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket: %v", err)
	}
	go func() { _ = srv.Serve(conn) }()
	t.Cleanup(srv.Shutdown)
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// panicValue runs call and returns what it panicked with, or nil.
func panicValue(call func()) (v any) {
	defer func() { v = recover() }()
	call()
	return nil
}

// TestGet_CallerWriterPanicIsRaisedAgainUnchanged keeps the recovery from
// hiding a bug in the caller's own destination as a transfer failure: the
// value must arrive exactly as raised, neither an error nor relabeled.
func TestGet_CallerWriterPanicIsRaisedAgainUnchanged(t *testing.T) {
	port := serveOneFile(t, []byte("firmware"))
	var err error
	v := panicValue(func() {
		_, err = Get(context.Background(), "127.0.0.1", port, Options{}, "fw.bin", panickingWriter{})
	})
	if v != errCallerBug {
		t.Fatalf("Get raised %v (%T) and returned error %v, want the writer's own panic value unchanged", v, v, err)
	}
}

// TestPut_CallerReaderPanicIsRaisedAgainUnchanged is the same for Put's
// source.
func TestPut_CallerReaderPanicIsRaisedAgainUnchanged(t *testing.T) {
	port := serveOneFile(t, nil)
	var err error
	v := panicValue(func() {
		_, err = Put(context.Background(), "127.0.0.1", port, Options{}, "fw.bin", panickingReader{})
	})
	if v != errCallerBug {
		t.Fatalf("Put raised %v (%T) and returned error %v, want the reader's own panic value unchanged", v, v, err)
	}
}

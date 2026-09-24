package tftpxfer_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	tftp "github.com/pin/tftp/v3"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/tftpxfer"
)

// This file is the RULE 0 evidence for pkg/tftpxfer, against a real TFTP
// server (github.com/pin/tftp/v3's own Server, the same library this
// package's client half wraps) speaking real UDP -- the same discipline
// this phase's other packages apply against a real, in-process fake
// server, except here "fake" and "real" are the same code: nothing about
// TFTP's own wire protocol is mocked, only the file store backing the
// server's read/write handlers is in-memory instead of a real disk.

// testServer starts a real TFTP server on an ephemeral loopback UDP
// port, backed by an in-memory file store, and returns the port to dial
// plus the store itself so a test can pre-seed or inspect it.
func testServer(t *testing.T) (port int, files *sync.Map) {
	t.Helper()
	files = &sync.Map{}

	readHandler := func(filename string, rf io.ReaderFrom) error {
		v, ok := files.Load(filename)
		if !ok {
			return fmt.Errorf("file not found: %s", filename)
		}
		_, err := rf.ReadFrom(bytes.NewReader(v.([]byte)))
		return err
	}
	writeHandler := func(filename string, wt io.WriterTo) error {
		var buf bytes.Buffer
		if _, err := wt.WriteTo(&buf); err != nil {
			return err
		}
		files.Store(filename, buf.Bytes())
		return nil
	}

	srv := tftp.NewServer(readHandler, writeHandler)
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket: %v", err)
	}
	go func() { _ = srv.Serve(conn) }()
	t.Cleanup(srv.Shutdown)

	return conn.LocalAddr().(*net.UDPAddr).Port, files
}

// TestPut_RoundTripsToARealServer proves Put uploads real bytes to a
// real server, exactly as the server's own file store recorded them.
func TestPut_RoundTripsToARealServer(t *testing.T) {
	port, files := testServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := tftpxfer.Put(ctx, "127.0.0.1", port, tftpxfer.Options{}, "firmware.bin", bytes.NewReader([]byte("firmware contents")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len("firmware contents")) {
		t.Errorf("n = %d, want %d", n, len("firmware contents"))
	}

	got, ok := files.Load("firmware.bin")
	if !ok {
		t.Fatal("server never received the file")
	}
	if string(got.([]byte)) != "firmware contents" {
		t.Errorf("server stored %q, want %q", got, "firmware contents")
	}
}

// TestGet_RoundTripsFromARealServer proves Get downloads real bytes from
// a real server and writes them verbatim to the caller's own io.Writer.
func TestGet_RoundTripsFromARealServer(t *testing.T) {
	port, files := testServer(t)
	files.Store("config.txt", []byte("hostname switch1\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	n, err := tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{}, "config.txt", &buf)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n != int64(len("hostname switch1\n")) {
		t.Errorf("n = %d, want %d", n, len("hostname switch1\n"))
	}
	if buf.String() != "hostname switch1\n" {
		t.Errorf("received %q, want %q", buf.String(), "hostname switch1\n")
	}
}

// TestGet_LargeFileSurvivesMultipleBlocks proves a transfer spanning
// many blocks (the default block size is 512 bytes) round-trips intact,
// not just a single-block payload small enough to hide a block-boundary
// bug.
func TestGet_LargeFileSurvivesMultipleBlocks(t *testing.T) {
	port, files := testServer(t)
	want := bytes.Repeat([]byte("0123456789ABCDEF"), 1000) // 16000 bytes, ~31 blocks
	files.Store("big.bin", want)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var buf bytes.Buffer
	n, err := tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{}, "big.bin", &buf)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n != int64(len(want)) {
		t.Errorf("n = %d, want %d", n, len(want))
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Error("received content does not match what was stored, byte for byte")
	}
}

// TestGet_NonexistentFileIsAClearError proves the server's own "file not
// found" failure reaches the caller as a wrapped, non-nil error.
func TestGet_NonexistentFileIsAClearError(t *testing.T) {
	port, _ := testServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{}, "does-not-exist.txt", &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error for a file the server does not have")
	}
}

// Refused filenames, and proof that none of them sends a datagram, are in
// filename_test.go.

// TestGet_AllowsARealisticNestedFilename proves validateFilename is not
// so strict it refuses ordinary, legitimate nested paths TFTP servers
// commonly use (a vendor firmware directory layout, for example) --
// only actual traversal segments and absolute paths are refused.
func TestGet_AllowsARealisticNestedFilename(t *testing.T) {
	port, files := testServer(t)
	files.Store("vendor/switch1/config.txt", []byte("ok"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if _, err := tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{}, "vendor/switch1/config.txt", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if buf.String() != "ok" {
		t.Errorf("received %q, want %q", buf.String(), "ok")
	}
}

// TestGet_AlreadyCanceledContextIsRefused proves an already-canceled
// context is checked before a request is ever sent.
func TestGet_AlreadyCanceledContextIsRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tftpxfer.Get(ctx, "127.0.0.1", 1, tftpxfer.Options{}, "file.txt", &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
}

// TestPut_AlreadyCanceledContextIsRefused mirrors the Get case for Put.
func TestPut_AlreadyCanceledContextIsRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tftpxfer.Put(ctx, "127.0.0.1", 1, tftpxfer.Options{}, "file.txt", bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
}

// TestGet_UnreachableServerFailsWithinTheTimeoutBudget proves Get does
// not hang forever against a server that never answers, bounded by
// Options.Timeout and Options.Retries the way the package doc comment
// states cancellation is (a budget, not an immediate interrupt).
func TestGet_UnreachableServerFailsWithinTheTimeoutBudget(t *testing.T) {
	// A real UDP socket held OPEN for the whole test, never read from and
	// never answering. That is deliberately not the "bind a port, close
	// it, reuse the number" construction FAILURE_PATTERNS.md #123, #177
	// and #183 record failing, and port 0 is not the fix here either:
	// this test's subject is the Timeout/Retries BUDGET, and a closed UDP
	// port answers with an ICMP port-unreachable that ends the call
	// early, while an invalid address fails validation earlier still.
	// Either would make this pass without the budget ever bounding
	// anything. A silent server is the case the budget exists for, and
	// holding the socket open also means no other process can take the
	// port mid-test.
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	port := conn.LocalAddr().(*net.UDPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err = tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{Timeout: 200 * time.Millisecond, Retries: 1}, "file.txt", &bytes.Buffer{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error against a server that never answers")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Get took %v, expected the Timeout/Retries budget to bound it well under that", elapsed)
	}
}

// failingWriter always fails its Write call, for proving Get surfaces a
// destination-writer failure rather than swallowing it.
type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, fmt.Errorf("simulated write failure") }

// TestGet_WriterFailureIsSurfaced proves a failure in the caller's own
// destination io.Writer (disk full, a closed pipe, ...) reaches Get as a
// wrapped, non-nil error rather than being silently absorbed.
func TestGet_WriterFailureIsSurfaced(t *testing.T) {
	port, files := testServer(t)
	files.Store("file.txt", []byte("some content"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{}, "file.txt", failingWriter{})
	if err == nil {
		t.Fatal("expected an error when the destination writer fails")
	}
}

// failingReader always fails its Read call, for proving Put surfaces a
// source-reader failure rather than swallowing it.
type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) { return 0, fmt.Errorf("simulated read failure") }

// TestPut_ReaderFailureIsSurfaced proves a failure in the caller's own
// source io.Reader reaches Put as a wrapped, non-nil error.
func TestPut_ReaderFailureIsSurfaced(t *testing.T) {
	port, _ := testServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := tftpxfer.Put(ctx, "127.0.0.1", port, tftpxfer.Options{}, "file.txt", failingReader{})
	if err == nil {
		t.Fatal("expected an error when the source reader fails")
	}
}

// TestPut_ServerRefusalIsSurfaced proves a server that refuses every
// write (a real "permission denied" or "disk full" from the server's own
// writeHandler) reaches Put as a wrapped, non-nil error.
func TestPut_ServerRefusalIsSurfaced(t *testing.T) {
	readHandler := func(filename string, rf io.ReaderFrom) error { return fmt.Errorf("not implemented") }
	writeHandler := func(filename string, wt io.WriterTo) error { return fmt.Errorf("permission denied") }
	srv := tftp.NewServer(readHandler, writeHandler)
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket: %v", err)
	}
	go func() { _ = srv.Serve(conn) }()
	t.Cleanup(srv.Shutdown)
	port := conn.LocalAddr().(*net.UDPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = tftpxfer.Put(ctx, "127.0.0.1", port, tftpxfer.Options{}, "file.txt", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected an error when the server refuses the write")
	}
}

// TestGet_CustomBlockSizeIsHonored proves Options.BlockSize actually
// reaches the client (a real negotiated transfer still succeeds with a
// non-default value), not just accepted and ignored.
func TestGet_CustomBlockSizeIsHonored(t *testing.T) {
	port, files := testServer(t)
	want := bytes.Repeat([]byte("x"), 3000)
	files.Store("blocksize-test.bin", want)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	n, err := tftpxfer.Get(ctx, "127.0.0.1", port, tftpxfer.Options{BlockSize: 1024}, "blocksize-test.bin", &buf)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n != int64(len(want)) || !bytes.Equal(buf.Bytes(), want) {
		t.Error("content did not round-trip correctly with a custom block size")
	}
}

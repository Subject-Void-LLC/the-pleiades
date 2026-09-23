// Tests that every refused filename and block size is refused before a
// datagram leaves, and that the longest accepted name crosses a real
// server whole.
package tftpxfer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/tftpxfer"
)

// silentListener holds a real UDP socket that never answers, and reports
// whether any datagram reached it, which is the observable meaning of "a
// request was sent". A packet sent over loopback is already queued on the
// socket by the time the sending call returns.
func silentListener(t *testing.T) (port int, received func() bool) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.ListenPacket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().(*net.UDPAddr).Port, func() bool {
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, _, err := conn.ReadFrom(make([]byte, 1<<16))
		return err == nil
	}
}

// quick bounds a call that wrongly reaches the silent listener, so a
// regression fails in a fraction of a second instead of waiting out the
// default retry budget. The largest block size is set so an over-long name
// that got past the refusal would reach the library's buffer overrun.
var quick = tftpxfer.Options{Timeout: 50 * time.Millisecond, Retries: 1, BlockSize: tftpxfer.MaxBlockSize}

// refusedFilenames are the names no request may carry: the traversal
// shapes refused since Phase 73, and every name a request cannot carry
// whole or a log could misread.
var refusedFilenames = []struct{ name, filename string }{
	{"empty", ""},
	{"parent traversal", "../etc/passwd"},
	{"parent traversal with backslashes", "..\\windows\\system32"},
	{"traversal in the middle", "a/../../b"},
	{"absolute path", "/etc/passwd"},
	{"absolute path with a backslash", "\\server\\share\\f"},
	{"drive letter", `C:\Windows`},
	{"NUL injecting the netascii mode", "firmware.bin\x00netascii"},
	{"NUL injecting a blksize option", "fw.bin\x00blksize\x0065464"},
	{"newline", "config.txt\nforged log line"},
	{"carriage return", "a\rb"},
	{"tab", "a\tb"},
	{"DEL", "a\x7fb"},
	{"C1 control (NEL)", "a\xc2\x85b"},
	{"right-to-left override", "\xe2\x80\xaegnp.exe"},
	{"zero-width space", "a\xe2\x80\x8bb"},
	{"byte order mark", "\xef\xbb\xbfconfig.txt"},
	{"invalid UTF-8", "\xff"},
	{"overlong encodings of dots", "\xc0\xae\xc0\xae/x"},
	{"encoded surrogate", "a\xed\xa0\x80"},
	{"one byte over the limit", strings.Repeat("a", tftpxfer.MaxFilenameBytes+1)},
	{"one byte over the limit in two-byte runes", strings.Repeat("\xc3\xa9", (tftpxfer.MaxFilenameBytes+1)/2)},
	{"600 bytes, the measured panic", strings.Repeat("a", 600)},
	{"64 KiB", strings.Repeat("a", 64<<10)},
}

// TestRefusedFilenamesNeverLeaveTheProcess drives each refused name
// through Get and Put and requires ErrInvalidFilename with no datagram
// sent. The error kind matters as much as the silence: an over-long name
// that reached pin/tftp would also send nothing, because the library
// panics before sending, and would come back as a recovered panic instead.
func TestRefusedFilenamesNeverLeaveTheProcess(t *testing.T) {
	calls := map[string]func(port int, filename string) (int64, error){
		"Get": func(port int, filename string) (int64, error) {
			return tftpxfer.Get(context.Background(), "127.0.0.1", port, quick, filename, &bytes.Buffer{})
		},
		"Put": func(port int, filename string) (int64, error) {
			return tftpxfer.Put(context.Background(), "127.0.0.1", port, quick, filename, strings.NewReader("image"))
		},
	}
	for _, tc := range refusedFilenames {
		for call, do := range calls {
			t.Run(call+"/"+tc.name, func(t *testing.T) {
				port, received := silentListener(t)
				n, err := do(port, tc.filename)
				if !errors.Is(err, tftpxfer.ErrInvalidFilename) {
					t.Errorf("error = %v, want ErrInvalidFilename", err)
				}
				if n != 0 {
					t.Errorf("reported %d bytes transferred, want 0", n)
				}
				if received() {
					t.Error("a datagram reached the server for a name that should never be sent")
				}
			})
		}
	}
}

// TestBlockSizeOutsideTheNegotiableRangeIsRefused covers Options.BlockSize
// at and past both ends of the range, for Get and Put. Below the range,
// pin/tftp would ignore a server's answer and end the download at its
// first block; above it, the option's digits no longer leave room for a
// name of MaxFilenameBytes.
func TestBlockSizeOutsideTheNegotiableRangeIsRefused(t *testing.T) {
	for _, size := range []int{-1, 1, 8, tftpxfer.MinBlockSize - 1, tftpxfer.MaxBlockSize + 1, 100000} {
		opts := tftpxfer.Options{Timeout: 50 * time.Millisecond, Retries: 1, BlockSize: size}
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			port, received := silentListener(t)
			_, getErr := tftpxfer.Get(context.Background(), "127.0.0.1", port, opts, "fw.bin", &bytes.Buffer{})
			_, putErr := tftpxfer.Put(context.Background(), "127.0.0.1", port, opts, "fw.bin", strings.NewReader("x"))
			for call, err := range map[string]error{"Get": getErr, "Put": putErr} {
				if err == nil || !strings.Contains(err.Error(), "block size") {
					t.Errorf("%s error = %v, want a block size refusal", call, err)
				}
			}
			if received() {
				t.Error("a datagram reached the server for a block size that should never be sent")
			}
		})
	}
}

// TestLongestAcceptedFilenamesCrossARealServerWhole puts and then gets
// each accepted edge case through a real server, with and without the
// largest block size. The server's store is keyed by the name it received,
// so a name cut short, or one the library could not pack, cannot pass.
func TestLongestAcceptedFilenamesCrossARealServerWhole(t *testing.T) {
	names := []struct{ name, filename string }{
		{"exactly the limit", strings.Repeat("a", tftpxfer.MaxFilenameBytes)},
		{"exactly the limit, nested", "firmware/" + strings.Repeat("b", tftpxfer.MaxFilenameBytes-len("firmware/"))},
		{"exactly the limit in two-byte runes", strings.Repeat("\xc3\xa9", tftpxfer.MaxFilenameBytes/2) + "a"},
		{"backslash separators", "vendor\\switch1\\config.txt"},
		{"a space", "running config.txt"},
		{"non-ASCII", "\xe9\x85\x8d\xe7\xbd\xae.txt"},
	}
	optionSets := map[string]tftpxfer.Options{
		"default":            {},
		"smallest blocksize": {BlockSize: tftpxfer.MinBlockSize},
		"largest blocksize":  {BlockSize: tftpxfer.MaxBlockSize},
	}
	for _, tc := range names {
		for label, opts := range optionSets {
			t.Run(label+"/"+tc.name, func(t *testing.T) {
				if len(tc.filename) > tftpxfer.MaxFilenameBytes {
					t.Fatalf("test name is %d bytes, over the limit it means to sit at", len(tc.filename))
				}
				port, files := testServer(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				payload := []byte("image for " + tc.name)
				if _, err := tftpxfer.Put(ctx, "127.0.0.1", port, opts, tc.filename, bytes.NewReader(payload)); err != nil {
					t.Fatalf("Put: %v", err)
				}
				if stored, ok := files.Load(tc.filename); !ok || !bytes.Equal(stored.([]byte), payload) {
					t.Fatalf("the server did not store the payload under the exact %d-byte name", len(tc.filename))
				}
				var got bytes.Buffer
				if _, err := tftpxfer.Get(ctx, "127.0.0.1", port, opts, tc.filename, &got); err != nil {
					t.Fatalf("Get: %v", err)
				}
				if !bytes.Equal(got.Bytes(), payload) {
					t.Errorf("Get returned %q, want %q", got.Bytes(), payload)
				}
			})
		}
	}
}

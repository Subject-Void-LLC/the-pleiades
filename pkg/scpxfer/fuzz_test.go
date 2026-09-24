// Fuzz targets for the SCP protocol against a hostile device.
package scpxfer

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// FuzzParseRecord attacks the record grammar. Whatever it accepts must
// name exactly one file by a plain name, with a non-negative size and a
// mode no wider than four octal digits allow.
func FuzzParseRecord(f *testing.F) {
	for _, seed := range []string{
		"0644 5 f", "0755 0 dir", "0644 -1 f", "0644 1 ../x", "0644 1 a/b", "0644 1 \x00",
		"9999 1 f", "0644 99999999999999999999 f", "0644 1 name with spaces", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		rec, err := parseRecord(line)
		if err != nil {
			return
		}
		if rec.size < 0 || rec.mode > 0o7777 {
			t.Fatalf("parseRecord(%q) accepted size %d mode %o", line, rec.size, rec.mode)
		}
		if rec.name == "" || rec.name == "." || rec.name == ".." || strings.ContainsAny(rec.name, "/\x00") {
			t.Fatalf("parseRecord(%q) accepted name %q", line, rec.name)
		}
	})
}

// limitCheck is a destination that fails the fuzz run the moment more
// than limit bytes reach it.
type limitCheck struct {
	t     *testing.T
	limit int64
	got   int64
}

// Write records and bounds.
func (l *limitCheck) Write(p []byte) (int, error) {
	l.got += int64(len(p))
	if l.got > l.limit {
		l.t.Fatalf("receive wrote %d bytes to the destination, past its limit of %d", l.got, l.limit)
	}
	return len(p), nil
}

// FuzzReceiveAgainstAHostileSource drives the Get side against arbitrary
// device output. It must never panic, never write past the caller's
// limit, and succeed only for exactly one record naming the requested
// file, its exact content, a zero byte and the end of the stream.
func FuzzReceiveAgainstAHostileSource(f *testing.F) {
	for _, seed := range []string{
		"C0644 5 f\nhello\x00", "C0644 5 f\nhello\x00C0644 1 f\nx\x00", "D0755 0 f\n", "T1 0 1 0\n",
		"\x02no\n", "C0644 999999 f\nshort", "C0644 5 g\nhello\x00", "C0644 0 f\n\x00", "\x00\x00\x00",
	} {
		f.Add([]byte(seed), int64(10))
	}
	f.Fuzz(func(t *testing.T, device []byte, limit int64) {
		if limit < 0 {
			limit = -limit
		}
		if limit < 0 { // math.MinInt64 stays negative
			limit = 0
		}
		dst := &limitCheck{t: t, limit: limit}
		n, err := receive(bufio.NewReader(bytes.NewReader(device)), io.Discard, dst, "f", limit)
		if n != dst.got {
			t.Fatalf("receive reported %d bytes but wrote %d", n, dst.got)
		}
		if err != nil {
			return
		}
		rest := string(device)
		if !strings.HasPrefix(rest, "C") || !strings.HasSuffix(rest, "\x00") {
			t.Fatalf("receive accepted device output %q that is not one record and a final zero", device)
		}
		header, _, _ := strings.Cut(rest[1:], "\n")
		rec, perr := parseRecord(header)
		if perr != nil || rec.name != "f" || rec.size != n || len(rest) != 1+len(header)+1+int(n)+1 {
			t.Fatalf("receive accepted %q as %d bytes of %q", device, n, rec.name)
		}
	})
}

// FuzzSendAgainstAHostileSink drives the Put side against arbitrary
// device replies. It must never panic, and must not read one byte of
// the caller's source unless the device answered both the ready byte
// and the record with a zero.
func FuzzSendAgainstAHostileSink(f *testing.F) {
	for _, seed := range []string{"\x00\x00\x00", "\x02nope\n", "\x00\x01warn\n", "banner\n", "", "\x00"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, device []byte) {
		src := &countingReader{r: strings.NewReader("content")}
		err := send(bufio.NewReader(bytes.NewReader(device)), io.Discard, src, 7, "f")
		accepted := len(device) >= 2 && device[0] == 0 && device[1] == 0
		if !accepted && src.read != 0 {
			t.Fatalf("send read %d source bytes after device replies %q", src.read, device)
		}
		if err == nil && (!accepted || len(device) < 3 || device[2] != 0) {
			t.Fatalf("send succeeded against device replies %q", device)
		}
		if err != nil && errors.Is(err, filexfer.ErrSizeMismatch) {
			t.Fatalf("send blamed a correct source for replies %q: %v", device, err)
		}
	})
}

// FuzzReadPreflight attacks the device's first answer: it must never
// panic, and never hold more than three bounded fields.
func FuzzReadPreflight(f *testing.F) {
	f.Add([]byte("/r\x00/r/s\x00f\x00"))
	f.Add([]byte("/r\n\x00/r\n\x00a\x00"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, device []byte) {
		ans, err := readPreflight(bufio.NewReader(bytes.NewReader(device)))
		if err != nil {
			return
		}
		if len(ans.root) > filexfer.MaxPathBytes || len(ans.parent) > filexfer.MaxPathBytes {
			t.Fatalf("readPreflight kept an answer longer than %d bytes", filexfer.MaxPathBytes)
		}
	})
}

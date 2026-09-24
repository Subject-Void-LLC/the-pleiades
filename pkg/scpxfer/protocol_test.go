// Tests for the SCP protocol against honest and hostile scripted devices.
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

// server wraps scripted device output as the reader the protocol
// functions consume.
func server(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

// countingReader counts how many bytes a protocol function pulled from
// the caller's source, which is how "not one byte of src is read before
// the device accepts" is measured rather than assumed.
type countingReader struct {
	r    io.Reader
	read int
}

// Read counts, then reads.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

// TestReadAck_Table covers every reply shape.
func TestReadAck_Table(t *testing.T) {
	var remote *RemoteError
	if err := readAck(server("\x00")); err != nil {
		t.Errorf("ack = %v, want nil", err)
	}
	if err := readAck(server("\x01disk nearly full\n")); !errors.As(err, &remote) || remote.Fatal || remote.Message != "disk nearly full" {
		t.Errorf("warning = %v, want a non-fatal RemoteError", err)
	}
	if err := readAck(server("\x02scp: /x: Permission denied\n")); !errors.As(err, &remote) || !remote.Fatal {
		t.Errorf("error = %v, want a fatal RemoteError", err)
	}
	if err := readAck(server("Welcome to the device!\n")); !errors.Is(err, errUnexpectedByte) {
		t.Errorf("banner = %v, want errUnexpectedByte naming a shell startup file", err)
	}
	if err := readAck(server("")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("EOF = %v, want io.ErrUnexpectedEOF", err)
	}
	if err := readAck(server("\x02" + strings.Repeat("x", maxMessageBytes+10) + "\n")); err == nil || errors.As(err, &remote) {
		t.Errorf("overlong message = %v, want it refused rather than held", err)
	}
	if err := readAck(server("\x02no newline")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated message = %v, want io.ErrUnexpectedEOF", err)
	}
	msg := (&RemoteError{Fatal: true, Message: "bad\x1b[31m"}).Error()
	if strings.Contains(msg, "\x1b") {
		t.Errorf("RemoteError %q prints a control character raw", msg)
	}
	if !strings.Contains((&RemoteError{Message: "w"}).Error(), "warning") {
		t.Error("a non-fatal RemoteError does not call itself a warning")
	}
}

// TestParseRecord_Table covers the strict record grammar.
func TestParseRecord_Table(t *testing.T) {
	good, err := parseRecord("0644 12 firmware.bin")
	if err != nil || good.mode != 0o644 || good.size != 12 || good.name != "firmware.bin" {
		t.Fatalf("parseRecord(good) = %+v, %v", good, err)
	}
	if spaced, err := parseRecord("0600 1 name with spaces"); err != nil || spaced.name != "name with spaces" {
		t.Errorf("a name with spaces = %+v, %v", spaced, err)
	}
	for _, line := range []string{
		"", "0644", "0644 12", "644 12 f", "06444 12 f", "0648 12 f", "0644 -1 f", "0644 +1 f",
		"0644  f", "0644 1e3 f", "0644 99999999999999999999 f", "0644 9223372036854775808 f",
		"0644 12 ", "0644 12 .", "0644 12 ..", "0644 12 a/b", "0644 12 ../../etc/passwd",
		"0644 12 a\x00b",
	} {
		if rec, err := parseRecord(line); err == nil {
			t.Errorf("parseRecord(%q) = %+v, want a refusal", line, rec)
		}
	}
}

// TestSend_HappyPath checks the exact bytes a sink receives.
func TestSend_HappyPath(t *testing.T) {
	var wire bytes.Buffer
	if err := send(server("\x00\x00\x00"), &wire, strings.NewReader("hello"), 5, "f.txt"); err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if want := "C0600 5 f.txt\nhello\x00"; wire.String() != want {
		t.Errorf("wire = %q, want %q", wire.String(), want)
	}
}

// TestSend_ReadsNothingUntilAccepted is the property that makes a
// refused Put harmless to the caller's source: every refusal before the
// record is accepted leaves it unread.
func TestSend_ReadsNothingUntilAccepted(t *testing.T) {
	for name, reply := range map[string]string{
		"not ready":        "\x02scp: cannot create\n",
		"banner":           "motd\n",
		"record refused":   "\x00\x02scp: no space\n",
		"silent then gone": "",
		"ready then gone":  "\x00",
	} {
		src := &countingReader{r: strings.NewReader("secret content")}
		if err := send(server(reply), io.Discard, src, 14, "f"); err == nil {
			t.Errorf("%s: send() error = nil, want a refusal", name)
		}
		if src.read != 0 {
			t.Errorf("%s: send() read %d bytes of the source before the device accepted", name, src.read)
		}
	}
}

// TestSend_SizeMismatchAndFinalAck covers the stream's own failures.
func TestSend_SizeMismatchAndFinalAck(t *testing.T) {
	if err := send(server("\x00\x00"), io.Discard, strings.NewReader("abc"), 5, "f"); !errors.Is(err, filexfer.ErrSizeMismatch) {
		t.Errorf("short source = %v, want ErrSizeMismatch", err)
	}
	if err := send(server("\x00\x00\x02write failed\n"), io.Discard, strings.NewReader("abc"), 3, "f"); err == nil {
		t.Error("a refused final ack returned nil")
	}
}

// TestReceive_Table runs the client's source side against honest and
// hostile devices.
func TestReceive_Table(t *testing.T) {
	tests := []struct {
		name    string
		device  string
		limit   int64
		want    string
		wantErr error // nil with ok false means any error
		ok      bool
	}{
		{"one file", "C0644 5 f\nhello\x00", 100, "hello", nil, true},
		{"empty file", "C0644 0 f\n\x00", 100, "", nil, true},
		{"binary content with protocol lookalikes", "C0644 6 f\n\x00\x01\x02C\n\x00\x00", 100, "\x00\x01\x02C\n\x00", nil, true},
		{"exact limit", "C0644 5 f\nhello\x00", 5, "hello", nil, true},
		{"over the limit", "C0644 6 f\nhello!\x00", 5, "", filexfer.ErrLimitExceeded, false},
		{"another name", "C0644 5 authorized_keys\nhello\x00", 100, "", nil, false},
		{"traversal name", "C0644 5 ../../.ssh/authorized_keys\nhello\x00", 100, "", nil, false},
		{"directory record", "D0755 0 d\n", 100, "", nil, false},
		{"timestamp record", "T1 0 1 0\n", 100, "", nil, false},
		{"end record", "E\n", 100, "", nil, false},
		{"error first", "\x02scp: f: No such file\n", 100, "", nil, false},
		{"banner", "Last login: yesterday\n", 100, "", errUnexpectedByte, false},
		{"second file", "C0644 5 f\nhello\x00C0644 1 g\nx\x00", 100, "hello", nil, false},
		{"missing trailing ack", "C0644 5 f\nhello", 100, "hello", nil, false},
		{"truncated content", "C0644 50 f\nhello", 100, "hello", nil, false},
		{"overlong record", "C0644 1 " + strings.Repeat("n", maxRecordBytes) + "\n", 100, "", nil, false},
		{"nothing at all", "", 100, "", io.ErrUnexpectedEOF, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got, acks bytes.Buffer
			n, err := receive(server(tc.device), &acks, &got, "f", tc.limit)
			if tc.ok {
				if err != nil || got.String() != tc.want || n != int64(len(tc.want)) {
					t.Fatalf("receive() = %d, %q, %v; want %q", n, got.String(), err, tc.want)
				}
				if acks.String() != "\x00\x00\x00" {
					t.Errorf("acks sent = %q, want three zero bytes", acks.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("receive() error = nil for a hostile device, wrote %q", got.String())
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("receive() error = %v, want %v", err, tc.wantErr)
			}
			if int64(got.Len()) > tc.limit {
				t.Errorf("receive() wrote %d bytes past a limit of %d", got.Len(), tc.limit)
			}
		})
	}
}

// failAfter is a writer that fails once it has taken n bytes.
type failAfter struct{ n int }

// Write fails after its budget.
func (f *failAfter) Write(p []byte) (int, error) {
	if len(p) > f.n {
		return 0, errors.New("sink closed")
	}
	f.n -= len(p)
	return len(p), nil
}

// TestProtocol_WriteFailuresAreReported covers the acks and content
// failing to go out, which a dropped session produces.
func TestProtocol_WriteFailuresAreReported(t *testing.T) {
	for budget := 0; budget < 3; budget++ {
		if _, err := receive(server("C0644 5 f\nhello\x00"), &failAfter{n: budget}, io.Discard, "f", 10); err == nil {
			t.Errorf("receive() with the ack channel failing after %d bytes returned nil", budget)
		}
	}
	if _, err := receive(server("C0644 5 f\nhello\x00"), io.Discard, &failAfter{n: 0}, "f", 10); err == nil {
		t.Error("receive() into a failing destination returned nil")
	}
	for _, budget := range []int{0, 12, 13} { // the record, the content, the final byte
		if err := send(server("\x00\x00\x00"), &failAfter{n: budget}, strings.NewReader("abc"), 3, "f"); err == nil {
			t.Errorf("send() with the wire failing after %d bytes returned nil", budget)
		}
	}
}

// TestReadPreflight_Table covers the device's first answer.
func TestReadPreflight_Table(t *testing.T) {
	ans, err := readPreflight(server("/srv/x\x00/srv/x/sub\x00f\x00"))
	if err != nil || ans.root != "/srv/x" || ans.parent != "/srv/x/sub" || ans.kind != filexfer.KindRegular || !ans.exists {
		t.Fatalf("readPreflight(file) = %+v, %v", ans, err)
	}
	kinds := map[string]filexfer.Kind{"d": filexfer.KindDirectory, "l": filexfer.KindSymlink, "o": filexfer.KindOther}
	for letter, kind := range kinds {
		if ans, err := readPreflight(server("/r\x00/r\x00" + letter + "\x00")); err != nil || ans.kind != kind {
			t.Errorf("readPreflight(%s) = %+v, %v", letter, ans, err)
		}
	}
	if ans, err := readPreflight(server("/r\x00/r\x00a\x00")); err != nil || ans.exists {
		t.Errorf("readPreflight(absent) = %+v, %v", ans, err)
	}
	for name, bad := range map[string]string{
		"unknown kind": "/r\x00/r\x00z\x00",
		"truncated":    "/r\x00/r",
		"endless":      strings.Repeat("a", filexfer.MaxPathBytes+10),
	} {
		if _, err := readPreflight(server(bad)); err == nil {
			t.Errorf("readPreflight(%s) error = nil", name)
		}
	}
}

// TestExitError_Table maps every script status.
func TestExitError_Table(t *testing.T) {
	p, _ := filexfer.Resolve("/srv/x", "f")
	var cErr *filexfer.ContainmentError
	if err := exitError(p, exitRootMissing, ""); !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentRootMissing {
		t.Errorf("exit 101 = %v", err)
	}
	if err := exitError(p, exitParentMissing, ""); !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentParentMissing {
		t.Errorf("exit 102 = %v", err)
	}
	for code, want := range map[int]string{
		exitDeclined: "declined", exitNoTempDir: "private directory", exitScpFailed: "scp failed",
		exitChmodFailed: "mode", exitRenameFailed: "rename", 1: "exited 1",
	} {
		if err := exitError(p, code, "why"); !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "why") {
			t.Errorf("exit %d = %v, want it to say %q and carry stderr", code, err, want)
		}
	}
	if err := exitError(p, 1, ""); strings.Contains(err.Error(), "stderr") {
		t.Errorf("exit without stderr = %v, want no empty stderr clause", err)
	}
}

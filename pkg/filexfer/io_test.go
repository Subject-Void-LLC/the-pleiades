// Tests for the exact-size reader and the limit writer.
package filexfer_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// TestExactReader_Table covers "exactly size bytes or nothing" in every
// shape a source can get it wrong.
func TestExactReader_Table(t *testing.T) {
	tests := []struct {
		name    string
		src     io.Reader
		size    int64
		want    string
		wantErr error
	}{
		{"exact", strings.NewReader("hello"), 5, "hello", nil},
		{"exact empty", strings.NewReader(""), 0, "", nil},
		{"short", strings.NewReader("hel"), 5, "hel", filexfer.ErrSizeMismatch},
		{"long", strings.NewReader("hello!"), 5, "hello", filexfer.ErrSizeMismatch},
		{"long when zero declared", strings.NewReader("x"), 0, "", filexfer.ErrSizeMismatch},
		{"one byte at a time", iotest.OneByteReader(strings.NewReader("hello")), 5, "hello", nil},
		{"data with its EOF", iotest.DataErrReader(strings.NewReader("hello")), 5, "hello", nil},
		{"source failure", iotest.ErrReader(errors.New("disk gone")), 5, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got bytes.Buffer
			_, err := io.Copy(&got, filexfer.ExactReader(tc.src, tc.size))
			if tc.name == "source failure" {
				if err == nil || !strings.Contains(err.Error(), "disk gone") {
					t.Fatalf("copy error = %v, want the source's own failure", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Fatalf("copy error = %v, want %v", err, tc.wantErr)
			}
			if got.String() != tc.want {
				t.Errorf("copied %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// TestExactReader_ReportsItsSize is what lets pkg/sftp pipeline writes.
func TestExactReader_ReportsItsSize(t *testing.T) {
	if got := filexfer.ExactReader(strings.NewReader("abc"), 3).Size(); got != 3 {
		t.Errorf("Size() = %d, want 3", got)
	}
}

// TestExactReader_EndIsStable pins that reading past the end keeps
// saying io.EOF without probing the source again.
func TestExactReader_EndIsStable(t *testing.T) {
	r := filexfer.ExactReader(strings.NewReader("ab"), 2)
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		if n, err := r.Read(make([]byte, 4)); n != 0 || err != io.EOF {
			t.Fatalf("Read() after the end = %d, %v; want 0, io.EOF", n, err)
		}
	}
}

// zeroThenEOF returns (0, nil) once before ending, which io.Reader's
// contract discourages but permits.
type zeroThenEOF struct{ calls int }

// Read returns nothing once, then io.EOF.
func (z *zeroThenEOF) Read([]byte) (int, error) {
	z.calls++
	if z.calls == 1 {
		return 0, nil
	}
	return 0, io.EOF
}

// TestExactReader_ProbeToleratesAnEmptyRead covers the one legal but
// odd reader shape the leftover probe must not mistake for data.
func TestExactReader_ProbeToleratesAnEmptyRead(t *testing.T) {
	r := filexfer.ExactReader(&zeroThenEOF{}, 0)
	if n, err := r.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("Read() = %d, %v; want 0, io.EOF", n, err)
	}
}

// failingReaderAfterSize yields its data, then fails instead of ending.
type failingReaderAfterSize struct{ data *strings.Reader }

// Read serves the data, then a failure.
func (f failingReaderAfterSize) Read(p []byte) (int, error) {
	if f.data.Len() == 0 {
		return 0, errors.New("stream reset")
	}
	return f.data.Read(p)
}

// TestExactReader_ProbeReportsAFailedEnd covers a source that fails
// where it should have ended.
func TestExactReader_ProbeReportsAFailedEnd(t *testing.T) {
	_, err := io.ReadAll(filexfer.ExactReader(failingReaderAfterSize{strings.NewReader("ab")}, 2))
	if err == nil || !strings.Contains(err.Error(), "stream reset") {
		t.Fatalf("ReadAll() error = %v, want the source's failure at its end", err)
	}
}

// TestLimitWriter_Table covers the backstop Get uses for a file that
// grows while it is read.
func TestLimitWriter_Table(t *testing.T) {
	var got bytes.Buffer
	w := filexfer.LimitWriter(&got, 5)
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first Write() = %d, %v", n, err)
	}
	n, err := w.Write([]byte("defg"))
	if n != 2 || !errors.Is(err, filexfer.ErrLimitExceeded) {
		t.Fatalf("crossing Write() = %d, %v; want 2 and ErrLimitExceeded", n, err)
	}
	if got.String() != "abcde" {
		t.Errorf("wrote %q, want exactly the first 5 bytes", got.String())
	}
	if n, err := w.Write([]byte("h")); n != 0 || !errors.Is(err, filexfer.ErrLimitExceeded) {
		t.Errorf("Write() past the limit = %d, %v; want 0 and ErrLimitExceeded", n, err)
	}
}

// failingWriter fails every write.
type failingWriter struct{}

// Write always fails.
func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

// TestLimitWriter_PassesTheSinkFailureThrough keeps the sink's own error
// ahead of the limit's.
func TestLimitWriter_PassesTheSinkFailureThrough(t *testing.T) {
	for _, data := range []string{"ab", "abcdefgh"} {
		_, err := filexfer.LimitWriter(failingWriter{}, 4).Write([]byte(data))
		if err == nil || !strings.Contains(err.Error(), "pipe closed") {
			t.Errorf("Write(%q) error = %v, want the sink's own failure", data, err)
		}
	}
}

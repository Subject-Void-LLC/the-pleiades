package filters_test

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestGzipCompress(t *testing.T) {
	t.Run("over_cap", func(t *testing.T) {
		if got := filters.GzipCompress(strings.Repeat("a", filters.MaxStructuredInputBytes+1)); got != nil {
			t.Errorf("GzipCompress(over cap) = %v, want nil", got)
		}
	})

	t.Run("output_is_a_real_gzip_stream", func(t *testing.T) {
		compressed := filters.GzipCompress("hello, world")
		r, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		var out bytes.Buffer
		if _, err := out.ReadFrom(r); err != nil {
			t.Fatalf("reading decompressed stream: %v", err)
		}
		if out.String() != "hello, world" {
			t.Errorf("decompressed = %q, want %q", out.String(), "hello, world")
		}
	})
}

func TestGzipDecompress(t *testing.T) {
	realGzip := func(s string) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return buf.Bytes()
	}

	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"valid_stream", realGzip("hello"), "hello"},
		{"valid_empty_content", realGzip(""), ""},
		{"not_gzip_at_all", []byte("plain text, not gzip"), ""},
		{"truncated_stream", realGzip("hello world this is a real message")[:5], ""},
		{"empty_input", []byte{}, ""},
		{"over_cap", bytes.Repeat([]byte("a"), filters.MaxStructuredInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.GzipDecompress(tc.in); got != tc.want {
				t.Errorf("GzipDecompress(...) = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("decompression_bomb_refused", func(t *testing.T) {
		// A highly compressible payload well over maxGzipDecompressedBytes
		// (16 MiB) once inflated, from an input safely under
		// MaxStructuredInputBytes (1 MiB) compressed -- the exact shape
		// this phase's own Schema/Injection Hardening finding exists to
		// refuse.
		bomb := realGzip(strings.Repeat("a", 64<<20)) // 64 MiB of one repeated byte
		if len(bomb) > filters.MaxStructuredInputBytes {
			t.Fatalf("test setup: compressed bomb is %d bytes, expected well under MaxStructuredInputBytes so the input-length cap is not what is being tested here", len(bomb))
		}
		if got := filters.GzipDecompress(bomb); got != "" {
			t.Errorf("GzipDecompress(bomb) = %d bytes, want \"\" (refused)", len(got))
		}
	})
}

// TestGzipRoundTrip is this phase's own Adversarial Pattern Justification
// requirement: prove GzipCompress/GzipDecompress round-trip exactly for a
// representative payload, including one at this Part's own input-length
// cap boundary (MaxStructuredInputBytes, which GzipCompress/GzipDecompress
// reuse rather than declaring a new document-shaped bound of their own).
func TestGzipRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"short_ascii", "the quick brown fox"},
		{"unicode", "syslog: café, naïve, 日本語"},
		{"binary_looking_bytes", "\x00\x01\x02\xff\xfe line\nbreaks\ttabs"},
		{"at_cap_boundary", strings.Repeat("x", filters.MaxStructuredInputBytes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compressed := filters.GzipCompress(tc.content)
			if compressed == nil && tc.content != "" {
				t.Fatalf("GzipCompress(%d bytes) returned nil unexpectedly", len(tc.content))
			}
			got := filters.GzipDecompress(compressed)
			if got != tc.content {
				t.Errorf("round trip mismatch: got %d bytes, want %d bytes", len(got), len(tc.content))
			}
		})
	}
}

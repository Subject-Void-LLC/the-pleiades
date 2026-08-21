package filters

import (
	"bytes"
	"compress/gzip"
	"io"
)

// maxGzipDecompressedBytes bounds GzipDecompress's own output, separate
// from and larger than MaxStructuredInputBytes' bound on its *input*.
// This is a real, verified-not-assumed finding from this phase's own
// Schema/Injection Hardening audit, recorded in FAILURE_PATTERNS.md
// entry 163: a naive io.ReadAll(gzip.Reader) has no cap of its own, so a
// small, well-within-MaxStructuredInputBytes compressed input can still
// expand into an arbitrarily large allocation (a decompression bomb) -- gzip's
// own format allows extreme compression ratios for pathological input
// (a long run of one repeated byte), nothing like the modest ratios
// real log or config text produces. 16 MiB leaves generous headroom
// over any ratio realistic text content achieves against a 1 MiB
// (MaxStructuredInputBytes) compressed input, while still refusing to
// allocate without bound for a crafted one.
const maxGzipDecompressedBytes = 16 << 20 // 16 MiB

// GzipCompress compresses content with gzip (stdlib compress/gzip, an
// in-memory bytes.Buffer only -- no temp file, no subprocess) and
// returns the raw compressed bytes. Returns nil if content exceeds
// MaxStructuredInputBytes (reused rather than MaxInputBytes: content
// here is arbitrary file/log text, not a flat scalar, the same
// document-shaped reasoning MaxStructuredInputBytes' own doc comment
// states).
//
// The result is CEL bytes, not a string: gzip's compressed output is
// arbitrary binary data, not necessarily valid UTF-8, and cel-go's own
// StringType requires valid UTF-8 while its BytesType does not -- a
// string return here would be silently wrong for the vast majority of
// real compressed payloads.
func GzipCompress(content string) []byte {
	if len(content) > MaxStructuredInputBytes {
		return nil
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	// Neither Write nor Close can return an error here: both write only
	// to an in-memory bytes.Buffer, whose own Write's doc comment states
	// plainly "err is always nil" -- its only failure mode, ErrTooLarge,
	// is a panic, not a returned error, and needs an allocation past
	// Go's own maximum slice length, unreachable for a
	// MaxStructuredInputBytes-bounded input compressing to something
	// smaller or comparable in size. Verified directly against both
	// bytes.Buffer.Write's and compress/gzip's Writer.Write/Close source
	// (the latter two simply forward to the underlying flate.Writer,
	// which forwards to buf.Write) before relying on this rather than
	// assuming it.
	_, _ = w.Write([]byte(content))
	_ = w.Close()
	return buf.Bytes()
}

// GzipDecompress reverses GzipCompress: decompresses data (stdlib
// compress/gzip, in-memory only) back to its original string content.
// Returns "" if data exceeds MaxStructuredInputBytes, is not a valid
// gzip stream, or decompresses to more than maxGzipDecompressedBytes --
// see that constant's own doc comment for why the output needs its own
// separate cap, not just one on data's own input length.
func GzipDecompress(data []byte) string {
	if len(data) > MaxStructuredInputBytes {
		return ""
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	defer func() { _ = r.Close() }()

	limited := io.LimitReader(r, maxGzipDecompressedBytes+1)
	out, err := io.ReadAll(limited)
	if err != nil {
		return ""
	}
	if len(out) > maxGzipDecompressedBytes {
		return ""
	}
	return string(out)
}

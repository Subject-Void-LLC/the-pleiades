// The stream helpers every Store copies through, so "exactly size bytes or
// nothing" and a Get's limit are enforced once.
package filexfer

import (
	"errors"
	"fmt"
	"io"
)

// SizedReader yields exactly the number of bytes a Put declared, then
// io.EOF, and fails with ErrSizeMismatch if its source ends early or
// still has bytes left over. Every Store copies from one, so "exactly
// size bytes or nothing" is enforced by the stream itself rather than
// re-implemented per protocol, and the failure arrives while the
// content is still going to a temporary file nothing reads.
type SizedReader struct {
	r      io.Reader
	size   int64
	remain int64

	// ended records that the source has been confirmed to end exactly
	// at size, so the one-byte probe for leftover data runs once.
	ended bool
}

// ExactReader wraps r to yield exactly size bytes. r must end (return
// io.EOF) after size bytes; a source that is part of a longer stream
// should be wrapped in io.LimitReader first.
func ExactReader(r io.Reader, size int64) *SizedReader {
	return &SizedReader{r: r, size: size, remain: size}
}

// Size returns the declared size. Its presence also lets a protocol
// library that sizes its pipelining by the source (pkg/sftp's
// File.ReadFrom does) do so.
func (s *SizedReader) Size() int64 { return s.size }

// Read reads up to the declared size, then probes the source once for a
// leftover byte.
func (s *SizedReader) Read(p []byte) (int, error) {
	if s.remain == 0 {
		return 0, s.probeEnd()
	}
	if int64(len(p)) > s.remain {
		p = p[:s.remain]
	}
	n, err := s.r.Read(p)
	s.remain -= int64(n)
	switch {
	case errors.Is(err, io.EOF) && s.remain > 0:
		return n, fmt.Errorf("%w: source ended %d bytes short of the declared %d",
			ErrSizeMismatch, s.remain, s.size)
	case errors.Is(err, io.EOF):
		s.ended = true
		return n, io.EOF
	}
	return n, err
}

// probeEnd confirms the source has nothing past the declared size. It
// reads at most one byte, so a source that does have more costs one
// byte of reading to detect, not the rest of the stream.
func (s *SizedReader) probeEnd() error {
	if s.ended {
		return io.EOF
	}
	var one [1]byte
	for {
		n, err := s.r.Read(one[:])
		if n > 0 {
			return fmt.Errorf("%w: source has more than the declared %d bytes", ErrSizeMismatch, s.size)
		}
		if errors.Is(err, io.EOF) {
			s.ended = true
			return io.EOF
		}
		if err != nil {
			return err
		}
		// A zero-byte read with no error is legal and means "try
		// again"; io.Reader's contract discourages it but permits it.
	}
}

// LimitWriter passes at most limit bytes to w and fails with
// ErrLimitExceeded on the first byte past it, writing none of the bytes
// of the write that crossed it beyond the limit. It is Get's backstop
// for a file that grows while it is being read, or a protocol that does
// not announce the size up front.
func LimitWriter(w io.Writer, limit int64) io.Writer {
	return &limitWriter{w: w, remain: limit, limit: limit}
}

// limitWriter is LimitWriter's implementation.
type limitWriter struct {
	w      io.Writer
	remain int64
	limit  int64
}

// Write writes p, or the part of it that fits under the limit followed
// by ErrLimitExceeded.
func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) <= l.remain {
		n, err := l.w.Write(p)
		l.remain -= int64(n)
		return n, err
	}
	n, err := l.w.Write(p[:l.remain])
	l.remain -= int64(n)
	if err != nil {
		return n, err
	}
	return n, fmt.Errorf("%w: more than %d bytes", ErrLimitExceeded, l.limit)
}

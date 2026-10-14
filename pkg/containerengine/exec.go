// Exec: one command run inside one container, attached, with its output
// bounded and its real exit code.
package containerengine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/moby/moby/client"
)

// DefaultMaxExecOutput bounds the standard output and error one Exec keeps,
// together: 1 MiB, the order of magnitude pkg/sdk's structured-input caps
// use for document-shaped content.
const DefaultMaxExecOutput = 1 << 20

// ExecOptions configures one Exec.
type ExecOptions struct {
	// MaxOutputBytes bounds the combined standard output and error kept
	// from the command, refused rather than truncated past it, so a command
	// that writes forever cannot exhaust memory. Zero takes
	// DefaultMaxExecOutput.
	MaxOutputBytes int
}

// ExecResult is what running one command inside one container produced.
type ExecResult struct {
	// Stdout is everything the command wrote to its standard output.
	Stdout string
	// Stderr is everything the command wrote to its standard error.
	Stderr string
	// ExitCode is the command's real exit status, read from the engine's
	// exec inspection once its output stream ends.
	ExitCode int
}

// namePattern is what a container name or ID, or an exec ID, may look like
// before it is put into a request path: the engine's own name grammar,
// which also cannot reshape a path (no slash, "?", "#", "%", whitespace or
// NUL). The guard refuses a reshaped request anyway; this refuses it with a
// message that names the value, before the engine is asked anything.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidName reports whether name, which a caller is about to put into a
// request path as a kind (container, exec, network, volume), has the shape
// every engine gives its names and IDs.
func ValidName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("containerengine: the %s name is empty", kind)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("containerengine: %s name %q holds a character no engine allows in a name", kind, name)
	}
	return nil
}

// ExecOps are the operations Exec sends; a session that calls it must be
// opened with them.
var ExecOps = []Op{OpExecCreate, OpExecStart, OpExecInspect}

// Exec runs command inside container through the container's own
// `/bin/sh -c`, attached, and reports its output and real exit code.
//
// The command is one argument to the shell, never split or expanded here,
// so it behaves as it would in any other transport's remote shell; this
// assumes the image has /bin/sh, as essentially every Linux image does.
// Cancelling ctx closes the output stream at once rather than waiting for
// the command to finish writing.
func (s *Session) Exec(ctx context.Context, container, command string, opts ExecOptions) (ExecResult, error) {
	if err := ValidName("container", container); err != nil {
		return ExecResult{}, err
	}
	limit := opts.MaxOutputBytes
	if limit <= 0 {
		limit = DefaultMaxExecOutput
	}

	created, err := s.API.ExecCreate(ctx, container, client.ExecCreateOptions{
		Cmd:          []string{"/bin/sh", "-c", command},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("containerengine: creating an exec in %q: %w", container, err)
	}
	// The engine's answer becomes the next request's path, so it is held to
	// the same shape as a caller's.
	if err := ValidName("exec", created.ID); err != nil {
		return ExecResult{}, fmt.Errorf("containerengine: the engine answered an unusable exec ID: %w", err)
	}

	attached, err := s.API.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("containerengine: starting exec %s: %w", created.ID, err)
	}
	stop := closeOnDone(ctx, attached.Conn)
	var stdout, stderr []byte
	// Without a terminal an engine frames the two streams; an engine that
	// says it sent a raw stream sent one stream unframed, which read as
	// frames would be garbage.
	if media, ok := attached.MediaType(); ok && media == rawStream {
		stdout, err = readBounded(attached.Reader, limit)
	} else {
		stdout, stderr, err = demux(attached.Reader, limit)
	}
	stop()
	attached.Close()
	if err != nil {
		if ctx.Err() != nil {
			return ExecResult{}, fmt.Errorf("containerengine: exec %s: %w", created.ID, ctx.Err())
		}
		return ExecResult{}, fmt.Errorf("containerengine: reading exec %s's output: %w", created.ID, err)
	}

	inspected, err := s.API.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("containerengine: inspecting exec %s: %w", created.ID, err)
	}
	return ExecResult{Stdout: string(stdout), Stderr: string(stderr), ExitCode: inspected.ExitCode}, nil
}

// closeOnDone closes c the moment ctx is done, which unblocks a read waiting
// on the engine, and returns the function that stops watching.
func closeOnDone(ctx context.Context, c io.Closer) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// rawStream is the media type an engine gives an attached stream it did not
// frame.
const rawStream = "application/vnd.docker.raw-stream"

// readBounded reads r to its end, refusing past limit bytes.
func readBounded(r io.Reader, limit int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("the output exceeded %d bytes", limit)
	}
	return b, nil
}

// demux reads an attached stream's frames until the engine ends it: an
// 8-byte header (the stream, 1 for output and 2 for error, three reserved
// bytes, then a big-endian payload length) and that many bytes, repeated.
// The end of the stream is the command's end, not a guess from a quiet
// period. Past limit bytes kept, it refuses rather than truncates.
func demux(r io.Reader, limit int) (stdout, stderr []byte, err error) {
	header := make([]byte, 8)
	for {
		n, err := io.ReadFull(r, header)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return stdout, stderr, nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return stdout, stderr, fmt.Errorf("the stream ended mid-frame after %d of %d header bytes", n, len(header))
			}
			return stdout, stderr, fmt.Errorf("reading a frame header: %w", err)
		}
		size := binary.BigEndian.Uint32(header[4:8])
		// Checked against what is left BEFORE allocating: the length is read
		// off the wire, and allocating first would let one frame claiming
		// nearly 4 GiB force that allocation whatever the limit says.
		if int64(len(stdout))+int64(len(stderr))+int64(size) > int64(limit) {
			return nil, nil, fmt.Errorf("the output exceeded %d bytes", limit)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return stdout, stderr, fmt.Errorf("reading a %d-byte frame: %w", size, err)
		}
		switch header[0] {
		case 1:
			stdout = append(stdout, payload...)
		case 2:
			stderr = append(stderr, payload...)
		}
	}
}

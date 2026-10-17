// A created container run to its exit, its output kept, bounded.
package containerengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// RunToExitOps are the operations RunToExit sends.
var RunToExitOps = []Op{OpContainerAttach, OpContainerStart, OpContainerWait, OpContainerKill}

// Exited is how a container run to its exit ended.
type Exited struct {
	// ExitCode is its exit status; 137 when it was killed for timing out.
	ExitCode int
	// TimedOut reports that it outlived its timeout and was killed.
	TimedOut bool
	// Stdout and Stderr are what it wrote, together at most the limit given.
	Stdout, Stderr string
}

// RunToExit starts the created container id, attached, waits up to timeout
// for it to exit, and returns how it ended with what it wrote. Output past
// limit bytes, together, is an error, never cut short. A container still
// running at timeout is killed and reported TimedOut; a ctx that ends
// kills it too and returns ctx's error. The caller removes the container.
func (s *Session) RunToExit(ctx context.Context, id string, tty bool, timeout time.Duration, limit int) (Exited, error) {
	var res Exited
	if err := ValidName("container", id); err != nil {
		return res, err
	}
	attached, err := s.API.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdout: true, Stderr: true})
	if err != nil {
		return res, fmt.Errorf("attaching to container %s: %w", id, err)
	}
	defer attached.Close()
	type output struct {
		stdout, stderr []byte
		err            error
	}
	read := make(chan output, 1)
	go func() {
		if media, ok := attached.MediaType(); tty || (ok && media == rawStream) {
			buf := &boundedBuffer{limit: limit}
			_, err := buf.ReadFrom(attached.Reader)
			read <- output{stdout: buf.Bytes(), err: err}
			return
		}
		stdout, stderr, err := demux(attached.Reader, limit)
		read <- output{stdout: stdout, stderr: stderr, err: err}
	}()
	if _, err := s.API.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return res, fmt.Errorf("starting container %s: %w", id, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	waited := s.API.ContainerWait(runCtx, id, client.ContainerWaitOptions{Condition: moby.WaitConditionNotRunning})
	var out *output
	for exited := false; !exited; {
		select {
		case o := <-read:
			if o.err != nil {
				// Output past the limit, or a broken stream: nothing reads
				// the container's output now, so it would block writing
				// until its timeout. It is stopped at once instead.
				s.kill(id)
				return res, fmt.Errorf("reading container %s's output: %w; it was killed", id, o.err)
			}
			out = &o
			read = nil
		case done := <-waited.Result:
			res.ExitCode = int(done.StatusCode)
			if done.Error != nil && done.Error.Message != "" {
				return res, fmt.Errorf("waiting for container %s: %s", id, done.Error.Message)
			}
			exited = true
		case err := <-waited.Error:
			if runCtx.Err() == nil {
				return res, fmt.Errorf("waiting for container %s: %w", id, err)
			}
			s.kill(id)
			if ctx.Err() != nil {
				return res, fmt.Errorf("container %s: %w", id, ctx.Err())
			}
			res.TimedOut, res.ExitCode = true, 137
			exited = true
		}
	}
	// The stream ends when the container does, a killed one included.
	if out == nil {
		select {
		case o := <-read:
			if o.err != nil {
				return res, fmt.Errorf("reading container %s's output: %w", id, o.err)
			}
			out = &o
		case <-time.After(removeGrace):
			return res, fmt.Errorf("container %s exited but its output never ended", id)
		}
	}
	res.Stdout, res.Stderr = string(out.stdout), string(out.stderr)
	return res, nil
}

// boundedBuffer keeps what is written to it up to limit bytes, and is an
// error past it.
type boundedBuffer struct {
	buf   []byte
	limit int
}

// ReadFrom reads r to its end into the buffer, refusing past the limit.
func (b *boundedBuffer) ReadFrom(r io.Reader) (int64, error) {
	chunk := make([]byte, 32<<10)
	var total int64
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			if len(b.buf)+n > b.limit {
				return total, fmt.Errorf("the output is over %d bytes", b.limit)
			}
			b.buf = append(b.buf, chunk[:n]...)
			total += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}

// Bytes is what the buffer holds.
func (b *boundedBuffer) Bytes() []byte { return b.buf }

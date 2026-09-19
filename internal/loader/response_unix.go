//go:build unix

// Package loader: the response channel, the pipe a program writes its one
// response frame to as file descriptor 3.
package loader

import (
	"io"
	"os"
	"sync"
	"time"
)

// responseRead is what the response channel's reader collected.
type responseRead struct {
	// data is everything read, or nil when oversize.
	data []byte

	// oversize reports that the program wrote more than the cap.
	oversize bool

	// err is the read's own error. It is os.ErrClosed, wrapped, when the
	// channel was closed on a reader still waiting for end of file.
	err error
}

// responseChannel is the parent's end of the pipe a program writes its
// response frame to. The reader runs from the moment the channel opens,
// concurrently with the program, so a program writing a frame bigger than
// the pipe's buffer never blocks on a parent that has not started reading
// yet.
type responseChannel struct {
	read      *os.File
	closeOnce sync.Once
	done      chan responseRead
}

// openResponseChannel opens the pipe, starts its reader, and returns the
// channel plus the write end to hand the program as ExtraFiles[0]. The
// caller must close the write end once the program has started (or failed
// to), and must call collect exactly once.
func openResponseChannel(limit int64) (*responseChannel, *os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	c := &responseChannel{read: r, done: make(chan responseRead, 1)}
	go c.readAll(limit)
	return c, w, nil
}

// readAll reads until end of file or until more than limit bytes arrive.
//
// Past the limit it closes the channel rather than draining it. Draining
// would let a program that writes forever keep this process busy until
// the timeout; closing makes the program's next write fail at once, which
// is the fastest honest end to a response that is already refused.
func (c *responseChannel) readAll(limit int64) {
	data, err := io.ReadAll(io.LimitReader(c.read, limit+1))
	res := responseRead{data: data, err: err}
	if int64(len(data)) > limit {
		res = responseRead{oversize: true}
		c.close()
	}
	c.done <- res
}

// close closes the read end, once, however many callers ask.
func (c *responseChannel) close() {
	c.closeOnce.Do(func() { _ = c.read.Close() })
}

// collect waits for the reader and returns what it read, called once the
// program has exited.
//
// End of file needs every copy of the write end closed, and a background
// process the program started may have inherited one and kept it. So the
// reader gets grace to finish, and then the channel is closed under it,
// which ends its read at once. heldOpen reports that this happened, and
// data then holds whatever arrived before it did. The wait is bounded
// either way, and the reader always finishes, so no goroutine outlives
// the call.
func (c *responseChannel) collect(grace time.Duration) (res responseRead, heldOpen bool) {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case res = <-c.done:
	case <-timer.C:
		c.close()
		res = <-c.done
		heldOpen = true
	}
	c.close()
	return res, heldOpen
}

// Package cml: waiting for a stopped capture's file to be whole. CML 2.7
// writes a capture's file behind the capture itself. Measured on a real
// controller (2026-11-17): a link capture stopped at 25,593 packets was
// served five seconds later as 12,461 packets (4,455,644 bytes) and four
// minutes later as all 25,593 (9,147,421 bytes), growing at about 20 KB a
// second in between; a fetch while it grew came back short, or was reset
// partway (INTERNAL_ERROR) when it grew during the download, or changed
// length between two attempts.
package cml

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultAwaitPoll is how long AwaitPCAP waits between two reads of the
// file's length unless Await.Poll says otherwise.
const DefaultAwaitPoll = 5 * time.Second

// steadyPolls is how many polls in a row the file's length must hold still
// when the capture's packet count is not known, the length then being all
// that says the writer is done: three, so a writer pausing for less than
// two polls (ten seconds by default) is not taken for one that finished.
// Measured on CML 2.7 (2026-11-17, a capture stopping itself at 15,000
// packets, its length read every second): the writer wrote for 63 seconds
// after the last packet at about 70 KB a second and never paused, and the
// capture reported itself running until the file was whole.
const steadyPolls = 3

// Await is how AwaitPCAP waits.
type Await struct {
	// Packets is how many packets the capture reported taken just before
	// it was stopped, or 0 when that is not known (it had stopped at its
	// own bound before the task). A whole file holds at least that many:
	// more can arrive between that read and the stop.
	Packets int
	// Poll is the time between two reads of the file's length; zero is
	// DefaultAwaitPoll.
	Poll time.Duration
}

// afterPoll, when set, runs at the end of each poll, before the wait for
// the next, so a test can end a wait at a known point rather than on a
// deadline. Nothing but a test sets it.
var afterPoll func(poll int)

// AwaitPCAP waits until the controller has written the capture file key
// names whole, then returns it, at most max bytes. The file's length is
// read on every poll (pcapLength, which downloads nothing when the
// controller answers a range) and the file is fetched only once that
// length has held still: across one poll when w.Packets is known, across
// steadyPolls when it is not. A fetched file is taken when its last record
// is complete, it is as long as its length said, and it holds at least
// w.Packets records; one short of them is not fetched again until its
// length changes. A fetch the controller cuts off, or whose length changes
// between attempts, is a file still being written. ctx bounds the wait;
// when it ends first, the error says how far the file had got and how the
// last fetch ended.
func (c *Client) AwaitPCAP(ctx context.Context, key string, max int64, w Await) ([]byte, error) {
	interval := w.Poll
	if interval <= 0 {
		interval = DefaultAwaitPoll
	}
	steady := steadyPolls
	if w.Packets > 0 {
		steady = 1
	}
	probe := &lengthProbe{c: c, key: key, max: max}
	st := awaitState{last: -1, fetchedSize: -1, packets: w.Packets}
	for poll := 1; ; poll++ {
		data, err := st.poll(ctx, c, probe, key, max, steady)
		if data != nil || err != nil {
			return data, err
		}
		if afterPoll != nil {
			afterPoll(poll)
		}
		if ctx.Err() != nil {
			return nil, st.awaitError(ctx)
		}
		select {
		case <-ctx.Done():
			return nil, st.awaitError(ctx)
		case <-time.After(interval):
		}
	}
}

// awaitState is what AwaitPCAP knows of the file between polls.
type awaitState struct {
	// last is the length the last poll read, -1 before one did; still
	// counts the polls in a row it has held.
	last  int64
	still int
	// data is the last file fetched whole, fetchedSize its length (-1
	// before one was) and count the records it held. A file short of its
	// packets is judged again from data while its length holds, not
	// fetched again.
	data        []byte
	fetchedSize int64
	count       int
	// packets is the count a whole file holds at least, 0 when not known.
	packets int
	// lastErr is how the last fetch ended when it did not bring the file.
	lastErr error
}

// poll reads the file's length once and, when it has held still for
// steady polls, judges the file at that length: the one fetched at it
// already when there is one, else a new fetch. It returns the file when it
// is whole, an error that ends the wait, or neither.
func (s *awaitState) poll(ctx context.Context, c *Client, probe *lengthProbe, key string, max int64, steady int) ([]byte, error) {
	size, err := probe.length(ctx)
	if err != nil {
		return nil, s.ended(ctx, err)
	}
	if size < 0 {
		// The controller states no length: it is read by fetching.
		data, err := c.FetchPCAP(ctx, key, max)
		if err != nil {
			return nil, s.fetchFailed(ctx, err)
		}
		s.keep(data)
		size = int64(len(data))
	}
	if size == s.last {
		s.still++
	} else {
		s.still = 0
	}
	s.last = size
	if s.still < steady {
		return nil, nil
	}
	if size != s.fetchedSize {
		// Not fetched at this length yet.
		data, err := c.FetchPCAP(ctx, key, max)
		if err != nil {
			return nil, s.fetchFailed(ctx, err)
		}
		s.keep(data)
	}
	n, whole := countRecords(s.data)
	if whole && n >= s.packets && s.fetchedSize == size {
		return s.data, nil
	}
	return nil, nil
}

// keep records a file fetched whole: its bytes, length and records.
func (s *awaitState) keep(data []byte) {
	s.data, s.fetchedSize, s.lastErr = data, int64(len(data)), nil
	s.count, _ = countRecords(data)
}

// fetchFailed sorts a fetch's failure: a file still being written (cut
// off, or changed between attempts) is waited on, a wait that ended says
// so, and anything else ends the wait.
func (s *awaitState) fetchFailed(ctx context.Context, err error) error {
	if errors.Is(err, ErrCaptureChanged) || cutOff(err) {
		s.lastErr, s.still = err, 0
		return nil
	}
	return s.ended(ctx, err)
}

// ended is err, or the wait's own end when ctx ended it.
func (s *awaitState) ended(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return s.awaitError(ctx)
	}
	return err
}

// awaitError says how far the file had got when ctx ended a wait for it,
// and how the last fetch ended when it did not bring the file, so a reset
// on every fetch is not mistaken for a slow writer.
func (s *awaitState) awaitError(ctx context.Context) error {
	got := "no length read yet"
	if s.last >= 0 {
		got = fmt.Sprintf("it stated %d bytes", s.last)
	}
	if s.fetchedSize >= 0 {
		got += fmt.Sprintf(" and the last fetch held %d packets", s.count)
	}
	if s.packets > 0 {
		got += fmt.Sprintf(" of the %d the capture took", s.packets)
	}
	if s.lastErr != nil {
		got += fmt.Sprintf("; the last fetch ended: %v", s.lastErr)
	}
	return fmt.Errorf("cml: the controller had not finished writing the capture file (%s): %w", got, ctx.Err())
}

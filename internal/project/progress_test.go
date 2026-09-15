// This file covers the live clone output: that a watcher receives lines as
// they are written, that one arriving mid-clone sees what it missed, that a
// stream ends when the clone does, and that a new clone does not hand a
// reader the previous attempt's output as though it were live.
package project_test

import (
	"io"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// collect drains a subscription until it closes, or fails the test if that
// takes longer than a clone ever should in a unit test.
func collect(t *testing.T, ch <-chan string) []string {
	t.Helper()
	var got []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, line)
		case <-deadline:
			t.Fatalf("the stream did not close; collected %v so far", got)
			return got
		}
	}
}

func TestProgress_AWatcherReceivesLinesAndThenTheClose(t *testing.T) {
	p := project.NewProgress()
	w := p.Writer(7)

	sub, cancel := p.Subscribe(7)
	defer cancel()

	_, _ = io.WriteString(w, "Counting objects: 12\n")
	_, _ = io.WriteString(w, "Resolving deltas: 100%\n")
	p.Finish(7)

	got := collect(t, sub)
	if len(got) != 2 || got[0] != "Counting objects: 12" || got[1] != "Resolving deltas: 100%" {
		t.Errorf("watcher received %v, want both lines in order", got)
	}
}

// TestProgress_SplitsOnCarriageReturns is the one that decides whether a
// clone appears to do anything at all: git rewrites an in-place counter with
// \r rather than ending the line, so a splitter that only knew \n would show
// nothing until the transfer finished.
func TestProgress_SplitsOnCarriageReturns(t *testing.T) {
	p := project.NewProgress()
	w := p.Writer(7)

	sub, cancel := p.Subscribe(7)
	defer cancel()

	_, _ = io.WriteString(w, "Receiving objects:  10%\rReceiving objects:  50%\rReceiving objects: 100%\n")
	p.Finish(7)

	got := collect(t, sub)
	if len(got) != 3 {
		t.Fatalf("received %v, want each rewritten counter as its own line", got)
	}
	if got[2] != "Receiving objects: 100%" {
		t.Errorf("last line = %q, want the final counter", got[2])
	}
}

// TestProgress_ALateWatcherSeesWhatItMissed covers opening the page partway
// through a clone: an empty page would look like an outage rather than like
// arriving late.
func TestProgress_ALateWatcherSeesWhatItMissed(t *testing.T) {
	p := project.NewProgress()
	w := p.Writer(7)

	_, _ = io.WriteString(w, "cloning into /var/lib/pleiades/projects/7\n")
	_, _ = io.WriteString(w, "Counting objects: 12\n")

	sub, cancel := p.Subscribe(7)
	defer cancel()
	_, _ = io.WriteString(w, "done\n")
	p.Finish(7)

	got := collect(t, sub)
	if len(got) != 3 || got[0] != "cloning into /var/lib/pleiades/projects/7" {
		t.Errorf("late watcher received %v, want the replay followed by the live line", got)
	}
}

// TestProgress_AFinishedCloneReplaysAndClosesAtOnce is what a reader opening
// the page after a sync finished should get: the tail of what happened, not
// a page that hangs waiting for output that will never come.
func TestProgress_AFinishedCloneReplaysAndClosesAtOnce(t *testing.T) {
	p := project.NewProgress()
	w := p.Writer(7)
	_, _ = io.WriteString(w, "done\n")
	p.Finish(7)

	sub, cancel := p.Subscribe(7)
	defer cancel()

	got := collect(t, sub)
	if len(got) != 1 || got[0] != "done" {
		t.Errorf("watcher of a finished clone received %v, want the tail", got)
	}
}

// TestProgress_ANewCloneStartsAWatcherFresh proves the previous attempt's
// output is not served as though it were this one's, and that anyone
// watching the old attempt is released rather than left hanging.
func TestProgress_ANewCloneStartsAWatcherFresh(t *testing.T) {
	p := project.NewProgress()

	first := p.Writer(7)
	_, _ = io.WriteString(first, "first attempt\n")
	watchingOld, cancelOld := p.Subscribe(7)
	defer cancelOld()

	// A second clone of the same project replaces the stream.
	second := p.Writer(7)
	_, _ = io.WriteString(second, "second attempt\n")
	p.Finish(7)

	// The old watcher was released rather than left waiting forever.
	old := collect(t, watchingOld)
	for _, line := range old {
		if line == "second attempt" {
			t.Error("a watcher of the previous clone was fed the next one's output")
		}
	}

	fresh, cancelFresh := p.Subscribe(7)
	defer cancelFresh()
	got := collect(t, fresh)
	for _, line := range got {
		if line == "first attempt" {
			t.Errorf("a watcher of the new clone was shown the previous attempt's output: %v", got)
		}
	}
}

// TestProgress_ASlowWatcherNeverStallsTheClone is the property that keeps a
// browser from holding up a fetch: a reader that cannot keep up loses lines
// rather than blocking the writer.
func TestProgress_ASlowWatcherNeverStallsTheClone(t *testing.T) {
	p := project.NewProgress()
	w := p.Writer(7)

	sub, cancel := p.Subscribe(7)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more than any subscriber buffer holds, written by a producer
		// nobody is draining.
		for i := 0; i < 5000; i++ {
			_, _ = io.WriteString(w, "line\n")
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writing progress blocked on a watcher that was not reading")
	}
	_ = sub
}

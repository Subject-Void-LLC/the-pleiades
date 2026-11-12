// Failure-mode tests of the CML client against a controller that misbehaves
// partway through: one that stops answering, and one whose answer never
// ends. Each runs against cmltest's controller over real TLS, through
// ClientFor, the path import-cml --controller and the virt.cml methods take.
package cml

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// within runs f and fails the test unless it returns inside limit: a bound
// that was taken away shows as a call still running, not as a hung test.
func within(t *testing.T, limit time.Duration, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("the call was still running after %s: nothing bounds it", limit)
		return nil
	}
}

// TestClientFor_AControllerThatStopsAnsweringIsCutOff: a controller that
// accepts a call and then sends nothing, or sends its headers and part of
// its answer and then nothing more, is cut off at requestTimeout, so it
// cannot hold a run open. requestTimeout is shortened here; it is a minute.
func TestClientFor_AControllerThatStopsAnsweringIsCutOff(t *testing.T) {
	saved := requestTimeout
	requestTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { requestTimeout = saved })
	for name, partway := range map[string]bool{"before its headers": false, "partway through its answer": true} {
		t.Run(name, func(t *testing.T) {
			ctl := cmltest.New(t, "admin", "pw")
			c := connectFor(t, ctl)
			onCall(ctl, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v0/labs" {
					return false
				}
				if partway {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`["00000000-0000-4000-8000-`))
					w.(http.Flusher).Flush()
				}
				// Nothing more, until the client gives up.
				<-r.Context().Done()
				return true
			})
			start := time.Now()
			err := within(t, 10*time.Second, func() error { _, err := c.Labs(context.Background()); return err })
			if err == nil || !strings.Contains(err.Error(), "GET /api/v0/labs") {
				t.Fatalf("Labs from a controller that stopped answering = %v, want an error naming the call", err)
			}
			if took := time.Since(start); took > 10*time.Second {
				t.Errorf("cut off after %s, want about %s", took, requestTimeout)
			}
		})
	}
}

// TestCall_ReadsNoMoreThanItsBound: an answer that does not end (here four
// times maxResponse) is refused once it passes maxResponse, and the client
// stops reading there. What the controller managed to write is the proof:
// with the read unbounded it writes all of it; bounded, it writes the
// bound plus what the sockets between them buffer.
func TestCall_ReadsNoMoreThanItsBound(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connectFor(t, ctl)
	const total = 4 * maxResponse
	wrote := make(chan int, 1)
	onCall(ctl, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v0/labs" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		chunk := []byte("[" + strings.Repeat(" ", 64<<10-1))
		n := 0
		for n < total {
			k, err := w.Write(chunk)
			n += k
			if err != nil {
				break
			}
			chunk[0] = ' '
		}
		wrote <- n
		return true
	})
	err := within(t, 30*time.Second, func() error { _, err := c.Labs(context.Background()); return err })
	if err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("Labs of an answer with no end = %v, want it refused as over the bound", err)
	}
	select {
	case n := <-wrote:
		// The sockets of a loopback connection buffer a few megabytes.
		if slack := 16 << 20; n > maxResponse+slack {
			t.Errorf("the controller wrote %d bytes before the client stopped reading, want at most %d", n, maxResponse+slack)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the controller was still writing: the client never stopped reading")
	}
}

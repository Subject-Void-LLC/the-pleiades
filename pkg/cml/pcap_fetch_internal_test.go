// Tests of a capture's fetch against cmltest's controller, over HTTP/2 as
// a real controller's front end serves it and over HTTP/1.1: a large file
// whole, a stream the controller resets partway fetched again (from the
// start, or from where it stopped when byte ranges are served), the reset
// that recurs on every attempt reported as the controller's own, a length
// over the bound refused before the body is read, the idle bound, and each
// answer that is not the rest of the same file refused or started over.
package cml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// largeCapture is the size of the large capture the tests fetch: about
// what the 22,419 packets the real controller cut off came to.
const largeCapture = 12 << 20

// bigPCAP is a pcap file of size bytes: the global header, then records
// of a 16-byte header and a payload, the last one cut to fit.
func bigPCAP(size int) []byte {
	b := bytes.NewBuffer([]byte{0xd4, 0xc3, 0xb2, 0xa1, 2, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 1, 0, 0, 0})
	for i := 0; b.Len() < size; i++ {
		rec := make([]byte, 16+1000)
		for j := range rec {
			rec[j] = byte(i*31 + j)
		}
		b.Write(rec)
	}
	return b.Bytes()[:size]
}

// quickRetries makes a retry wait no time, for the test's length.
func quickRetries(t *testing.T) {
	old := fetchRetryWait
	fetchRetryWait = time.Millisecond
	t.Cleanup(func() { fetchRetryWait = old })
}

// capturing is a controller (HTTP/2 when h2) holding one link whose
// stopped capture has key k1 and file pcap, and a client logged in to it
// through ClientFor, as a task's is.
func capturing(t *testing.T, h2 bool, pcap []byte) (*cmltest.Controller, *Client) {
	t.Helper()
	quickRetries(t)
	ctl := cmltest.New(t, "admin", "pw")
	if h2 {
		ctl = cmltest.NewHTTP2(t, "admin", "pw")
	}
	ctl.Mu.Lock()
	ctl.Labs["lab1"] = &cmltest.Lab{ID: "lab1", Title: "cap", Nodes: map[string]*cmltest.Node{},
		Links: map[string]*cmltest.Link{"l1": {ID: "l1", Capture: &cmltest.Capture{Key: "k1"}}}}
	ctl.PCAP = pcap
	ctl.Mu.Unlock()
	return ctl, connectFor(t, ctl)
}

// requests are the controller's /pcap requests so far.
func requests(ctl *cmltest.Controller) []string {
	ctl.Mu.Lock()
	defer ctl.Mu.Unlock()
	return append([]string(nil), ctl.PCAPRequests...)
}

// TestFetchPCAP_ALargeCaptureOverHTTP2ComesWhole: a 12 MiB capture, over
// HTTP/2, in one request.
func TestFetchPCAP_ALargeCaptureOverHTTP2ComesWhole(t *testing.T) {
	want := bigPCAP(largeCapture)
	ctl, c := capturing(t, true, want)
	got, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(got), err)
	}
	if r := requests(ctl); len(r) != 1 || !strings.HasPrefix(r[0], "HTTP/2.0") {
		t.Fatalf("requests %q: not one, over HTTP/2", r)
	}
}

// TestFetchPCAP_AStreamResetPartwayIsFetchedAgain: the controller resets
// the stream a quarter of the way in, as a real one did, and the second
// request, from the start since no byte ranges are served, brings the
// whole file.
func TestFetchPCAP_AStreamResetPartwayIsFetchedAgain(t *testing.T) {
	want := bigPCAP(largeCapture)
	ctl, c := capturing(t, true, want)
	ctl.Mu.Lock()
	ctl.PCAPCuts = 1
	ctl.Mu.Unlock()
	got, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(got), err)
	}
	if r := requests(ctl); len(r) != 2 || r[1] != "HTTP/2.0 " {
		t.Fatalf("requests %q: not a second, whole one", r)
	}
}

// TestFetchPCAP_AResetOnEveryAttemptIsTheControllersOwn: a reset on every
// attempt ends the fetch after the last, naming the controller's reset
// as net/http reports it, how far the last attempt got, and the attempts.
func TestFetchPCAP_AResetOnEveryAttemptIsTheControllersOwn(t *testing.T) {
	ctl, c := capturing(t, true, bigPCAP(largeCapture))
	ctl.Mu.Lock()
	ctl.PCAPCuts = fetchAttempts
	ctl.Mu.Unlock()
	_, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	for _, want := range []string{"INTERNAL_ERROR; received from peer", fmt.Sprintf("attempt %d of %d", fetchAttempts, fetchAttempts),
		fmt.Sprintf("of %d bytes", largeCapture)} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the error %v does not say %q", err, want)
		}
	}
	if r := requests(ctl); len(r) != fetchAttempts {
		t.Errorf("%d requests, not %d", len(r), fetchAttempts)
	}
}

// TestFetchPCAP_ResumesWhereTheStreamWasCut: a controller serving byte
// ranges is asked for the rest after each cut, and the pieces make the
// file.
func TestFetchPCAP_ResumesWhereTheStreamWasCut(t *testing.T) {
	want := bigPCAP(largeCapture)
	ctl, c := capturing(t, true, want)
	ctl.Mu.Lock()
	ctl.PCAPRanges, ctl.PCAPCuts = true, 2
	ctl.Mu.Unlock()
	got, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(got), err)
	}
	q := largeCapture / 4
	wantReqs := []string{"HTTP/2.0 ", fmt.Sprintf("HTTP/2.0 bytes=%d-", q), fmt.Sprintf("HTTP/2.0 bytes=%d-", 2*q)}
	if r := requests(ctl); strings.Join(r, "|") != strings.Join(wantReqs, "|") {
		t.Fatalf("requests %q, not %q", r, wantReqs)
	}
}

// TestFetchPCAP_AClosedHTTP1ConnectionIsFetchedAgain: over HTTP/1.1 the
// cut is a connection closed short of the stated length, fetched again.
func TestFetchPCAP_AClosedHTTP1ConnectionIsFetchedAgain(t *testing.T) {
	want := bigPCAP(1 << 20)
	ctl, c := capturing(t, false, want)
	ctl.Mu.Lock()
	ctl.PCAPCuts = 1
	ctl.Mu.Unlock()
	got, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(got), err)
	}
	if r := requests(ctl); len(r) != 2 || !strings.HasPrefix(r[1], "HTTP/1.1") {
		t.Fatalf("requests %q", r)
	}
}

// pcapHandler answers /pcap requests with answer, counting them, and lets
// every other call through.
func pcapHandler(ctl *cmltest.Controller, answer func(n int, w http.ResponseWriter, r *http.Request)) *int {
	n := new(int)
	onCall(ctl, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/pcap/") {
			return false
		}
		*n++
		answer(*n, w, r)
		return true
	})
	return n
}

// TestFetchPCAP_AStatedLengthOverTheBoundIsRefusedFirst: an answer stating
// 1 GiB is refused with its size before its body is read: the controller
// never sends one, and reading it would wait out the idle bound instead.
func TestFetchPCAP_AStatedLengthOverTheBoundIsRefusedFirst(t *testing.T) {
	ctl, c := capturing(t, true, nil)
	n := pcapHandler(ctl, func(_ int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(1<<30))
		_, _ = w.Write([]byte{0xd4, 0xc3, 0xb2, 0xa1})
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	_, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err == nil || !strings.Contains(err.Error(), "the capture is 1073741824 bytes, over the 16777216 this fetch takes") || !strings.Contains(err.Error(), "attempt 1 of") {
		t.Fatalf("FetchPCAP: %v", err)
	}
	if *n != 1 {
		t.Errorf("%d requests", *n)
	}
}

// TestFetchPCAP_AnUnstatedLengthIsRefusedAtTheBound: a file sent with no
// length is read to the bound and no further.
func TestFetchPCAP_AnUnstatedLengthIsRefusedAtTheBound(t *testing.T) {
	ctl, c := capturing(t, false, nil)
	pcapHandler(ctl, func(_ int, w http.ResponseWriter, _ *http.Request) {
		for range 4 {
			_, _ = w.Write(bigPCAP(512 << 10))
			_ = http.NewResponseController(w).Flush()
		}
	})
	_, err := c.FetchPCAP(context.Background(), "k1", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "the capture is over 1048576 bytes") {
		t.Fatalf("FetchPCAP: %v", err)
	}
}

// TestFetchPCAP_TheIdleBoundEndsAStalledFetchOnce: a controller that stops
// sending partway is given up on after the idle bound, which is not a cut
// to fetch again.
func TestFetchPCAP_TheIdleBoundEndsAStalledFetchOnce(t *testing.T) {
	old := fetchIdle
	fetchIdle = 200 * time.Millisecond
	t.Cleanup(func() { fetchIdle = old })
	ctl, c := capturing(t, true, nil)
	n := pcapHandler(ctl, func(_ int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte{0xd4, 0xc3, 0xb2, 0xa1, 0, 0})
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	_, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err == nil || !strings.Contains(err.Error(), "sent nothing for the idle bound (200ms)") || !strings.Contains(err.Error(), "after 6 of 1000 bytes") {
		t.Fatalf("FetchPCAP: %v", err)
	}
	if *n != 1 {
		t.Errorf("a stalled fetch was asked for %d times", *n)
	}
}

// TestFetchPCAP_ADownloadOutlastingOneRequestsBoundIsWhole: a file that
// keeps arriving for longer than the client's per-request timeout (a large
// capture over a slow link) is read to its end; only idle time bounds it.
func TestFetchPCAP_ADownloadOutlastingOneRequestsBoundIsWhole(t *testing.T) {
	old := requestTimeout
	requestTimeout = 300 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	want := bigPCAP(10 << 10)
	ctl, c := capturing(t, true, nil)
	pcapHandler(ctl, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(want)))
		for i := 0; i < len(want); i += 1 << 10 {
			_, _ = w.Write(want[i : i+1<<10])
			_ = http.NewResponseController(w).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	got, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(got), err)
	}
}

// cutAt sends the first k bytes of data as the body of an answer stating
// its full length, then resets the stream.
func cutAt(w http.ResponseWriter, data []byte, k int) {
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data[:k])
	_ = http.NewResponseController(w).Flush()
	panic(http.ErrAbortHandler)
}

// TestFetchPCAP_AFileThatChangesBetweenAttemptsIsRefused: an answer asked
// for again that states another length is another file, never spliced or
// taken in its place.
func TestFetchPCAP_AFileThatChangesBetweenAttemptsIsRefused(t *testing.T) {
	ctl, c := capturing(t, true, nil)
	pcapHandler(ctl, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			cutAt(w, bigPCAP(4000), 100)
		}
		w.Header().Set("Content-Length", "8000")
		_, _ = w.Write(bigPCAP(8000))
	})
	_, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err == nil || !strings.Contains(err.Error(), "the file changed between attempts: its answers stated 4000 bytes and then 8000 bytes") {
		t.Fatalf("FetchPCAP: %v", err)
	}
}

// TestFetchPCAP_AWrongRangeStartsOver: a partial answer that is not the
// rest of the file is set aside, and the fetch starts over from the
// first byte without asking for a range again.
func TestFetchPCAP_AWrongRangeStartsOver(t *testing.T) {
	want := bigPCAP(4000)
	ctl, c := capturing(t, true, nil)
	var ranges []string
	pcapHandler(ctl, func(n int, w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range")+"|"+r.Header.Get("If-Range"))
		switch n {
		case 1:
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"v1"`)
			cutAt(w, want, 1000)
		case 2:
			w.Header().Set("Content-Range", "bytes 0-2999/4000")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(want[:3000])
		default:
			w.Header().Set("Content-Length", strconv.Itoa(len(want)))
			_, _ = w.Write(want)
		}
	})
	got, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("FetchPCAP = %d bytes, %v", len(got), err)
	}
	if strings.Join(ranges, " ") != `| bytes=1000-|"v1" |` {
		t.Fatalf("requests' Range|If-Range: %q", ranges)
	}
}

// TestFetchPCAP_ARefusalIsNotAskedAgain: an answer outside 2xx is the
// controller's refusal, reported once with its own words.
func TestFetchPCAP_ARefusalIsNotAskedAgain(t *testing.T) {
	ctl, c := capturing(t, true, nil)
	n := pcapHandler(ctl, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"description": "busy"}`)
	})
	_, err := c.FetchPCAP(context.Background(), "k1", 16<<20)
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusServiceUnavailable || !strings.Contains(err.Error(), "busy") || *n != 1 {
		t.Fatalf("FetchPCAP: %v after %d requests", err, *n)
	}
}

// TestFetchPCAP_AnEndedContextStopsTheRetries: a context that ends while
// a fetch waits to ask again ends it, naming both the cut and the end.
func TestFetchPCAP_AnEndedContextStopsTheRetries(t *testing.T) {
	ctl, c := capturing(t, true, bigPCAP(64<<10))
	fetchRetryWait = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pcapHandler(ctl, func(_ int, w http.ResponseWriter, _ *http.Request) {
		// Ended a second after the cut, while the fetch waits its hour.
		time.AfterFunc(time.Second, cancel)
		cutAt(w, bigPCAP(64<<10), 100)
	})
	_, err := c.FetchPCAP(ctx, "k1", 16<<20)
	if err == nil || !strings.Contains(err.Error(), "INTERNAL_ERROR; received from peer, then context canceled") {
		t.Fatalf("FetchPCAP: %v", err)
	}
}

// TestCutOff: only a transient reset received from the controller, or a
// body short of its stated length, is a cut worth asking again for.
func TestCutOff(t *testing.T) {
	peer := errors.New("received from peer")
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"INTERNAL_ERROR from the controller":  {streamReset{1, h2InternalError, peer}, true},
		"REFUSED_STREAM from the controller":  {fmt.Errorf("x: %w", streamReset{1, h2RefusedStream, peer}), true},
		"CANCEL from the controller":          {streamReset{1, h2Cancel, peer}, true},
		"PROTOCOL_ERROR from the controller":  {streamReset{1, 0x1, peer}, false},
		"INTERNAL_ERROR made on this side":    {streamReset{1, h2InternalError, nil}, false},
		"a body short of its length":          {fmt.Errorf("x: %w", io.ErrUnexpectedEOF), true},
		"a context ended":                     {context.Canceled, false},
		"an error of no transport's shape":    {errors.New("no"), false},
		"INTERNAL_ERROR with another's cause": {streamReset{1, h2InternalError, errors.New("local")}, false},
	} {
		if got := cutOff(tc.err); got != tc.want {
			t.Errorf("%s: cutOff = %v", name, got)
		}
	}
	if (streamReset{3, h2Cancel, nil}).Error() != "stream 3 reset with code 8" {
		t.Error("streamReset's text")
	}
}

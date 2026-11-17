// Package cml: a capture's file fetched from the controller (GET
// /pcap/{key}). The size the answer states is held to the bound before its
// body is read; the read is bounded by idle time rather than by the whole
// download, which over a slow link outlasts one request's minute; and a body
// the controller cuts off partway is asked for again. A real CML 2.7
// controller reset the stream (HTTP/2 RST_STREAM, INTERNAL_ERROR) partway
// through a 22,419-packet capture on both of two tries (2026-11-17), while a
// 3,000-packet one came whole.
package cml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// fetchAttempts is how many times one fetch asks for the file, counting
// the first.
const fetchAttempts = 4

// maxErrorBody bounds what is read of an answer that is not the file.
const maxErrorBody = 64 << 10

// fetchIdle is how long a fetch waits for its next bytes before giving up,
// and fetchRetryWait how long it waits before asking again. They are
// variables only so a test can prove them quickly; nothing else sets them.
var (
	fetchIdle      = time.Minute
	fetchRetryWait = 2 * time.Second
)

// errFetchIdle is why a fetch that waited fetchIdle for bytes was ended.
var errFetchIdle = errors.New("the controller sent nothing for the idle bound")

// h2Code is an HTTP/2 error code, as RFC 9113 section 7 numbers them.
type h2Code uint32

// The HTTP/2 error codes a controller resets a stream with that say the
// stream failed, not the request: asked again, the same request can work.
const (
	h2InternalError h2Code = 0x2
	h2RefusedStream h2Code = 0x7
	h2Cancel        h2Code = 0x8
)

// streamReset is the shape of net/http's HTTP/2 stream error, which its
// As method converts to any struct with these three fields, so the code is
// read without importing a second HTTP/2 implementation.
type streamReset struct {
	StreamID uint32
	Code     h2Code
	Cause    error
}

// Error implements error, which errors.As requires of a target.
func (s streamReset) Error() string {
	return fmt.Sprintf("stream %d reset with code %d", s.StreamID, s.Code)
}

// cutOff reports whether err is the controller ending the body partway in
// a way that asking again can mend: a stream it reset with a transient
// code (the reset received from it, never one this side made), or a
// connection closed before the length its answer stated.
func cutOff(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var reset streamReset
	if !errors.As(err, &reset) || reset.Cause == nil || reset.Cause.Error() != "received from peer" {
		return false
	}
	return reset.Code == h2InternalError || reset.Code == h2RefusedStream || reset.Code == h2Cancel
}

// pcapFetch is one fetch of a capture's file, across its attempts.
type pcapFetch struct {
	hc    *http.Client
	url   string
	token string
	max   int64
	buf   bytes.Buffer
	// total is the length the first answer stated, -1 when it stated none.
	total int64
	// ranges is whether that answer said the controller serves byte ranges,
	// and validator its ETag (a strong one) or Last-Modified, which a resumed
	// request sends as If-Range so a changed file is never spliced.
	ranges    bool
	validator string
	started   bool
}

// FetchPCAP returns the capture file the key names. An answer stating more
// than max bytes is refused before its body is read, and one stating no
// length is refused once max bytes have arrived. A body the controller cuts
// off partway (cutOff) is asked for again, at most fetchAttempts times in
// all: from where it stopped when the controller serves byte ranges of the
// same file, otherwise from the start, and then only when it states the
// length the first answer did. The fetch is bounded by idle time
// (fetchIdle) and ctx, not by the client's per-request timeout.
func (c *Client) FetchPCAP(ctx context.Context, key string, max int64) ([]byte, error) {
	if c.token == "" {
		return nil, errors.New("cml: Connect first")
	}
	u, err := c.endpoint("/pcap/" + url.PathEscape(key))
	if err != nil {
		return nil, err
	}
	// A copy of the client without its whole-request timeout, which would
	// end a long download that is still arriving; idle time bounds it.
	hc := *c.http
	hc.Timeout = 0
	f := &pcapFetch{hc: &hc, url: u, token: c.token, max: max, total: -1}
	began := time.Now()
	for attempt := 1; ; attempt++ {
		again, err := f.attempt(ctx)
		if err == nil {
			break
		}
		if !again || attempt == fetchAttempts {
			return nil, fmt.Errorf("cml: GET /api/v0/pcap: %w (attempt %d of %d, %s in all)", err, attempt, fetchAttempts, time.Since(began).Round(time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("cml: GET /api/v0/pcap: %w, then %w", err, ctx.Err())
		case <-time.After(fetchRetryWait):
		}
	}
	data := f.buf.Bytes()
	if !isCapture(data) {
		return nil, errors.New("cml: the controller's answer is not a pcap or pcapng file")
	}
	return data, nil
}

// attempt asks for the file once, from where the last attempt stopped
// when it can, and reports whether a failure is worth asking again.
func (f *pcapFetch) attempt(parent context.Context) (bool, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	resume := f.buf.Len() > 0 && f.ranges && f.total > 0
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	if resume {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", f.buf.Len()))
		if f.validator != "" {
			req.Header.Set("If-Range", f.validator)
		}
	}
	idle := time.AfterFunc(fetchIdle, func() { cancel(errFetchIdle) })
	defer idle.Stop()
	res, err := f.hc.Do(req)
	if err != nil {
		return false, explainCancel(ctx, err)
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case resume && res.StatusCode == http.StatusPartialContent:
		if err := f.checkRange(res.Header.Get("Content-Range")); err != nil {
			// Not the rest of this file: start over, without ranges.
			f.ranges = false
			f.buf.Reset()
			return true, err
		}
	case res.StatusCode == http.StatusOK:
		if err := f.begin(res); err != nil {
			return false, err
		}
	default:
		body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		return false, &StatusError{Method: http.MethodGet, Path: "/pcap/<key>", Status: res.StatusCode, Body: string(body)}
	}
	room := f.max + 1 - int64(f.buf.Len())
	_, err = io.Copy(&f.buf, &idleReader{r: io.LimitReader(res.Body, room), timer: idle})
	if int64(f.buf.Len()) > f.max {
		return false, f.overBound()
	}
	if err != nil {
		return cutOff(err), fmt.Errorf("reading the file: after %d of %s bytes: %w", f.buf.Len(), f.stated(), explainCancel(ctx, err))
	}
	if f.total >= 0 && int64(f.buf.Len()) != f.total {
		return false, fmt.Errorf("reading the file: it ended at %d of the %d bytes its answer stated", f.buf.Len(), f.total)
	}
	return false, nil
}

// begin takes a whole file's answer: the length it states, held to the
// bound and, on a fetch started over, to the first answer's, and whether
// a later attempt can resume it.
func (f *pcapFetch) begin(res *http.Response) error {
	f.buf.Reset()
	n := res.ContentLength
	if f.started && f.total >= 0 && n != f.total {
		then := "no length"
		if n >= 0 {
			then = fmt.Sprintf("%d bytes", n)
		}
		return fmt.Errorf("the file changed between attempts: its answers stated %d bytes and then %s", f.total, then)
	}
	if n > f.max {
		f.total = n
		return f.overBound()
	}
	if !f.started {
		f.started, f.total = true, n
		f.ranges = strings.EqualFold(strings.TrimSpace(res.Header.Get("Accept-Ranges")), "bytes")
		f.validator = res.Header.Get("Last-Modified")
		if tag := res.Header.Get("ETag"); tag != "" && !strings.HasPrefix(tag, "W/") {
			f.validator = tag
		}
	}
	return nil
}

// checkRange holds a partial answer's Content-Range ("bytes S-E/T") to the
// rest of the file being fetched: from the next byte to the last of the
// length first stated.
func (f *pcapFetch) checkRange(header string) error {
	want := fmt.Sprintf("bytes %d-%d/%d", f.buf.Len(), f.total-1, f.total)
	if strings.TrimSpace(header) != want {
		return fmt.Errorf("the controller answered the range %q, not %q", header, want)
	}
	return nil
}

// overBound is the refusal of a file past the bound, naming its size when
// the answer stated one.
func (f *pcapFetch) overBound() error {
	if f.total > f.max {
		return fmt.Errorf("the capture is %d bytes, over the %d this fetch takes: bound it with fewer packets or less time, or a filter", f.total, f.max)
	}
	return fmt.Errorf("the capture is over %d bytes, which this fetch takes: bound it with fewer packets or less time, or a filter", f.max)
}

// stated is the length the first answer stated, for a message.
func (f *pcapFetch) stated() string {
	if f.total < 0 {
		return "an unstated number of"
	}
	return strconv.FormatInt(f.total, 10)
}

// explainCancel names the idle bound when it is what ended ctx, rather
// than the bare cancellation the transport reports.
func explainCancel(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errFetchIdle) {
		return fmt.Errorf("%w (%s): %w", errFetchIdle, fetchIdle, err)
	}
	return err
}

// idleReader pushes the idle bound back each time bytes arrive.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
}

// Read implements io.Reader.
func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(fetchIdle)
	}
	return n, err
}

// The magic numbers a capture file begins with: pcap in either byte order,
// with microsecond or nanosecond times, and pcapng's section header.
var captureMagics = [][]byte{
	{0xd4, 0xc3, 0xb2, 0xa1}, {0xa1, 0xb2, 0xc3, 0xd4},
	{0x4d, 0x3c, 0xb2, 0xa1}, {0xa1, 0xb2, 0x3c, 0x4d},
	{0x0a, 0x0d, 0x0d, 0x0a},
}

// isCapture reports whether data begins as a pcap or pcapng file does.
func isCapture(data []byte) bool {
	for _, m := range captureMagics {
		if bytes.HasPrefix(data, m) {
			return true
		}
	}
	return false
}

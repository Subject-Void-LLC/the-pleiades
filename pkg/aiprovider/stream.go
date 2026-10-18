// Package aiprovider: sending a request and reading a streamed answer,
// as every kind does.
package aiprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// maxLine bounds one line of a streamed answer.
const maxLine = 4 << 20

// maxErrorBody bounds what an error answer is read for.
const maxErrorBody = 64 << 10

// maxAnswer bounds a whole answer's text.
const maxAnswer = 16 << 20

// post sends body as JSON to u with headers, and returns the response for
// a 2xx; any other status is read (bounded) into an *APIError.
func post(ctx context.Context, ep Endpoint, u *url.URL, headers map[string]string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return send(ep, req)
}

// get sends a GET to u with headers.
func get(ctx context.Context, ep Endpoint, u *url.URL, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return send(ep, req)
}

// send sends req, turning a failure to reach the provider and a non-2xx
// answer into an *APIError.
func send(ep Endpoint, req *http.Request) (*http.Response, error) {
	client := ep.Client
	if client == nil {
		return nil, errors.New("aiprovider: an endpoint needs a client")
	}
	resp, err := client.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, &APIError{Network: true, Message: err.Error()}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return nil, &APIError{Status: resp.StatusCode, Message: errorMessage(body), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
}

// errorMessage is a provider's error body's message: the message field of
// the shapes providers answer with, or the body itself, bounded.
func errorMessage(body []byte) string {
	var shaped struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &shaped) == nil {
		switch e := shaped.Error.(type) {
		case string:
			return bound(e)
		case map[string]any:
			if m, ok := e["message"].(string); ok {
				return bound(m)
			}
		}
	}
	return bound(strings.TrimSpace(string(body)))
}

// errTruncated is a stream that ended, cleanly, before the provider said the
// answer was whole: the text so far is part of one, and a caller reading it
// as the answer would act on a cut-off sentence.
func errTruncated() error {
	return &APIError{Streamed: true, Message: "the stream ended before the provider finished the answer"}
}

// bound makes text a provider sent safe to put in an error, a record or a
// terminal: cut to a size an error can be, on a rune boundary, with every
// character that is not printable escaped.
func bound(s string) string {
	return remoteexec.Excerpt(s, 512)
}

// retryAfter reads a Retry-After header's seconds (a date is ignored).
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 3600 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// sseEvents reads a server-sent event stream, calling fn with each
// event's name and data. An error fn returns stops the stream.
func sseEvents(body io.Reader, fn func(event string, data []byte) error) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	event, data := "", []byte(nil)
	flush := func() error {
		if data == nil {
			return nil
		}
		err := fn(event, data)
		event, data = "", nil
		return err
	}
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if err := flush(); err != nil {
				return err
			}
		case bytes.HasPrefix(line, []byte(":")):
		case bytes.HasPrefix(line, []byte("event:")):
			event = strings.TrimSpace(string(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			d := bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))
			// An event nothing ends is never handed over, so the answer's own
			// limit cannot stop it: bound what one event holds the same way.
			if len(data)+len(d)+1 > maxAnswer {
				return &APIError{Streamed: true, Message: fmt.Sprintf("an event is over %d bytes", maxAnswer)}
			}
			if data != nil {
				data = append(data, '\n')
			}
			data = append(data, d...)
		}
	}
	if err := sc.Err(); err != nil {
		return &APIError{Streamed: true, Message: err.Error()}
	}
	return flush()
}

// ndjson reads newline-delimited JSON objects, calling fn with each.
func ndjson(body io.Reader, fn func(line []byte) error) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			if err := fn(line); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return &APIError{Streamed: true, Message: err.Error()}
	}
	return nil
}

// answer collects streamed text within maxAnswer.
type answer struct {
	b strings.Builder
}

func (a *answer) add(s string) error {
	if a.b.Len()+len(s) > maxAnswer {
		return &APIError{Streamed: true, Message: fmt.Sprintf("the answer is over %d bytes", maxAnswer)}
	}
	a.b.WriteString(s)
	return nil
}

// join resolves path against base, keeping base's own path.
func join(base *url.URL, path string) *url.URL {
	u := *base
	u.Path = strings.TrimSuffix(base.Path, "/") + path
	return &u
}

// maxListBody bounds a model list.
const maxListBody = 8 << 20

// decodeBounded decodes a JSON answer within maxListBody.
func decodeBounded(resp *http.Response, v any) error {
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListBody)).Decode(v); err != nil {
		return &APIError{Status: resp.StatusCode, Message: "the answer is not JSON: " + err.Error()}
	}
	return nil
}

// newRequest builds a request with a JSON body.
func newRequest(ctx context.Context, method string, u *url.URL, body []byte, headers map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

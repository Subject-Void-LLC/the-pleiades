// Package cfapi reads Cloudflare's API v4: an account, its tunnels and
// their connectors, its Access applications, organization and policies,
// and a zone's DNS records. It only reads. Every answer is held to
// Cloudflare's envelope ({success, errors, result, result_info}) and
// bounded; every list is followed page by page, up to a bound past which
// it fails rather than answer from part of a list; a 429 or 5xx is retried
// twice, honoring Retry-After. The token goes only to Base, as a bearer
// header, and no redirect is followed.
package cfapi

import (
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

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

// DefaultBase is Cloudflare's API.
const DefaultBase = "https://api.cloudflare.com/client/v4"

// Bounds on what is read: one answer, the pages of one list and their
// results together, and the items one page may hold.
const (
	maxBody     = 4 << 20
	maxListBody = 16 << 20
	maxPages    = 50
	perPage     = 50
	retries     = 2
)

// Client reads one account's API with one token.
type Client struct {
	// Base is the API's base URL, https.
	Base *url.URL
	// HTTP sends the requests; its transport carries the device's TLS and
	// it follows no redirect.
	HTTP *http.Client
	// Token is the API token.
	Token string
	// Sleep waits between retries; time.Sleep with the context when nil.
	Sleep func(context.Context, time.Duration) error
}

// Error is Cloudflare's refusal: the HTTP status and the envelope's errors.
type Error struct {
	Status int
	Codes  []int
	// Messages are Cloudflare's own, bounded.
	Messages []string
	// Path is what was read, so the refusal says which permission it needed.
	Path string
}

// Error names what was read, the status and Cloudflare's own messages,
// each after its code, saying for a 401 or a 403 what the token lacks.
func (e *Error) Error() string {
	var parts []string
	for i, m := range e.Messages {
		if i < len(e.Codes) {
			parts = append(parts, fmt.Sprintf("%d %s", e.Codes[i], m))
			continue
		}
		parts = append(parts, m)
	}
	msg := strings.Join(parts, "; ")
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("the token was refused reading %s (401): %s", e.Path, msg)
	case http.StatusForbidden:
		return fmt.Sprintf("the token may not read %s (403): %s; give it that permission", e.Path, msg)
	}
	return fmt.Sprintf("reading %s: status %d: %s", e.Path, e.Status, msg)
}

// envelope is every Cloudflare answer.
type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiMessage    `json:"errors"`
	Result  json.RawMessage `json:"result"`
	Info    *struct {
		Page    int  `json:"page"`
		PerPage *int `json:"per_page"`
		// TotalPages and TotalCount are each absent from some endpoints'
		// answers; absent is not zero.
		TotalPages *int `json:"total_pages"`
		TotalCount *int `json:"total_count"`
	} `json:"result_info"`
}

// apiMessage is one of the envelope's errors.
type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// get reads one page of path with query, into result's raw form.
func (c *Client) get(ctx context.Context, path string, query url.Values) (envelope, error) {
	full, err := httpapi.Join(c.Base, path)
	if err != nil {
		return envelope{}, err
	}
	if len(query) > 0 {
		full.RawQuery = query.Encode()
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for attempt := 0; ; attempt++ {
		env, status, wait, err := c.once(ctx, full, path)
		retryable := status == http.StatusTooManyRequests || status >= 500
		if err == nil || !retryable || attempt >= retries {
			return env, err
		}
		if wait <= 0 {
			wait = time.Duration(attempt+1) * time.Second
		}
		if err := sleep(ctx, wait); err != nil {
			return envelope{}, err
		}
	}
}

// once makes one request.
func (c *Client) once(ctx context.Context, full *url.URL, path string) (envelope, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full.String(), nil)
	if err != nil {
		return envelope{}, 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return envelope{}, 0, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return envelope{}, resp.StatusCode, 0, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(body) > maxBody {
		return envelope{}, resp.StatusCode, 0, fmt.Errorf("reading %s: the answer is over %d bytes", path, maxBody)
	}
	env, err := decode(path, resp.StatusCode, body)
	if err != nil {
		return envelope{}, resp.StatusCode, retryAfter(resp), err
	}
	return env, resp.StatusCode, 0, nil
}

// decode reads one answer: Cloudflare's envelope, successful, with a 200;
// anything else is an error carrying Cloudflare's own codes and messages,
// bounded.
func decode(path string, status int, body []byte) (envelope, error) {
	if status >= 300 && status < 400 {
		return envelope{}, fmt.Errorf("reading %s: the API redirected (%d), which is not followed", path, status)
	}
	var env envelope
	decodeErr := json.Unmarshal(body, &env)
	if status == http.StatusOK && decodeErr == nil && env.Success {
		return env, nil
	}
	if status == http.StatusOK && decodeErr != nil {
		return envelope{}, fmt.Errorf("reading %s: the answer is not Cloudflare's envelope", path)
	}
	e := &Error{Status: status, Path: path}
	if status == http.StatusOK {
		e.Status = http.StatusBadGateway
	}
	for i, m := range env.Errors {
		if i == 8 {
			break
		}
		e.Codes = append(e.Codes, m.Code)
		// Each message is Cloudflare's own text: quoted and bounded through
		// pkg/farside, so a control byte in it is shown rather than sent.
		e.Messages = append(e.Messages, farside.Text(m.Message))
	}
	if len(e.Messages) == 0 && strings.TrimSpace(string(body)) != "" {
		// An answer that is not the envelope (Cloudflare's edge refusing
		// with "error code: 1020", a proxy's page) still says why, in its
		// own words, bounded and quoted.
		e.Messages = append(e.Messages, farside.Text(string(body)))
	}
	return envelope{}, e
}

// list reads every page of path into a slice of T.
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("per_page", strconv.Itoa(perPage))
	var out []T
	read := 0
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("reading %s: more than %d pages, which is not judged from part of a list", path, maxPages)
		}
		query.Set("page", strconv.Itoa(page))
		env, err := c.get(ctx, path, query)
		if err != nil {
			return nil, err
		}
		if read += len(env.Result); read > maxListBody {
			return nil, fmt.Errorf("reading %s: the list is over %d bytes, which is not judged from part of it", path, maxListBody)
		}
		var items []T
		if err := json.Unmarshal(env.Result, &items); err != nil {
			return nil, fmt.Errorf("reading %s: the result is not a list: %w", path, err)
		}
		if len(items) > perPage {
			return nil, fmt.Errorf("reading %s: page %d holds %d items, more than the %d asked for", path, page, len(items), perPage)
		}
		out = append(out, items...)
		if lastPage(env, page, len(items), len(out)) {
			return out, nil
		}
	}
}

// lastPage reports whether page, holding n items with total read so far,
// ends its list: by total_pages when the answer gives it, else by
// total_count, else by a page shorter than a full one. An answer with no
// result_info is a list of one page.
func lastPage(env envelope, page, n, total int) bool {
	switch {
	case n == 0 || env.Info == nil:
		return true
	case env.Info.TotalPages != nil:
		return page >= *env.Info.TotalPages
	case env.Info.TotalCount != nil:
		return total >= *env.Info.TotalCount
	}
	full := perPage
	if env.Info.PerPage != nil && *env.Info.PerPage > 0 && *env.Info.PerPage < perPage {
		full = *env.Info.PerPage
	}
	return n < full
}

// one reads path's single result into T.
func one[T any](ctx context.Context, c *Client, path string) (T, error) {
	var out T
	env, err := c.get(ctx, path, nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(env.Result, &out); err != nil {
		return out, fmt.Errorf("reading %s: the result is not what was asked for: %w", path, err)
	}
	return out, nil
}

// retryAfter is what the API asked to wait, or zero.
func retryAfter(resp *http.Response) time.Duration {
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s <= 300 {
		return time.Duration(s) * time.Second
	}
	return 0
}

// bounded cuts text Cloudflare sent to 512 bytes, on a rune.
func bounded(s string) string {
	if len(s) <= 512 {
		return s
	}
	cut := 512
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ErrNotFound is a name that resolves to nothing.
var ErrNotFound = errors.New("not found")

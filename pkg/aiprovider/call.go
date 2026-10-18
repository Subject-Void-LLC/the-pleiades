// Package aiprovider: one call through a kind, with every rule a call is
// held to, and what it cost.
package aiprovider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// CallOptions are how hard Call tries.
type CallOptions struct {
	// Retries is how many times a retryable failure is tried again.
	Retries int
	// Backoff is the first wait; each retry doubles it, with jitter. A
	// provider's Retry-After wins when it is longer.
	Backoff time.Duration
	// Sleep waits; time.Sleep when nil (a test passes its own).
	Sleep func(context.Context, time.Duration) error
}

// Usage is one call's record: what it asked, what it took, what it cost.
type Usage struct {
	Kind         string
	Model        string
	InputTokens  int
	OutputTokens int
	// Reported is whether the provider said how many tokens the call took.
	// When it did not, the counts are zero because nothing was said, and the
	// call is not priced.
	Reported bool
	Duration time.Duration
	Retries  int
	// Ignored are parameters the kind does not accept, never sent.
	Ignored []string
	// Dropped are parameters the provider rejected, sent once and never
	// again.
	Dropped []string
}

// Map is the usage record as a return value.
func (u Usage) Map() map[string]any {
	return map[string]any{
		"kind": u.Kind, "model": u.Model, "input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "usage_reported": u.Reported,
		"duration_ms": u.Duration.Milliseconds(), "retries": u.Retries,
		"ignored": strs(u.Ignored), "dropped": strs(u.Dropped),
	}
}

// strs is a string list as a return value.
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// Drops remembers, across one run's calls, the parameters a provider
// rejected, so a dropped parameter is never sent to it again. The zero
// value is ready; it is not safe for concurrent use.
type Drops map[string]bool

// Call makes req through kind, holding it to every rule: a parameter the
// kind does not accept is ignored and not sent; one the provider rejects
// (a 400 naming it) is dropped and the call sent again without it, and is
// never sent again where drops is shared; a retryable failure is retried
// with backoff up to opts.Retries; anything else fails the call. The usage
// record is returned with the error too.
func Call(ctx context.Context, kind Kind, ep Endpoint, req Request, drops Drops, opts CallOptions) (Response, Usage, error) {
	if drops == nil {
		drops = Drops{}
	}
	var learned []string
	resp, usage, err := call(ctx, kind, ep, req, drops, opts, &learned)
	if err != nil {
		// A drop is kept only by a call that then succeeded: a rejection that
		// went on failing was not shown to be about the parameter.
		for _, p := range learned {
			delete(drops, p)
			usage.Dropped = slices.DeleteFunc(usage.Dropped, func(d string) bool { return d == p })
		}
	}
	return resp, usage, err
}

// call is Call, naming in learned each parameter this call dropped that
// the drops it was given did not already hold.
func call(ctx context.Context, kind Kind, ep Endpoint, req Request, drops Drops, opts CallOptions, learned *[]string) (Response, Usage, error) {
	usage := Usage{Kind: kind.Name(), Model: req.Model}
	if kind.NeedsKey() && ep.Key == "" {
		return Response{}, usage, fmt.Errorf("a %s provider needs an API key, and the device has no credential", kind.Name())
	}
	omit := map[string]bool{}
	for _, p := range req.Set() {
		switch {
		case !kind.Accepts(p):
			omit[p] = true
			usage.Ignored = append(usage.Ignored, p)
		case drops[p]:
			omit[p] = true
			usage.Dropped = append(usage.Dropped, p)
		}
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	backoff := opts.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	start := time.Now()
	defer func() { usage.Duration = time.Since(start) }()
	for {
		resp, err := kind.Chat(ctx, ep, req, omit)
		if err == nil {
			// What the provider says about itself goes into records and review
			// rules, so it is held to the size and the characters an error is.
			resp.Model, resp.StopReason = remoteexec.Excerpt(resp.Model, 128), remoteexec.Excerpt(resp.StopReason, 64)
			usage.InputTokens, usage.OutputTokens, usage.Reported = resp.InputTokens, resp.OutputTokens, resp.Reported
			if resp.Model != "" {
				usage.Model = resp.Model
			}
			usage.Duration = time.Since(start)
			return resp, usage, nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			return Response{}, usage, err
		}
		if p := rejected(kind, req, omit, apiErr); p != "" {
			omit[p], drops[p] = true, true
			usage.Dropped = append(usage.Dropped, p)
			*learned = append(*learned, p)
			continue
		}
		if !apiErr.Retryable() || usage.Retries >= opts.Retries {
			if usage.Retries > 0 {
				return Response{}, usage, fmt.Errorf("after %d retries: %w", usage.Retries, err)
			}
			return Response{}, usage, err
		}
		wait := backoff << usage.Retries
		wait += jitter(wait / 2)
		if apiErr.RetryAfter > wait {
			wait = apiErr.RetryAfter
		}
		usage.Retries++
		if err := sleep(ctx, wait); err != nil {
			return Response{}, usage, err
		}
	}
}

// rejected is the parameter a 400 names among those sent, or "".
func rejected(kind Kind, req Request, omit map[string]bool, e *APIError) string {
	if e.Status != 400 {
		return ""
	}
	msg := strings.ToLower(e.Message)
	for _, p := range req.Set() {
		if omit[p] {
			continue
		}
		if wire := strings.ToLower(kind.WireName(p)); wire != "" && mentions(msg, wire) {
			return p
		}
	}
	return ""
}

// mentions reports whether msg holds name as a word of its own: "stop"
// is in "unsupported parameter: stop" and not in "stopped".
func mentions(msg, name string) bool {
	word := func(r byte) bool {
		return r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}
	for from := 0; ; {
		at := strings.Index(msg[from:], name)
		if at < 0 {
			return false
		}
		start, end := from+at, from+at+len(name)
		if (start == 0 || !word(msg[start-1])) && (end == len(msg) || !word(msg[end])) {
			return true
		}
		from = start + 1
	}
}

// jitter is a random wait from zero to most, so retries from many runs do
// not arrive together.
func jitter(most time.Duration) time.Duration {
	if most <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(most)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
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

// Price is a model's price per million tokens, in and out, in one
// currency.
type Price struct {
	Input, Output float64
	Currency      string
}

// Prices is an operator's price table: model to price.
type Prices map[string]Price

// Cost is what usage cost under the table, and false for a model the
// table does not price or a call whose usage the provider did not report.
func (p Prices) Cost(u Usage) (float64, string, bool) {
	price, ok := p[u.Model]
	if !ok || !u.Reported {
		return 0, "", false
	}
	return (float64(u.InputTokens)*price.Input + float64(u.OutputTokens)*price.Output) / 1e6, price.Currency, true
}

// Models is the table's models, sorted.
func (p Prices) Models() []string {
	out := make([]string, 0, len(p))
	for m := range p {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// EstimateTokens estimates a text's tokens at four characters a token,
// the rule of thumb a check prices a prompt by before any call.
func EstimateTokens(text string) int {
	return (len([]rune(text)) + 3) / 4
}

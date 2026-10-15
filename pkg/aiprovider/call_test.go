// Tests for the rules every call is held to.
package aiprovider_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// okAnswer streams a one-word OpenAI answer.
func okAnswer(w http.ResponseWriter) {
	stream(w, `data: {"model":"m","choices":[{"delta":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`, `data: [DONE]`)
}

// sleeps records the waits Call asked for, without waiting.
type sleeps []time.Duration

func (s *sleeps) sleep(_ context.Context, d time.Duration) error { *s = append(*s, d); return nil }

// TestCall_IgnoresDropsAndRetries: a parameter the kind does not accept
// is ignored and never sent; one the provider rejects is dropped, the
// call resent without it, and a later call sharing the drops never sends
// it; a 429 is retried after its Retry-After; a 400 naming nothing sent
// and a 401 are not retried.
func TestCall_IgnoresDropsAndRetries(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		switch {
		case n == 0:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'temperature' is not supported with this model."}}`))
		case n == 1:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
		default:
			okAnswer(w)
		}
	})
	k, ep := kind(t, "openai"), srv.endpoint(t, "/v1", "k")
	drops := aiprovider.Drops{}
	var waited sleeps
	req := aiprovider.Request{Model: "m", Messages: []aiprovider.Message{{Role: "user", Content: "x"}}, Params: aiprovider.Params{Temperature: f64(0.5), Thinking: i(100)}}
	resp, usage, err := aiprovider.Call(context.Background(), k, ep, req, drops, aiprovider.CallOptions{Retries: 3, Sleep: waited.sleep})
	if err != nil || resp.Text != "ok" {
		t.Fatalf("Call = %+v, %v", resp, err)
	}
	if strings.Join(usage.Ignored, ",") != "thinking" || strings.Join(usage.Dropped, ",") != "temperature" || usage.Retries != 1 || usage.InputTokens != 3 {
		t.Errorf("usage = %+v", usage)
	}
	if len(waited) != 1 || waited[0] < 7*time.Second {
		t.Errorf("waits = %v, want at least the 7s Retry-After", waited)
	}
	reqs := srv.requests()
	if _, sent := reqs[1].Body["temperature"]; sent {
		t.Error("a dropped parameter was sent again")
	}
	if _, usage, err := aiprovider.Call(context.Background(), k, ep, req, drops, aiprovider.CallOptions{}); err != nil || strings.Join(usage.Dropped, ",") != "temperature" {
		t.Fatalf("a later call sharing the drops = %+v, %v", usage, err)
	}
	if _, sent := srv.requests()[3].Body["temperature"]; sent {
		t.Error("a later call sent a parameter dropped earlier")
	}
}

// TestCall_GivesUpOnWhatCannotSucceed: a 401 and a 400 naming nothing
// sent are not retried; a 503 is retried up to the limit and the error
// says how many times; a kind that needs a key is refused without one.
func TestCall_GivesUpOnWhatCannotSucceed(t *testing.T) {
	for status, want := range map[int]string{401: "status 401", 400: "status 400"} {
		srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"no"}}`))
		})
		var waited sleeps
		_, usage, err := aiprovider.Call(context.Background(), kind(t, "openai"), srv.endpoint(t, "/v1", "k"),
			aiprovider.Request{Model: "m", Params: aiprovider.Params{TopP: f64(1)}}, nil, aiprovider.CallOptions{Retries: 3, Sleep: waited.sleep})
		if err == nil || !strings.Contains(err.Error(), want) || usage.Retries != 0 || len(srv.requests()) != 1 {
			t.Errorf("status %d: %v, %d retries, %d requests", status, err, usage.Retries, len(srv.requests()))
		}
	}
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) { w.WriteHeader(503) })
	var waited sleeps
	_, usage, err := aiprovider.Call(context.Background(), kind(t, "openai"), srv.endpoint(t, "/v1", "k"), aiprovider.Request{Model: "m"}, nil, aiprovider.CallOptions{Retries: 2, Backoff: time.Second, Sleep: waited.sleep})
	if err == nil || !strings.Contains(err.Error(), "after 2 retries") || usage.Retries != 2 || len(srv.requests()) != 3 || waited[1] < 2*time.Second {
		t.Errorf("503s: %v, %+v, %d requests, waits %v", err, usage, len(srv.requests()), waited)
	}
	if _, _, err := aiprovider.Call(context.Background(), kind(t, "anthropic"), srv.endpoint(t, "", ""), aiprovider.Request{Model: "m"}, nil, aiprovider.CallOptions{}); err == nil || !strings.Contains(err.Error(), "needs an API key") {
		t.Errorf("anthropic with no key = %v", err)
	}
}

// TestCall_AFailureMidStreamIsNotRetried: an error after the answer began
// could bill twice, so it ends the call.
func TestCall_AFailureMidStreamIsNotRetried(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		stream(w, `event: message_start`+"\n"+`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
			`event: error`+"\n"+`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	})
	var waited sleeps
	_, usage, err := aiprovider.Call(context.Background(), kind(t, "anthropic"), srv.endpoint(t, "", "k"), aiprovider.Request{Model: "m"}, nil, aiprovider.CallOptions{Retries: 3, Sleep: waited.sleep})
	if err == nil || !strings.Contains(err.Error(), "stopped partway") || usage.Retries != 0 || len(srv.requests()) != 1 {
		t.Errorf("a mid-stream failure: %v, %+v", err, usage)
	}
}

// TestPrices and EstimateTokens.
func TestPrices(t *testing.T) {
	p := aiprovider.Prices{"m": {Input: 3, Output: 15, Currency: "USD"}}
	cost, cur, ok := p.Cost(aiprovider.Usage{Model: "m", InputTokens: 1000, OutputTokens: 200})
	if !ok || cur != "USD" || cost < 0.005999 || cost > 0.006001 {
		t.Errorf("Cost = %v %s %v", cost, cur, ok)
	}
	if _, _, ok := p.Cost(aiprovider.Usage{Model: "other"}); ok {
		t.Error("an unpriced model was priced")
	}
	if n := aiprovider.EstimateTokens("abcdefgh"); n != 2 {
		t.Errorf("EstimateTokens = %d", n)
	}
}

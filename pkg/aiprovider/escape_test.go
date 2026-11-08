// Tests that a model name Google's kind puts in its request path reaches
// the server as that one name: escaped once, and decoded back to the name.
package aiprovider_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// TestChat_PostsJSON: every kind's chat is a POST whose body is declared as
// JSON. post builds its request through newRequest since the path moved to
// httpapi.Join, and this holds that the header moved with it.
func TestChat_PostsJSON(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		w.WriteHeader(http.StatusOK)
	})
	for _, name := range []string{"anthropic", "google", "ollama", "openai"} {
		// The empty answer is not one any kind reads, so the error is
		// expected; what is asserted is the request it sent.
		_, _ = kind(t, name).Chat(context.Background(), srv.endpoint(t, "", "key"), aiprovider.Request{
			Model:    "m",
			Messages: []aiprovider.Message{{Role: "user", Content: "hi"}},
		}, nil)
		reqs := srv.requests()
		if len(reqs) == 0 {
			t.Fatalf("%s sent nothing", name)
		}
		r := reqs[len(reqs)-1]
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Body == nil {
			t.Errorf("%s sent %s %s with Content-Type %q and body %v, want a JSON POST", name, r.Method, r.Path, r.Header.Get("Content-Type"), r.Body)
		}
	}
}

// TestChat_RefusesAnEndpointWithNoBase: an endpoint with no base URL is an
// error, not a request to nowhere (it used to be a nil dereference).
func TestChat_RefusesAnEndpointWithNoBase(t *testing.T) {
	for _, name := range []string{"anthropic", "google", "ollama", "openai"} {
		_, err := kind(t, name).Chat(context.Background(), aiprovider.Endpoint{Client: http.DefaultClient}, aiprovider.Request{
			Model:    "m",
			Messages: []aiprovider.Message{{Role: "user", Content: "hi"}},
		}, nil)
		if err == nil {
			t.Errorf("%s: Chat with no base URL succeeded", name)
		}
	}
}

// TestGoogle_SendsTheModelNameEscapedOnce: the model's path segment is
// url.PathEscape's, exactly, under a base URL with a path of its own. The
// kind used to append the escaped segment to the URL's decoded path, so it
// was escaped again: a model "a b" was asked for as "a%20b".
func TestGoogle_SendsTheModelNameEscapedOnce(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		stream(w, `data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	})
	for _, model := range []string{"a b", "a/b", "a?b", "a#b", "..", "%2E%2E", "%2F"} {
		_, err := kind(t, "google").Chat(context.Background(), srv.endpoint(t, "/proxy", "AIza-test"), aiprovider.Request{
			Model:    model,
			Messages: []aiprovider.Message{{Role: "user", Content: "hi"}},
		}, nil)
		if err != nil {
			t.Errorf("model %q: %v", model, err)
			continue
		}
		reqs := srv.requests()
		r := reqs[len(reqs)-1]
		if want := "/proxy/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"; r.URI != want {
			t.Errorf("model %q was sent as %q, want %q", model, r.URI, want)
		}
		if want := "/proxy/v1beta/models/" + model + ":streamGenerateContent"; r.Path != want {
			t.Errorf("model %q was decoded by the server as %q, want %q", model, r.Path, want)
		}
	}
}

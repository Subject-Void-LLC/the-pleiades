// Fuzzing for what reads untrusted provider output.
package aiprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// FuzzSchemaValidate holds a schema's checks to their contract on any
// answer: they return or refuse without panicking.
func FuzzSchemaValidate(f *testing.F) {
	s, err := aiprovider.ParseSchema([]byte(`{"type":"object","properties":{"a":{"type":"array","items":{"type":"integer","maximum":3}}},"additionalProperties":{"type":"string"}}`))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{`{"a":[1,2]}`, `{"b":"x"}`, `[]`, `{"a":[1e400]}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = s.Validate(data)
	})
}

// FuzzAnthropicStream holds the anthropic kind to its contract on any
// stream a server sends: it answers or refuses without panicking.
func FuzzAnthropicStream(f *testing.F) {
	f.Add([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
	f.Add([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"))
	f.Add([]byte("data: not json\n\n"))
	// One server for the whole run, answering with the current input: a
	// server per input runs a machine out of ephemeral ports.
	var mu sync.Mutex
	var current []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := current
		mu.Unlock()
		_, _ = w.Write(body)
	}))
	f.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	ep := aiprovider.Endpoint{Base: u, Client: srv.Client(), Key: "k"}
	k, _ := aiprovider.Lookup("anthropic")
	f.Fuzz(func(t *testing.T, body []byte) {
		mu.Lock()
		current = body
		mu.Unlock()
		_, _ = k.Chat(context.Background(), ep, aiprovider.Request{Model: "m"}, nil)
	})
}

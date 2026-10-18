// The servers the kinds' tests call: each speaks one provider's
// documented streaming format over real HTTP, and records what it was
// sent, so a test asserts both what a kind sends and how it reads the
// answer.
package aiprovider_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// seen is one request a test server received.
type seen struct {
	Method, Path, Query string
	Header              http.Header
	Body                map[string]any
}

// server is a test server answering each request with answer.
type server struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []seen
}

// newServer starts a server; answer writes the response for the i-th
// request.
func newServer(t *testing.T, answer func(i int, w http.ResponseWriter, s seen)) *server {
	t.Helper()
	srv := &server{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		s := seen{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone()}
		_ = json.Unmarshal(data, &s.Body)
		srv.mu.Lock()
		i := len(srv.reqs)
		srv.reqs = append(srv.reqs, s)
		srv.mu.Unlock()
		answer(i, w, s)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// requests is every request the server received.
func (s *server) requests() []seen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seen(nil), s.reqs...)
}

// endpoint reaches the server at path, with key.
func (s *server) endpoint(t *testing.T, path, key string) aiprovider.Endpoint {
	t.Helper()
	u, err := url.Parse(s.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	return aiprovider.Endpoint{Base: u, Client: s.Client(), Key: key}
}

// stream writes events as server-sent events.
func stream(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		_, _ = io.WriteString(w, e+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// kind looks a built-in kind up.
func kind(t *testing.T, name string) aiprovider.Kind {
	t.Helper()
	k, ok := aiprovider.Lookup(name)
	if !ok {
		t.Fatalf("kind %s is not registered", name)
	}
	return k
}

// f64 and i are pointers to parameter values.
func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }

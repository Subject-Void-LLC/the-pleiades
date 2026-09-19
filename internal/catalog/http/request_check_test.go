// Package http_test: tests of http.request's check.
package http_test

import (
	"context"
	"errors"
	nethttp "net/http"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestCheckRequest_SendsOnlyASafeRequest covers the check against a real
// HTTP server that counts what reaches it: a request in a safe method is
// sent, once, and the check reports exactly what the real run of it
// reports (the same stats, no change); a request in any other method
// never reaches the server and is answered "cannot check this call"; and
// a request a real run would refuse is refused the same way.
func TestCheckRequest_SendsOnlyASafeRequest(t *testing.T) {
	var hits atomic.Int32
	server := requestServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) {
		hits.Add(1)
		requestOK(w, r)
	})

	for _, method := range []string{"GET", "head", "OPTIONS", "TRACE"} {
		params := map[string]any{"url": server.URL, "method": method}
		before := hits.Load()
		checkRC := newRequestContext()
		checked, err := http.CheckRequest(context.Background(), checkRC, nil, params)
		if err != nil {
			t.Fatalf("%s: check: %v", method, err)
		}
		if hits.Load()-before != 1 {
			t.Errorf("%s: the check reached the server %d time(s), want once", method, hits.Load()-before)
		}
		runRC := newRequestContext()
		ran, err := requestRun(runRC, params)
		if err != nil {
			t.Fatalf("%s: run: %v", method, err)
		}
		if checked.Changed || checked.Changed != ran.Changed || checkRC.stats["status"] != runRC.stats["status"] || checkRC.stats["content"] != runRC.stats["content"] {
			t.Errorf("%s: check %+v %v, run %+v %v; want the same answer and no change", method, checked, checkRC.stats, ran, runRC.stats)
		}
	}

	for _, method := range []string{"POST", "put", "DELETE", "PATCH"} {
		before := hits.Load()
		_, err := http.CheckRequest(context.Background(), newRequestContext(), nil, map[string]any{"url": server.URL, "method": method, "body": "{}"})
		var cannot *collection.CannotCheckError
		if !errors.As(err, &cannot) {
			t.Errorf("%s: check = %v, want a CannotCheckError", method, err)
		}
		if hits.Load() != before {
			t.Errorf("%s: a check sent a request that may change the server", method)
		}
	}

	if _, err := http.CheckRequest(context.Background(), newRequestContext(), nil, map[string]any{"url": "ftp://example.com/x"}); err == nil {
		t.Error("the check accepted a URL a real run refuses")
	}
}

// TestCheckRequest_Registered proves the manifest and the functions agree:
// check support is declared, and Check is CheckRequest rather than Request,
// which would send a POST during a check.
func TestCheckRequest_Registered(t *testing.T) {
	d, ok := collection.Lookup("http.request")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("http.request = %+v, want check support declared and a Check", d.Manifest)
	}
	var hits atomic.Int32
	server := requestServer(t, func(w nethttp.ResponseWriter, r *nethttp.Request) { hits.Add(1); requestOK(w, r) })
	if _, err := d.Check(context.Background(), newRequestContext(), nil, map[string]any{"url": server.URL, "method": "POST"}); err == nil || hits.Load() != 0 {
		t.Errorf("the registered Check sent a POST (err %v, hits %d)", err, hits.Load())
	}
}

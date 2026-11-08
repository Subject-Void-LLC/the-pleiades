// Tests that a name the virt.clab methods escape into a route reaches the
// server as that one name: a server routing by segment, as the real one
// does, must read back exactly the name the method was given.
package clabapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/clabapi"
)

// TestCall_SendsEachNameAsOneSegment calls a node's logs route the way
// virt.clab.node.logs builds it, through a server that routes by segment.
// The server must match the route and read back the lab and node names
// unchanged. A node name holding a / used to reach the server as two
// segments, because the joined path was decoded and sent decoded.
func TestCall_SendsEachNameAsOneSegment(t *testing.T) {
	var mu sync.Mutex
	var uri string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/labs/{lab}/nodes/{node}/logs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uri = r.RequestURI
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"lab": r.PathValue("lab"), "node": r.PathValue("node")})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := clabapi.NewClient(base, nil)
	for _, tc := range []struct {
		node    string
		refused bool
	}{
		{"r1/mgmt", false},
		{"a b", false},
		{"a?b", false},
		{"a#b", false},
		{"%2E%2E", false},
		{"%2F", false},
		{"..", true},
	} {
		route := "/api/v1/labs/" + url.PathEscape("lab one") + "/nodes/" + url.PathEscape(tc.node) + "/logs"
		var got map[string]string
		err := c.Call(context.Background(), "", http.MethodGet, route, url.Values{"tail": {"5"}}, nil, &got)
		if tc.refused {
			if err == nil {
				t.Errorf("node %q was sent, want a refusal before any request", tc.node)
			}
			continue
		}
		if err != nil {
			t.Errorf("node %q: %v", tc.node, err)
			continue
		}
		if got["lab"] != "lab one" || got["node"] != tc.node {
			t.Errorf("node %q reached the server as lab %q node %q", tc.node, got["lab"], got["node"])
		}
		mu.Lock()
		sent := uri
		mu.Unlock()
		if want := route + "?tail=5"; sent != want {
			t.Errorf("node %q was sent as %q, want %q", tc.node, sent, want)
		}
	}
}

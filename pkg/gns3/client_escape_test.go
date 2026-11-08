// Tests that a project id reaches the server as the one id it is: escaped
// once in the path, and decoded back to the id by a server routing by
// segment.
package gns3_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
)

// TestNodes_SendsTheProjectIDEscapedOnce: the project id's segment is
// url.PathEscape's, exactly, and the server decodes it back to the id. The
// client used to set the escaped path as the URL's decoded path, so it was
// escaped again ("a b" went out as a%2520b), and ".." went out as a dot
// segment.
func TestNodes_SendsTheProjectIDEscapedOnce(t *testing.T) {
	var mu sync.Mutex
	var uri, project string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"2.2.61"}`))
	})
	mux.HandleFunc("GET /v2/projects/{project}/nodes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		project = r.PathValue("project")
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]gns3.Node{{ID: "n1", Name: "R1"}})
	})
	// Every request is recorded as it arrived, before routing, so one the
	// routes do not match is seen too.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uri = r.RequestURI
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, err := gns3.New(srv.URL, srv.Client(), gns3.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id      string
		refused bool
	}{
		{"a b", false},
		{"a/b", false},
		{"a?b", false},
		{"a#b", false},
		{"..", true},
		{"%2E%2E", false},
		{"%2F", false},
	} {
		mu.Lock()
		uri, project = "", ""
		mu.Unlock()
		nodes, err := c.Nodes(ctx, tc.id)
		mu.Lock()
		sent, decoded := uri, project
		mu.Unlock()
		if tc.refused {
			if err == nil || sent != "" {
				t.Errorf("Nodes(%q) sent %q, want a refusal before any request", tc.id, sent)
			}
			continue
		}
		if err != nil || len(nodes) != 1 {
			t.Errorf("Nodes(%q) = %v, %v", tc.id, nodes, err)
			continue
		}
		if want := "/v2/projects/" + url.PathEscape(tc.id) + "/nodes"; sent != want {
			t.Errorf("Nodes(%q) was sent as %q, want %q", tc.id, sent, want)
		}
		if decoded != tc.id {
			t.Errorf("Nodes(%q) reached the server as project %q", tc.id, decoded)
		}
	}
}

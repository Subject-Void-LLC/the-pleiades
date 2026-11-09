// Tests that a network or snapshot name reaches Batfish as the one name it
// is: escaped once in a path, and the same name in the path, the query and
// the JSON body of one call.
package batfish_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/batfish"
)

// escapeNames are the awkward names every escaping test sends, and whether
// the client must refuse each before sending anything: a dot segment names
// no network, since a server resolves it away.
var escapeNames = []struct {
	name    string
	refused bool
}{
	{"a b", false},
	{"a/b", false},
	{"a?b", false},
	{"a#b", false},
	{"..", true},
	{"%2E%2E", false},
	{"%2F", false},
}

// fakeBatfish routes by segment, as Batfish's own server does, and records
// how each request arrived and what it named.
type fakeBatfish struct {
	*httptest.Server
	mu sync.Mutex
	// uri is the request target of the last request.
	uri string
	// named is each request's method and route, with every name as the
	// server decoded it from the path, the query or the body.
	named []string
}

// newFakeBatfish starts the fake.
func newFakeBatfish(t *testing.T) *fakeBatfish {
	t.Helper()
	f := &fakeBatfish{}
	mux := http.NewServeMux()
	note := func(s string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.named = append(f.named, s)
	}
	mux.HandleFunc("POST /v2/networks", func(w http.ResponseWriter, r *http.Request) {
		note("create " + r.URL.Query().Get("name"))
	})
	mux.HandleFunc("DELETE /v2/networks/{network}", func(w http.ResponseWriter, r *http.Request) {
		note("delete " + r.PathValue("network"))
	})
	mux.HandleFunc("POST /v2/networks/{network}/snapshots/{snapshot}", func(w http.ResponseWriter, r *http.Request) {
		note("upload " + r.PathValue("network") + " | " + r.PathValue("snapshot"))
	})
	mux.HandleFunc("POST /v2/networks/{network}/work", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ContainerName string `json:"containerName"`
			TestrigName   string `json:"testrigName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		note("work " + r.PathValue("network") + " | body " + body.ContainerName + " | " + body.TestrigName)
	})
	mux.HandleFunc("GET /v2/networks/{network}/work/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workstatuscode":"TERMINATEDNORMALLY"}`))
	})
	// Every request is recorded as it arrived, before routing, so one the
	// routes do not match is seen too.
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.uri = r.RequestURI
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// last returns the last request target and every name recorded, clearing
// the record.
func (f *fakeBatfish) last() (string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	uri, named := f.uri, f.named
	f.uri, f.named = "", nil
	return uri, named
}

// TestDeleteNetwork_SendsTheNameEscapedOnce: the network's path segment is
// url.PathEscape's, exactly, and the server decodes it back to the name. The
// client used to escape the escaped segment again, so "a b" reached Batfish
// as a network literally named "a%20b", and ".." went out as a dot segment.
func TestDeleteNetwork_SendsTheNameEscapedOnce(t *testing.T) {
	f := newFakeBatfish(t)
	c, err := batfish.New(f.URL, f.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range escapeNames {
		err := c.DeleteNetwork(context.Background(), tc.name)
		uri, named := f.last()
		if tc.refused {
			if err == nil || uri != "" {
				t.Errorf("DeleteNetwork(%q) sent %q, want a refusal before any request", tc.name, uri)
			}
			continue
		}
		if err != nil {
			t.Errorf("DeleteNetwork(%q): %v", tc.name, err)
			continue
		}
		if want := "/v2/networks/" + url.PathEscape(tc.name); uri != want {
			t.Errorf("DeleteNetwork(%q) was sent as %q, want %q", tc.name, uri, want)
		}
		if len(named) != 1 || named[0] != "delete "+tc.name {
			t.Errorf("DeleteNetwork(%q) reached the server as %q", tc.name, named)
		}
	}
}

// TestUploadSnapshot_NamesOneNetworkEverywhere: one network created, given a
// snapshot and parsed is the same name in CreateNetwork's query, in each
// path, and in the work item's JSON body. When the path was escaped twice,
// the body named the network the caller meant and the path named another.
func TestUploadSnapshot_NamesOneNetworkEverywhere(t *testing.T) {
	f := newFakeBatfish(t)
	c, err := batfish.New(f.URL+"/", f.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const network, snapshot = "prod west/a?b#c", "base 1"
	if err := c.CreateNetwork(ctx, network); err != nil {
		t.Fatal(err)
	}
	if err := c.UploadSnapshot(ctx, network, snapshot, []byte("zip")); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteNetwork(ctx, network); err != nil {
		t.Fatal(err)
	}
	_, named := f.last()
	want := []string{
		"create " + network,
		"upload " + network + " | " + snapshot,
		"work " + network + " | body " + network + " | " + snapshot,
		"delete " + network,
	}
	if len(named) != len(want) {
		t.Fatalf("the server saw %q, want %q", named, want)
	}
	for i := range want {
		if named[i] != want[i] {
			t.Errorf("request %d named %q, want %q", i, named[i], want[i])
		}
	}
}

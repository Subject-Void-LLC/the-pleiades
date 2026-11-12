// Fuzz tests of what the client decodes from a GNS3 server it does not
// trust: the version answer and the login on either API line, the project
// list, and a project's nodes, with the project id (the server's own word)
// routed back into the next request's path. Each runs against a real HTTP
// server in the process, one per fuzz worker, whose answers the input sets.
package gns3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fuzzRequest is one request as the fuzz server received it.
type fuzzRequest struct {
	// URI is the request target exactly as it arrived, still escaped.
	URI string
	// Auth is the Authorization header it carried.
	Auth string
}

// fuzzServer is a GNS3 server whose every answer a fuzz input sets: 200
// with the body for a path it holds, 404 for any other. It records each
// request it receives.
type fuzzServer struct {
	*httptest.Server
	mu      sync.Mutex
	answers map[string][]byte
	seen    []fuzzRequest
}

// newFuzzServer starts a fuzz server, closed when tb ends.
func newFuzzServer(tb testing.TB) *fuzzServer {
	tb.Helper()
	s := &fuzzServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, fuzzRequest{URI: r.RequestURI, Auth: r.Header.Get("Authorization")})
		body, ok := s.answers[r.Method+" "+r.URL.EscapedPath()]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	tb.Cleanup(s.Close)
	return s
}

// set replaces every answer and forgets every request seen.
func (s *fuzzServer) set(answers map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers, s.seen = answers, nil
}

// requests returns the requests seen since the last set.
func (s *fuzzServer) requests() []fuzzRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fuzzRequest(nil), s.seen...)
}

// client returns a client of s logged in as creds.
func (s *fuzzServer) client(t *testing.T, creds Credentials) *Client {
	t.Helper()
	c, err := New(s.URL, s.Client(), creds)
	if err != nil {
		t.Fatalf("New(%q): %v", s.URL, err)
	}
	return c
}

// v2 is a 2.2 server's version answer, for the fuzz targets that need a
// connected client and fuzz what follows.
var v2 = map[string][]byte{"GET /v2/version": []byte(`{"version":"2.2.61"}`)}

// with returns answers and more, merged.
func with(answers map[string][]byte, more map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range answers {
		out[k] = v
	}
	for k, v := range more {
		out[k] = v
	}
	return out
}

// hostile are answers no real server sends, seeded into every target.
var hostile = []string{
	"", "\x00", "\xff\xfe", `"unbalanced`, `{"a":"b`, `[{"name":"x"}`, "}}]]", "-1", "null", "{}", "[]", "[null]",
	strings.Repeat("[", 5000), strings.Repeat(`{"a":`, 5000),
}

// FuzzVersion feeds Connect any version answer, on a 3.x server (with any
// login answer) or a 2.2 one. Connect succeeds exactly when the version
// names one and, on 3.x, the login holds a token; it returns the version
// as sent and the line it read it on; and the token, whatever its bytes,
// goes out as the next call's bearer and nothing else, or the call is
// refused before it is sent. A token cannot split the header it rides in.
func FuzzVersion(f *testing.F) {
	for _, s := range append([]string{
		`{"local":true,"version":"3.0.6"}`, `{"version":"2.2.61"}`, `{"version":""}`, `{"version":5}`,
		`{"version":"3.0.6","version":""}`, `{"version":"a\nb"}`, `"3.0.6"`,
	}, hostile...) {
		f.Add([]byte(s), false, []byte(`{"access_token":"tok","token_type":"bearer"}`))
		f.Add([]byte(s), true, []byte(`{"access_token":"tok","token_type":"bearer"}`))
	}
	f.Add([]byte(`{"version":"3.0.6"}`), true, []byte(`{"access_token":"tok\r\nX-Injected: 1"}`))
	f.Add([]byte(`{"version":"3.0.6"}`), true, []byte(`{"access_token":""}`))
	f.Add([]byte(`{"version":"3.0.6"}`), true, []byte(`{"access_token":" tok\t"}`))
	f.Add([]byte(`{"version":"3.0.6"}`), true, []byte(`{"access_token":7}`))
	srv := newFuzzServer(f)
	f.Fuzz(func(t *testing.T, version []byte, v3 bool, login []byte) {
		api, creds := APIv2, Credentials{}
		answers := map[string][]byte{"GET /v2/projects": []byte(`[]`), "GET /v3/projects": []byte(`[]`)}
		if v3 {
			api, creds = APIv3, Credentials{Username: "admin", Password: "admin"}
			answers["POST /v3/access/users/login"] = login
		}
		answers["GET /"+api+"/version"] = version
		srv.set(answers)
		c := srv.client(t, creds)
		got, err := c.Connect(context.Background())

		var want struct {
			Version string `json:"version"`
		}
		var token struct {
			AccessToken string `json:"access_token"`
		}
		ok := json.Unmarshal(version, &want) == nil && want.Version != ""
		if v3 {
			ok = ok && json.Unmarshal(login, &token) == nil && token.AccessToken != ""
		}
		if !ok {
			if err == nil {
				t.Fatalf("Connect accepted version %q, login %q: %+v", version, login, got)
			}
			return
		}
		if err != nil {
			t.Fatalf("Connect refused version %q: %v", want.Version, err)
		}
		if got.API != api || got.Version != want.Version {
			t.Fatalf("Connect = %+v, want API %s version %q", got, api, want.Version)
		}

		srv.set(answers)
		_, err = c.Projects(context.Background())
		sent := srv.requests()
		// The far side reads a header with its outer blanks trimmed.
		switch {
		case len(sent) == 0 && err == nil:
			t.Fatal("Projects succeeded with no request reaching the server")
		case len(sent) == 0:
			// Refused before it was sent: a token no header may carry.
		case len(sent) != 1:
			t.Fatalf("Projects sent %d requests", len(sent))
		case v3 && sent[0].Auth != strings.Trim("Bearer "+token.AccessToken, " \t"):
			t.Fatalf("the token %q went out as Authorization %q", token.AccessToken, sent[0].Auth)
		case !v3 && sent[0].Auth != "":
			t.Fatalf("a 2.2 server with no login was sent Authorization %q", sent[0].Auth)
		}
	})
}

// FuzzProjects feeds Projects any project list. It reads exactly what
// encoding/json reads into a Project and nothing else, and no more
// projects than the answer has bytes.
func FuzzProjects(f *testing.F) {
	for _, s := range append([]string{
		`[{"auto_close":true,"name":"lab","project_id":"5daa48ff-dbd6-407c-a3c6-645e743f233a","status":"closed"}]`,
		`[{"name":"a","project_id":"1"},{"name":"b","project_id":"2","status":"opened"}]`,
		`[1]`, `["lab"]`, `[{"name":5}]`, `[{"name":"x","name":"y"}]`, `[{"name":"\u001b[2J"}]`,
	}, hostile...) {
		f.Add([]byte(s))
	}
	srv := newFuzzServer(f)
	f.Fuzz(func(t *testing.T, raw []byte) {
		srv.set(v2)
		c := srv.client(t, Credentials{})
		if _, err := c.Connect(context.Background()); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		srv.set(with(v2, map[string][]byte{"GET /v2/projects": raw}))
		got, err := c.Projects(context.Background())
		var want []Project
		if json.Unmarshal(raw, &want) != nil {
			if err == nil {
				t.Fatalf("Projects accepted %q: %+v", raw, got)
			}
			return
		}
		if err != nil {
			t.Fatalf("Projects refused %q: %v", raw, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Projects = %+v, want %+v", got, want)
		}
		if len(got) > len(raw) {
			t.Fatalf("%d projects out of %d bytes", len(got), len(raw))
		}
	})
}

// FuzzNodes feeds Nodes any project id (in an import, the server's own
// word from the project list) and any node list. The id reaches the
// server as the one path segment url.PathEscape makes of it, or the call
// is refused before anything is sent; it never names another route. The
// nodes read are exactly what encoding/json reads into a Node.
func FuzzNodes(f *testing.F) {
	for _, id := range []string{"5daa48ff-dbd6-407c-a3c6-645e743f233a", "a b", "a/b", "..", "%2E%2E", "a%2F..%2Fb", "?x", "#", "\x00", "a\nb"} {
		f.Add(id, []byte(`[{"console":5000,"console_host":"0.0.0.0","console_type":"telnet","name":"R1","node_id":"n1","node_type":"qemu","status":"started"}]`))
	}
	for _, s := range append([]string{
		`[{"console":null,"console_type":"none","name":"SW1"}]`, `[{"console":0}]`, `[{"console":70000}]`,
		`[{"console":99999999999999999999}]`, `[{"console":5000.5}]`, `[{"console":"5000"}]`, `[{"console":5000,"console":-1}]`,
	}, hostile...) {
		f.Add("p", []byte(s))
	}
	srv := newFuzzServer(f)
	f.Fuzz(func(t *testing.T, project string, raw []byte) {
		srv.set(v2)
		c := srv.client(t, Credentials{})
		if _, err := c.Connect(context.Background()); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		route := "/v2/projects/" + url.PathEscape(project) + "/nodes"
		srv.set(with(v2, map[string][]byte{"GET " + route: raw}))
		got, err := c.Nodes(context.Background(), project)
		sent := srv.requests()
		if len(sent) == 0 {
			if err == nil {
				t.Fatalf("Nodes(%q) succeeded with no request sent", project)
			}
			return
		}
		if len(sent) != 1 || sent[0].URI != route {
			t.Fatalf("Nodes(%q) sent %+v, want one request for %s", project, sent, route)
		}
		var want []Node
		if json.Unmarshal(raw, &want) != nil {
			if err == nil {
				t.Fatalf("Nodes accepted %q: %+v", raw, got)
			}
			return
		}
		if err != nil {
			t.Fatalf("Nodes refused %q: %v", raw, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Nodes = %+v, want %+v", got, want)
		}
	})
}

// Tests of the GNS3 client against answers no real server gives: a base URL
// that is not one, an unknown API, an oversized or malformed answer.
package gns3_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
)

// TestNew_RefusesWhatIsNotABaseURL: a path, a query, credentials in the
// URL or a scheme other than http and https is refused before any call.
func TestNew_RefusesWhatIsNotABaseURL(t *testing.T) {
	for _, base := range []string{"", "ftp://gns3:3080", "http://", "http://gns3:3080/v3", "http://gns3:3080?x=1", "http://admin:pw@gns3:3080"} {
		if _, err := gns3.New(base, nil, gns3.Credentials{}); err == nil {
			t.Errorf("New(%q) accepted", base)
		}
	}
	if _, err := gns3.New("https://gns3.lab:3080/", nil, gns3.Credentials{}); err != nil {
		t.Errorf("a base URL with a trailing slash: %v", err)
	}
}

// serve answers each path with its status and body; anything else is 404.
func serve(t *testing.T, answers map[string][2]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var status int
		_, _ = fmt.Sscan(a[0], &status)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(a[1]))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestConnect_RefusesWhatItCannotRead: neither API line, a version answer
// naming nothing, an error status, a login with no token, and every call
// before Connect.
func TestConnect_RefusesWhatItCannotRead(t *testing.T) {
	ctx := context.Background()
	creds := gns3.Credentials{Username: "admin", Password: "x"}
	for name, tc := range map[string]struct {
		answers map[string][2]string
		want    string
	}{
		"no GNS3 at all":                          {map[string][2]string{}, "neither /v3/version nor /v2/version"},
		"a version naming none":                   {map[string][2]string{"GET /v3/version": {"200", `{}`}}, "names no version"},
		"a broken version":                        {map[string][2]string{"GET /v2/version": {"500", "boom\x1b[2J\nmore"}}, `500: "boom\x1b[2J more"`},
		"a login with no token":                   {map[string][2]string{"GET /v3/version": {"200", `{"version":"3.0.6"}`}, "POST /v3/access/users/login": {"200", `{}`}}, "no access token"},
		"a login that fails":                      {map[string][2]string{"GET /v3/version": {"200", `{"version":"3.0.6"}`}, "POST /v3/access/users/login": {"503", "down"}}, "503"},
		"a login answer that echoes the password": {map[string][2]string{"GET /v3/version": {"200", `{"version":"3.0.6"}`}, "POST /v3/access/users/login": {"422", `{"detail":[{"input":{"password":"x-ECHOED-SECRET"}}]}`}}, "422 (the answer is not shown"},
	} {
		c, err := gns3.New(serve(t, tc.answers), nil, creds)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Connect(ctx); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "ECHOED-SECRET") {
			t.Errorf("%s: Connect = %v, want it to say %q and never the login", name, err, tc.want)
		}
	}
	c, _ := gns3.New("http://127.0.0.1:1", nil, creds)
	if _, err := c.Projects(ctx); err == nil || !strings.Contains(err.Error(), "Connect first") {
		t.Errorf("Projects before Connect = %v", err)
	}
	if _, err := c.Connect(ctx); err == nil {
		t.Error("Connect to a closed port succeeded")
	}
}

// TestNodes_RefusesAnAnswerItCannotRead: a v2 server's nodes that are not
// a list, an error status (with the server's own JSON message), and an
// answer over the bound.
func TestNodes_RefusesAnAnswerItCannotRead(t *testing.T) {
	ctx := context.Background()
	huge := `[` + strings.Repeat(`{"name":"n"},`, 2<<20) + `{}]`
	base := serve(t, map[string][2]string{
		"GET /v2/version":            {"200", `{"version":"2.2.61"}`},
		"GET /v2/projects":           {"200", `[{"project_id":"p","name":"lab","status":"opened"}]`},
		"GET /v2/projects/p/nodes":   {"200", `{"not":"a list"}`},
		"GET /v2/projects/q/nodes":   {"403", "forbidden"},
		"GET /v2/projects/r/nodes":   {"404", "{\n    \"message\": \"Project ID r doesn't exist\",\n    \"status\": 404\n}"},
		"GET /v2/projects/big/nodes": {"200", huge},
	})
	c, err := gns3.New(base, nil, gns3.Credentials{Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Connect(ctx); err != nil || v.API != gns3.APIv2 {
		t.Fatalf("Connect = %+v, %v", v, err)
	}
	if p, err := c.Projects(ctx); err != nil || len(p) != 1 || p[0].Name != "lab" {
		t.Fatalf("Projects = %+v, %v", p, err)
	}
	for id, want := range map[string]string{"p": "not what this client reads", "q": "403", "big": "over",
		// A server's own error JSON gives its message, not its first line "{".
		"r": `404: "Project ID r doesn't exist"`} {
		if _, err := c.Nodes(ctx, id); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Nodes(%s) = %v, want it to say %q", id, err, want)
		}
	}
}

// Fuzz tests of what the client decodes from a controller it does not
// trust to be well formed: a node's configuration in either shape and the
// addresses read out of it; the version and the login; the lab list, with
// each lab id (the controller's own word) routed back into a path; and a
// lab's nodes with the addresses the controller observed on each. All but
// the first run against a real HTTP server in the process, one per fuzz
// worker, whose answers the input sets.
package cml

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

// FuzzConfigText feeds configText any bytes as a node's configuration, and
// the address pattern whatever text comes back: neither may panic, and
// every address the pattern finds is one netip reads or one Nodes drops.
func FuzzConfigText(f *testing.F) {
	for _, seed := range []string{
		`"hostname R1\ninterface E0/0\n ip address 10.0.0.1 255.255.255.0"`,
		`[{"name":"node.cfg","content":"ip addr add 10.10.20.177/24 dev eth0"}]`,
		`null`, `{}`, `[1,2]`, `"ifconfig eth1 999.1.1.1 up"`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		text := configText(json.RawMessage(raw))
		for _, m := range configAddress.FindAllStringSubmatch(text, -1) {
			if len(m) != 2 {
				t.Fatalf("a match with %d groups", len(m))
			}
			_, _ = netip.ParseAddr(m[1])
		}
	})
}

// fuzzRequest is one request as the fuzz controller received it.
type fuzzRequest struct {
	// Path is the request's path exactly as it arrived, still escaped.
	Path string
	// Auth is the Authorization header it carried.
	Auth string
}

// fuzzController is a controller whose every answer a fuzz input sets:
// 200 with the body for a path (under /api/v0, still escaped) it holds,
// and for "/labs/*" any one lab's read; 404 for any other. It records each
// request it receives.
type fuzzController struct {
	*httptest.Server
	mu      sync.Mutex
	answers map[string][]byte
	seen    []fuzzRequest
}

// newFuzzController starts a fuzz controller, closed when tb ends.
func newFuzzController(tb testing.TB) *fuzzController {
	tb.Helper()
	s := &fuzzController{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v0")
		s.mu.Lock()
		s.seen = append(s.seen, fuzzRequest{Path: path, Auth: r.Header.Get("Authorization")})
		body, ok := s.answers[path]
		if rest, lab := strings.CutPrefix(path, "/labs/"); !ok && lab && !strings.Contains(rest, "/") {
			body, ok = s.answers["/labs/*"]
		}
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
func (s *fuzzController) set(answers map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers, s.seen = answers, nil
}

// requests returns the requests seen since the last set.
func (s *fuzzController) requests() []fuzzRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fuzzRequest(nil), s.seen...)
}

// loggedIn is the answers a controller gives a login: ready, and a token.
var loggedIn = map[string][]byte{
	"/system_information": []byte(`{"version":"2.7.2+build.26","ready":true}`),
	"/authenticate":       []byte(`"tok"`),
}

// connected returns a client of s, logged in with loggedIn's answers.
func (s *fuzzController) connected(t *testing.T) *Client {
	t.Helper()
	s.set(loggedIn)
	c, err := New(s.URL, s.Client(), Credentials{Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}

// and returns loggedIn with more.
func and(more map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range loggedIn {
		out[k] = v
	}
	for k, v := range more {
		out[k] = v
	}
	return out
}

// hostile are answers no real controller sends, seeded into every target.
var hostile = []string{
	"", "\x00", "\xff\xfe", `"unbalanced`, `{"a":"b`, "}}]]", "-1", "null", "{}", "[]", "[null]",
	strings.Repeat("[", 5000), strings.Repeat(`{"a":`, 5000),
}

// FuzzConnect feeds Connect any system information and any login answer.
// It succeeds exactly when the controller names a version, says it is
// ready and answers the login with a token; it returns that version; and
// the token, whatever its bytes, goes out as the next call's bearer and
// nothing else, or the call is refused before it is sent. A token cannot
// split the header it rides in.
func FuzzConnect(f *testing.F) {
	for _, s := range append([]string{
		`{"version":"2.7.2+build.26","ready":true}`, `{"version":"2.7.2","ready":false}`, `{"version":"","ready":true}`,
		`{"version":7,"ready":true}`, `{"ready":"true","version":"2"}`,
	}, hostile...) {
		f.Add([]byte(s), []byte(`"tok"`))
	}
	for _, s := range append([]string{`"tok\r\nX-Injected: 1"`, `""`, `7`, `["tok"]`, `" tok "`, `"tÿk"`}, hostile...) {
		f.Add([]byte(`{"version":"2.7.2","ready":true}`), []byte(s))
	}
	srv := newFuzzController(f)
	f.Fuzz(func(t *testing.T, system, login []byte) {
		answers := map[string][]byte{"/system_information": system, "/authenticate": login, "/labs": []byte(`[]`)}
		srv.set(answers)
		c, err := New(srv.URL, srv.Client(), Credentials{Username: "u", Password: "p"})
		if err != nil {
			t.Fatal(err)
		}
		version, err := c.Connect(context.Background())
		var sys struct {
			Version string `json:"version"`
			Ready   bool   `json:"ready"`
		}
		var token string
		ok := json.Unmarshal(system, &sys) == nil && sys.Version != "" && sys.Ready && json.Unmarshal(login, &token) == nil && token != ""
		if !ok {
			if err == nil {
				t.Fatalf("Connect accepted system %q, login %q: %q", system, login, version)
			}
			return
		}
		if err != nil || version != sys.Version {
			t.Fatalf("Connect = %q, %v; want %q", version, err, sys.Version)
		}

		srv.set(answers)
		_, err = c.Labs(context.Background())
		sent := srv.requests()
		// The far side reads a header with its outer blanks trimmed.
		want := strings.Trim("Bearer "+token, " \t")
		switch {
		case len(sent) == 0 && err == nil:
			t.Fatal("Labs succeeded with no request reaching the controller")
		case len(sent) == 0:
			// Refused before it was sent: a token no header may carry.
		case len(sent) != 1 || sent[0].Auth != want:
			t.Fatalf("the token %q went out as %+v, want Authorization %q", token, sent, want)
		}
	})
}

// FuzzLabs feeds Labs any list of lab ids and any one answer for every
// lab's read. Each id reaches the controller as the one path segment
// url.PathEscape makes of it, or the call is refused before that read is
// sent; and the labs come back one per id, in the list's order, each with
// the id the list gave it, whatever id the lab's own answer claims.
func FuzzLabs(f *testing.F) {
	lab := []byte(`{"id":"other","lab_title":"cvd","state":"STARTED","node_count":2}`)
	for _, s := range append([]string{
		`["00000000-0000-4000-8000-000000000001"]`, `["a","b","a"]`, `["a b","a/b","?x","#"]`, `[".."]`, `["%2E%2E"]`,
		`["a%2F..%2Fb"]`, `["\u0000"]`, `[1]`, `[null]`,
	}, hostile...) {
		f.Add([]byte(s), lab)
	}
	for _, s := range append([]string{`{"lab_title":5}`, `{"node_count":"2"}`, `["x"]`}, hostile...) {
		f.Add([]byte(`["a"]`), []byte(s))
	}
	srv := newFuzzController(f)
	f.Fuzz(func(t *testing.T, ids, lab []byte) {
		c := srv.connected(t)
		srv.set(and(map[string][]byte{"/labs": ids, "/labs/*": lab}))
		got, err := c.Labs(context.Background())
		var wantIDs []string
		if json.Unmarshal(ids, &wantIDs) != nil {
			if err == nil {
				t.Fatalf("Labs accepted the list %q: %+v", ids, got)
			}
			return
		}
		// Every lab read sent reached the controller as its id, once
		// escaped, in the list's order.
		reads := srv.requests()[1:]
		if len(reads) > len(wantIDs) {
			t.Fatalf("Labs read %d labs for %d ids", len(reads), len(wantIDs))
		}
		for i, r := range reads {
			if want := "/labs/" + url.PathEscape(wantIDs[i]); r.Path != want {
				t.Fatalf("lab %q was read at %q, want %q", wantIDs[i], r.Path, want)
			}
		}
		if err != nil {
			return
		}
		if len(reads) != len(wantIDs) || len(got) != len(wantIDs) {
			t.Fatalf("Labs read %d labs and returned %d for %d ids", len(reads), len(got), len(wantIDs))
		}
		var want Lab
		if len(wantIDs) > 0 && json.Unmarshal(lab, &want) != nil {
			t.Fatalf("Labs accepted the lab answer %q", lab)
		}
		for i, l := range got {
			want.ID = wantIDs[i]
			if l != want {
				t.Fatalf("lab %d = %+v, want %+v", i, l, want)
			}
		}
	})
}

// FuzzNodes feeds Nodes any node list and any layer 3 address map. The
// nodes come back as encoding/json reads them, in order. A node's
// addresses are exactly the IPv4 addresses the map gives under that node's
// own id, sorted and each once: never one the map gives another node,
// whose credential would then go to the wrong machine. Its configured
// addresses are IPv4, never 0.0.0.0, sorted and each once.
func FuzzNodes(f *testing.F) {
	l3 := []byte(`{"n1":{"name":"R1","interfaces":{"aa":{"ip4":["10.10.20.171/24","bogus","10.10.20.171"]},"bb":{"ip4":["192.0.2.1","::1"]}}},"n2":{"interfaces":{"cc":{"ip4":["10.0.0.9/8"]}}}}`)
	nodes := []byte(`[{"id":"n1","label":"R1","node_definition":"iol-xe","state":"BOOTED","configuration":"interface E0/0\n ip address 10.10.20.171 255.255.255.0\n ip address 0.0.0.0 0.0.0.0"},{"id":"n2","label":"PC1","configuration":[{"name":"a","content":"ifconfig eth0 10.0.0.9 up"}]},{"id":"n3","label":"ext"}]`)
	f.Add(nodes, l3)
	for _, s := range append([]string{`[{"id":"n1"},{"id":"n1"}]`, `[{"id":5}]`, `[{"configuration":7}]`, `[{"id":"n1","addresses":["1.2.3.4"]}]`}, hostile...) {
		f.Add([]byte(s), l3)
	}
	for _, s := range append([]string{`{"n1":{"interfaces":{"a":{"ip4":["1.2.3.4","1.2.3.4/33","01.2.3.4","1.2.3.4/"]}}}}`, `{"n1":{"interfaces":{"a":{"ip4":"1.2.3.4"}}}}`, `{"n1":[]}`}, hostile...) {
		f.Add(nodes, []byte(s))
	}
	srv := newFuzzController(f)
	f.Fuzz(func(t *testing.T, nodesRaw, l3Raw []byte) {
		c := srv.connected(t)
		srv.set(and(map[string][]byte{"/labs/L/nodes": nodesRaw, "/labs/L/layer3_addresses": l3Raw}))
		got, err := c.Nodes(context.Background(), "L")
		var want []struct {
			ID             string `json:"id"`
			Label          string `json:"label"`
			NodeDefinition string `json:"node_definition"`
			State          string `json:"state"`
		}
		var observed map[string]struct {
			Interfaces map[string]struct {
				IP4 []string `json:"ip4"`
			} `json:"interfaces"`
		}
		if json.Unmarshal(nodesRaw, &want) != nil || json.Unmarshal(l3Raw, &observed) != nil {
			if err == nil {
				t.Fatalf("Nodes accepted nodes %q, addresses %q", nodesRaw, l3Raw)
			}
			return
		}
		if err != nil {
			t.Fatalf("Nodes refused nodes %q, addresses %q: %v", nodesRaw, l3Raw, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%d nodes, want %d", len(got), len(want))
		}
		for i, n := range got {
			w := want[i]
			if n.ID != w.ID || n.Label != w.Label || n.NodeDefinition != w.NodeDefinition || n.State != w.State {
				t.Fatalf("node %d = %+v, want %+v", i, n, w)
			}
			// The node's own addresses, as a set, built apart from the
			// client's way of building them.
			own := map[netip.Addr]bool{}
			for _, iface := range observed[w.ID].Interfaces {
				for _, s := range iface.IP4 {
					host, _, _ := strings.Cut(s, "/")
					if a, err := netip.ParseAddr(host); err == nil && a.Is4() {
						own[a] = true
					}
				}
			}
			ownSorted := make([]netip.Addr, 0, len(own))
			for a := range own {
				ownSorted = append(ownSorted, a)
			}
			sort.Slice(ownSorted, func(i, j int) bool { return ownSorted[i].Less(ownSorted[j]) })
			if len(n.Addresses) != len(ownSorted) {
				t.Fatalf("node %q has addresses %v, want %v", n.ID, n.Addresses, ownSorted)
			}
			for j := range ownSorted {
				if n.Addresses[j] != ownSorted[j] {
					t.Fatalf("node %q has addresses %v, want %v", n.ID, n.Addresses, ownSorted)
				}
			}
			for j, a := range n.ConfiguredAddresses {
				if !a.Is4() || a.IsUnspecified() || (j > 0 && !n.ConfiguredAddresses[j-1].Less(a)) {
					t.Fatalf("node %q has configured addresses %v", n.ID, n.ConfiguredAddresses)
				}
			}
		}
	})
}

// TestFuzzController_RoutesALabReadOnly: the fuzz controller's "/labs/*"
// answers a single lab's read and nothing under one, so FuzzNodes and
// FuzzLabs cannot pass on an answer meant for the other.
func TestFuzzController_RoutesALabReadOnly(t *testing.T) {
	srv := newFuzzController(t)
	srv.set(map[string][]byte{"/labs/*": []byte("lab")})
	for path, want := range map[string]string{"/api/v0/labs/x": "lab", "/api/v0/labs/x/nodes": "404 page not found\n"} {
		res, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(res.Body)
		_ = res.Body.Close()
		if b.String() != want {
			t.Errorf("GET %s = %q, want %q", path, b.String(), want)
		}
	}
}

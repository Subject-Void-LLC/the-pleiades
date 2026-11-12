// Tests that virt.clab's readers of a named lab or node answer absence as
// data (Phase 131, R4): a lab that is not deployed, a node it does not
// have, or a topology file the server does not hold is exists false (with
// an empty list where the method returns one) in a run and in a check, and
// a server that refuses, fails or cannot be reached still fails the task.
package clab_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/refusing"
)

// readerRoutes adds the routes the readers call to the stub: each answers
// 404 for a lab that is not deployed, as the real server's do (the topology
// routes for a lab with no topology file, deployed or not).
func readerRoutes(s *server, mux *http.ServeMux, reply func(http.ResponseWriter, int, any), authed func(http.HandlerFunc) http.HandlerFunc) {
	deployed := func(w http.ResponseWriter, r *http.Request) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.labs[r.PathValue("lab")]; !ok {
			reply(w, http.StatusNotFound, map[string]string{"error": "lab '" + r.PathValue("lab") + "' not found"})
			return false
		}
		return true
	}
	hasFile := func(w http.ResponseWriter, r *http.Request) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.labs[r.PathValue("lab")]; !ok && !s.files[r.PathValue("lab")] {
			reply(w, http.StatusNotFound, map[string]string{"error": "file not found"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/labs/{lab}/links", authed(func(w http.ResponseWriter, r *http.Request) {
		if deployed(w, r) {
			reply(w, 200, []map[string]any{{"type": "veth", "a": map[string]any{"node": "n1", "interface": "eth1"}, "b": map[string]any{"node": "n2", "interface": "eth1"}}})
		}
	}))
	mux.HandleFunc("GET /api/v1/labs/{lab}/interfaces", authed(func(w http.ResponseWriter, r *http.Request) {
		if deployed(w, r) {
			reply(w, 200, []map[string]any{{"name": "clab-" + r.PathValue("lab") + "-n1", "interfaces": []map[string]any{{"name": "eth1"}}}})
		}
	}))
	mux.HandleFunc("GET /api/v1/labs/{lab}/topology/yaml", authed(func(w http.ResponseWriter, r *http.Request) {
		if hasFile(w, r) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("name: " + r.PathValue("lab") + "\n"))
		}
	}))
	mux.HandleFunc("POST /api/v1/labs/{lab}/validate", authed(func(w http.ResponseWriter, r *http.Request) {
		if hasFile(w, r) {
			reply(w, 200, map[string]any{"valid": true, "nodes": 2, "links": 1})
		}
	}))
	mux.HandleFunc("POST /api/v1/labs/{lab}/capture/packetflix", authed(func(w http.ResponseWriter, r *http.Request) {
		if deployed(w, r) {
			reply(w, 200, map[string]any{"captures": []map[string]any{{"containerName": "clab-" + r.PathValue("lab") + "-n1", "interfaceNames": []string{"eth1"}, "packetflixUri": "packetflix://x"}}})
		}
	}))
	mux.HandleFunc("GET /api/v1/labs/{lab}/nodes/{node}/logs", authed(func(w http.ResponseWriter, r *http.Request) {
		if deployed(w, r) {
			reply(w, 200, map[string]string{"logs": "booted\n"})
		}
	}))
	mux.HandleFunc("GET /api/v1/labs/{lab}/nodes/{node}/browser-ports", authed(func(w http.ResponseWriter, r *http.Request) {
		if deployed(w, r) {
			reply(w, 200, map[string]any{"ports": []map[string]any{{"port": 8080}}})
		}
	}))
}

// reader is one reader of a named lab or node, with the parameters that
// name what lab1 holds and the list it returns, if any.
type reader struct {
	fqcn   string
	params map[string]any
	list   string
	// node is set for a reader of one node, which a missing node also
	// makes absent.
	node bool
}

// readers is every virt.clab reader of a named lab or node.
var readers = []reader{
	{"virt.clab.lab.links", map[string]any{"lab": "lab1"}, "links", false},
	{"virt.clab.lab.interfaces", map[string]any{"lab": "lab1"}, "interfaces", false},
	{"virt.clab.lab.topology", map[string]any{"lab": "lab1"}, "", false},
	{"virt.clab.lab.validate", map[string]any{"lab": "lab1"}, "", false},
	{"virt.clab.capture.packetflix", map[string]any{"lab": "lab1", "targets": []any{map[string]any{"container_name": "clab-lab1-n1", "interface_name": "eth1"}}}, "captures", false},
	{"virt.clab.node.logs", map[string]any{"lab": "lab1", "node": "n1"}, "", true},
	{"virt.clab.node.ports", map[string]any{"lab": "lab1", "node": "n1"}, "ports", true},
	{"virt.clab.netem.show", map[string]any{"lab": "lab1", "node": "n1"}, "interfaces", true},
}

// with is params with key set to value.
func with(params map[string]any, key string, value any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		out[k] = v
	}
	out[key] = value
	return out
}

// TestReaders_AnAbsentLabOrNodeIsExistsFalseInBothModes: each reader of a
// lab that is not deployed (and each node reader of a node lab1 does not
// have) answers exists false, with its list empty, and no error, in a run
// and a check alike; the control is the same call of what lab1 holds, which
// answers exists true. lab.topology and lab.validate read the lab's
// topology file, which a lab that was never deployed may still have.
func TestReaders_AnAbsentLabOrNodeIsExistsFalseInBothModes(t *testing.T) {
	// The readers table is exactly these, so none is left out of the loop
	// below (and internal/archtest's absence rule sees each named here).
	var names []string
	for _, r := range readers {
		names = append(names, r.fqcn)
	}
	if got, want := strings.Join(names, " "), strings.Join([]string{
		"virt.clab.lab.links", "virt.clab.lab.interfaces", "virt.clab.lab.topology", "virt.clab.lab.validate",
		"virt.clab.capture.packetflix", "virt.clab.node.logs", "virt.clab.node.ports", "virt.clab.netem.show",
	}, " "); got != want {
		t.Fatalf("readers are %s, want %s", got, want)
	}
	srv := newServer(t)
	srv.labs["lab1"] = map[string]string{"n1": "running", "n2": "running"}
	srv.files["undeployed"] = true
	dev := device(t, srv.URL, true)
	for _, r := range readers {
		for _, check := range []bool{false, true} {
			absent := []map[string]any{with(r.params, "lab", "ghost")}
			if r.node {
				absent = append(absent, with(r.params, "node", "n9"))
			}
			for _, params := range absent {
				c := login()
				res, err := run(t, r.fqcn, dev, c, params, check)
				if err != nil || res.Changed || c.stats["exists"] != false {
					t.Errorf("%s check=%v %v: err %v, stats %v; want exists false", r.fqcn, check, params, err, c.stats)
				}
				if r.list != "" {
					if l, ok := c.stats[r.list].([]any); !ok || len(l) != 0 {
						t.Errorf("%s check=%v: %s = %#v, want an empty list", r.fqcn, check, r.list, c.stats[r.list])
					}
				}
			}
			c := login()
			if _, err := run(t, r.fqcn, dev, c, r.params, check); err != nil || c.stats["exists"] != true {
				t.Errorf("%s check=%v of lab1: err %v, stats %v; want exists true", r.fqcn, check, err, c.stats)
			}
		}
	}
	for _, fqcn := range []string{"virt.clab.lab.topology", "virt.clab.lab.validate"} {
		c := login()
		if _, err := run(t, fqcn, dev, c, map[string]any{"lab": "undeployed"}, false); err != nil || c.stats["exists"] != true {
			t.Errorf("%s of a lab with a topology file and no deployment: err %v, stats %v; want exists true", fqcn, err, c.stats)
		}
	}
}

// TestReaders_ACannotLookFails: a server that refuses the login (403) or
// fails (500) on the reads a reader makes, and one nothing answers at, fail
// the task in a run and a check, and never report exists.
func TestReaders_ACannotLookFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refuse map[string]int
		down   bool
	}{
		{name: "403 on the lab", refuse: map[string]int{"/api/v1/labs/": 403}},
		{name: "500 on the lab", refuse: map[string]int{"/api/v1/labs/": 500}},
		{name: "nothing answers", down: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t)
			srv.labs["lab1"] = map[string]string{"n1": "running"}
			srv.refuse = tc.refuse
			base := srv.URL
			if tc.down {
				base = "http://" + refusing.Addr(t)
			}
			dev := device(t, base, true)
			for _, r := range readers {
				for _, check := range []bool{false, true} {
					for _, params := range []map[string]any{r.params, with(r.params, "lab", "ghost")} {
						c := login()
						_, err := run(t, r.fqcn, dev, c, params, check)
						if err == nil {
							t.Errorf("%s check=%v %v: no error, stats %v; a reader that could not look must fail", r.fqcn, check, params, c.stats)
						}
						if exists, ok := c.stats["exists"]; ok {
							t.Errorf("%s check=%v %v: reported exists %v", r.fqcn, check, params, exists)
						}
						if err != nil && strings.Contains(err.Error(), testPassword) {
							t.Errorf("%s: the error holds the password: %v", r.fqcn, err)
						}
					}
				}
			}
		})
	}
}

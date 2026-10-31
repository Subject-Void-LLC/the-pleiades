// Tests for the virt.clab methods against a server that speaks the shapes the
// real containerlab API server does (read off the real server running a lab:
// POST /login, inspect as a list of nodes, 404 {"error": ...} for a lab that
// is not there, and an exec that reports exit code 0 whatever the command
// did). The real server is clab_live_test.go's.
package clab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/virt/clab"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clabapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

const (
	testUser     = "pleiades"
	testPassword = "correct horse battery staple"
	testToken    = "tok-0123456789abcdef"
)

// rc is a RunbookContext holding the API login and what a method set.
type rc struct {
	secrets map[string]string
	stats   map[string]any
	project map[string]string
}

func (c *rc) InjectSecrets() map[string]string { return c.secrets }
func (c *rc) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *rc) EmitFact(string, any) error       { return nil }

// ReadProjectFile reads a file of the test's project.
func (c *rc) ReadProjectFile(name string, _ int64) ([]byte, error) {
	if body, ok := c.project[name]; ok {
		return []byte(body), nil
	}
	return nil, sdk.ErrNoProjectFiles
}

// login is the credential the stub accepts.
func login() *rc {
	return &rc{secrets: map[string]string{wire.SecretUsername: testUser, wire.SecretPassword: testPassword}, stats: map[string]any{}}
}

// server is a stand-in for the API server with a lab's state.
type server struct {
	*httptest.Server
	mu      sync.Mutex
	labs    map[string]map[string]string // lab -> node -> state
	netem   map[string][]map[string]any  // lab/node -> impairments
	calls   []string
	bearers []string
	logins  int
	// refuseFirstToken makes the stub, like the real server just after it
	// starts, answer 401 to the first token it issued.
	refuseFirstToken bool
	// edgeshark is whether the stub's Edgeshark runs.
	edgeshark bool
}

// newServer starts the stub on plain HTTP, which the device allows.
func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{labs: map[string]map[string]string{}, netem: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Username != testUser || in.Password != testPassword {
			reply(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password: " + in.Password})
			return
		}
		s.mu.Lock()
		s.logins++
		s.mu.Unlock()
		token := testToken
		if s.refuseFirstToken && s.logins == 1 {
			token = "stale-" + testToken
		}
		reply(w, 200, map[string]string{"token": token})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			s.calls = append(s.calls, r.Method+" "+r.URL.RequestURI())
			s.bearers = append(s.bearers, r.Header.Get("Authorization"))
			s.mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				reply(w, http.StatusUnauthorized, map[string]string{"error": "Authorization header required"})
				return
			}
			h(w, r)
		}
	}
	nodes := func(lab string) []map[string]any {
		var out []map[string]any
		for n, st := range s.labs[lab] {
			out = append(out, map[string]any{"name": "clab-" + lab + "-" + n, "nodeName": n, "kind": "linux", "image": "alpine", "state": st, "status": "x", "lab_name": lab, "container_id": "id" + n})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["nodeName"].(string) < out[j]["nodeName"].(string) })
		return out
	}
	mux.HandleFunc("GET /api/v1/labs/{lab}", authed(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		lab := r.PathValue("lab")
		if lab == "leaky" {
			// A server that quotes the bearer token it was sent in its error.
			reply(w, 500, map[string]string{"error": "failed for " + r.Header.Get("Authorization")})
			return
		}
		if _, ok := s.labs[lab]; !ok {
			reply(w, 404, map[string]string{"error": "lab '" + lab + "' not found"})
			return
		}
		reply(w, 200, nodes(lab))
	}))
	mux.HandleFunc("GET /api/v1/labs", authed(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := map[string]any{}
		for lab := range s.labs {
			out[lab] = nodes(lab)
		}
		reply(w, 200, out)
	}))
	mux.HandleFunc("POST /api/v1/labs", authed(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TopologyContent struct {
				Name     string `json:"name"`
				Topology struct {
					Nodes map[string]any `json:"nodes"`
				} `json:"topology"`
			} `json:"topologyContent"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		defer s.mu.Unlock()
		lab := map[string]string{}
		for n := range in.TopologyContent.Topology.Nodes {
			lab[n] = "running"
		}
		s.labs[in.TopologyContent.Name] = lab
		reply(w, 200, map[string]any{in.TopologyContent.Name: nodes(in.TopologyContent.Name)})
	}))
	mux.HandleFunc("DELETE /api/v1/labs/{lab}", authed(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.labs, r.PathValue("lab"))
		reply(w, 200, map[string]string{"message": "destroyed"})
	}))
	move := map[string]string{"start": "running", "stop": "exited", "pause": "paused", "unpause": "running", "restart": "running"}
	for verb, to := range move {
		mux.HandleFunc("POST /api/v1/labs/{lab}/nodes/{node}/"+verb, authed(func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			for n := range s.labs[r.PathValue("lab")] {
				if n == r.PathValue("node") || "clab-"+r.PathValue("lab")+"-"+n == r.PathValue("node") {
					s.labs[r.PathValue("lab")][n] = to
				}
			}
			reply(w, 200, map[string]string{"message": verb + "ed"})
		}))
	}
	// exec runs the command string as the real server does: split into words
	// as a shell would and run, reporting exit code 0 whatever happened.
	mux.HandleFunc("POST /api/v1/labs/{lab}/exec", authed(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Command string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		filter := r.URL.Query().Get("nodeFilter")
		out, errOut := runShell(in.Command)
		reply(w, 200, map[string]any{filter: []map[string]any{{"cmd": []string{}, "return-code": 0, "stdout": out, "stderr": errOut}}})
	}))
	mux.HandleFunc("GET /api/v1/tools/netem/show", authed(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		list := s.netem[r.URL.Query().Get("labName")+"/"+r.URL.Query().Get("nodeName")]
		if list == nil {
			list = []map[string]any{}
		}
		reply(w, 200, map[string]any{r.URL.Query().Get("nodeName"): list})
	}))
	mux.HandleFunc("POST /api/v1/tools/netem/set", authed(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		defer s.mu.Unlock()
		key := in["labName"].(string) + "/" + in["nodeName"].(string)
		entry := map[string]any{"interface": in["interface"], "delay": "", "jitter": "", "packet_loss": 0, "rate": 0}
		if v, ok := in["delay"]; ok {
			entry["delay"] = v
		}
		if v, ok := in["jitter"]; ok {
			entry["jitter"] = v
		}
		if v, ok := in["loss"]; ok {
			entry["packet_loss"] = v
		}
		if v, ok := in["rate"]; ok {
			entry["rate"] = v
		}
		var kept []map[string]any
		for _, e := range s.netem[key] {
			if e["interface"] != in["interface"] {
				kept = append(kept, e)
			}
		}
		s.netem[key] = append(kept, entry)
		reply(w, 200, map[string]string{"message": "set"})
	}))
	mux.HandleFunc("POST /api/v1/tools/netem/reset", authed(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		defer s.mu.Unlock()
		key := in["labName"].(string) + "/" + in["nodeName"].(string)
		var kept []map[string]any
		for _, e := range s.netem[key] {
			if e["interface"] != in["interface"] {
				kept = append(kept, e)
			}
		}
		s.netem[key] = kept
		reply(w, 200, map[string]string{"message": "reset"})
	}))
	mux.HandleFunc("GET /api/v1/tools/edgeshark/status", authed(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		reply(w, 200, map[string]any{"running": s.edgeshark, "runtime": "docker", "packetflixPort": 5001})
	}))
	for _, verb := range []string{"install", "uninstall"} {
		mux.HandleFunc("POST /api/v1/tools/edgeshark/"+verb, authed(func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.edgeshark = verb == "install"
			reply(w, 200, map[string]string{"message": verb + "ed"})
		}))
	}
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// runShell runs a command string through a local shell and returns its
// standard output and error: the server's exec, minus the container.
func runShell(command string) (string, string) {
	cmd := exec.Command("/bin/sh", "-c", command) // #nosec G204 -- a test's own stand-in for the server's exec
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	_ = cmd.Run() // the real server reports exit code 0 whatever the command did
	return out.String(), errOut.String()
}

// device is the API server as an onboarded generic_http device that
// onboarding recognised as a containerlab API server.
func device(t *testing.T, base string, recognised bool) inventory.InventoryItem {
	t.Helper()
	props := map[string]inventory.PropertyValue{generic.BaseURLProperty: base, generic.HTTPAuthProperty: httpapi.AuthBasic, httpapi.AllowPlaintextCredentialsProperty: true}
	caps := []capability.Name{capability.NameHTTPAPI}
	if recognised {
		caps = append(caps, capability.NameClabAPI)
	}
	d := inventory.Discovery{Protocol: "http", Capabilities: caps}
	d.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = d.Property()
	item, err := generic.NewHTTP(record.Record{ID: "clab-api", Name: "clab-api", Type: generic.TypeHTTP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// run calls a method in a mode.
func run(t *testing.T, fqcn string, dev inventory.InventoryItem, c *rc, params map[string]any, check bool) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	fn := d.Invoke
	if check {
		fn = d.Check
	}
	return fn(context.Background(), c, dev, params)
}

// recorded returns the undo c holds: its method and parameters.
func recorded(c *rc) (string, map[string]any, bool) {
	inv, ok := c.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		return "", nil, false
	}
	params, _ := inv[sdk.InverseParamsKey].(map[string]any)
	fqcn, _ := inv[sdk.InverseFQCNKey].(string)
	return fqcn, params, true
}

// TestEveryDocumentedMethodIsRegistered holds the reference text and the
// behavior to each other: every documented method is registered, implemented,
// checkable exactly when it says so, and a reversible one names the undos it
// may record.
func TestEveryDocumentedMethodIsRegistered(t *testing.T) {
	specs := clabapi.Specs()
	if len(specs) < 40 {
		t.Fatalf("%d methods documented", len(specs))
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if seen[spec.Name] {
			t.Errorf("%s is documented twice", spec.Name)
		}
		seen[spec.Name] = true
		d, ok := collection.Lookup(spec.Name)
		if !ok {
			t.Errorf("%s is documented and not registered", spec.Name)
			continue
		}
		m := d.Manifest
		if m.Status != collection.StatusImplemented || m.Reversibility.Notes == "" {
			t.Errorf("%s: status %v, reversibility notes %q", spec.Name, m.Status, m.Reversibility.Notes)
		}
		if m.SupportsCheck != (d.Check != nil) || (!m.SupportsCheck && m.NoCheckReason == "") {
			t.Errorf("%s: supports check %v, check %v, no-check reason %q", spec.Name, m.SupportsCheck, d.Check != nil, m.NoCheckReason)
		}
		if m.Reversibility.Reversible && len(m.Reversibility.Inverses) == 0 {
			t.Errorf("%s is reversible and names no undo", spec.Name)
		}
		if len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != capability.NameClabAPI {
			t.Errorf("%s requires %v", spec.Name, m.RequiredCapabilities)
		}
	}
}

// TestLabInfo_AbsentLabIsExistsFalseInBothModes: a lab that is not deployed
// is an answer, not a failure, in a run and in a check (virt.clab.lab.info).
func TestLabInfo_AbsentLabIsExistsFalseInBothModes(t *testing.T) {
	srv := newServer(t)
	dev := device(t, srv.URL, true)
	for _, check := range []bool{false, true} {
		c := login()
		res, err := run(t, "virt.clab.lab.info", dev, c, map[string]any{"lab": "ghost"}, check)
		if err != nil || res.Changed || c.stats["exists"] != false {
			t.Errorf("check=%v: changed %v, err %v, stats %v", check, res.Changed, err, c.stats)
		}
	}
}

// Failure-mode tests of import-gns3 against a GNS3 2.2 server, over real
// HTTP, that misbehaves partway through an import: one that stops
// answering, one whose answer is too large, a node renamed after the list,
// a console port no TCP connection reaches, and a project deleted between
// the project list and its nodes. Each import runs in this process, as the
// binary's own dispatch runs it, so the bounds can be shortened.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
)

// gns3Fake is a GNS3 2.2 server: projects by id and their nodes, answered
// in the shapes gns3-server 2.2.61 answers in. onCall, when set, runs
// first with mu held and answers the request itself when it returns true,
// through answer when it changes the server only after answering.
type gns3Fake struct {
	*httptest.Server
	mu       sync.Mutex
	projects map[string]string
	nodes    map[string][]gns3.Node
	calls    []string
	onCall   func(w http.ResponseWriter, r *http.Request) bool
}

// newGNS3Fake starts a server holding project "lab" (id p1) with nodes,
// closed when t ends.
func newGNS3Fake(t *testing.T, nodes ...gns3.Node) *gns3Fake {
	t.Helper()
	g := &gns3Fake{projects: map[string]string{"p1": "lab"}, nodes: map[string][]gns3.Node{"p1": nodes}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// serve answers one request.
func (g *gns3Fake) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, r.Method+" "+r.URL.Path)
	if g.onCall != nil && g.onCall(w, r) {
		return
	}
	g.answer(w, r)
}

// answer answers r from what g holds, with mu held.
func (g *gns3Fake) answer(w http.ResponseWriter, r *http.Request) {
	switch path := r.URL.Path; {
	case path == "/v2/version":
		_ = json.NewEncoder(w).Encode(map[string]any{"local": true, "version": "2.2.61"})
	case path == "/v2/projects":
		var out []gns3.Project
		for id, name := range g.projects {
			out = append(out, gns3.Project{ID: id, Name: name, Status: "opened"})
		}
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(path, "/v2/projects/") && strings.HasSuffix(path, "/nodes"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v2/projects/"), "/nodes")
		if _, ok := g.projects[id]; !ok {
			// gns3-server 2.2.61's own answer for a project it does not hold.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("{\n    \"message\": \"Project ID " + id + " doesn't exist\",\n    \"status\": 404\n}"))
			return
		}
		_ = json.NewEncoder(w).Encode(g.nodes[id])
	default:
		http.NotFound(w, r)
	}
}

// set runs f with g's lock held, to arrange what it holds.
func (g *gns3Fake) set(f func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f()
}

// telnetNode is a started VPCS node with a telnet console on every address.
func telnetNode(name string, console int) gns3.Node {
	return gns3.Node{ID: "id-" + name, Name: name, NodeType: "vpcs", Console: &console, ConsoleType: "telnet", ConsoleHost: "0.0.0.0", Status: "started"}
}

// labProject makes a project directory and returns it with a function that
// reads its inventory file's bytes.
func labProject(t *testing.T) (string, func() []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	return dir, func() []byte {
		b, err := os.ReadFile(filepath.Join(dir, inventory.DefaultInventoryFilename))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
}

// boundLab shortens labBounds for one test.
func boundLab(t *testing.T, header, call, handshake time.Duration) {
	t.Helper()
	saved := labBounds
	labBounds.header, labBounds.call, labBounds.handshake = header, call, handshake
	t.Cleanup(func() { labBounds = saved })
}

// finishes runs f and fails the test unless it returns inside limit: a
// bound that was taken away shows as an import still running.
func finishes(t *testing.T, limit time.Duration, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("the import was still running after %s: nothing bounds it", limit)
		return nil
	}
}

// TestImportGNS3_AServerThatStopsAnsweringIsCutOff: a server that takes
// the project list's request and then sends nothing is cut off by the
// header bound, and one that sends part of its answer and then nothing by
// the whole call's bound; either way the import fails and writes nothing.
// Each case sets only its own bound short, so it fails if that bound goes.
func TestImportGNS3_AServerThatStopsAnsweringIsCutOff(t *testing.T) {
	for name, tc := range map[string]struct {
		partway      bool
		header, call time.Duration
	}{
		"before its headers":         {false, time.Second, time.Minute},
		"partway through its answer": {true, time.Minute, 1500 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			boundLab(t, tc.header, tc.call, time.Minute)
			g := newGNS3Fake(t, telnetNode("pc1", 5000))
			g.set(func() {
				g.onCall = func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path != "/v2/projects" {
						return false
					}
					if tc.partway {
						_, _ = w.Write([]byte(`[{"project_id":"p1",`))
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
					return true
				}
			})
			dir, hosts := labProject(t)
			before := hosts()
			err := finishes(t, 20*time.Second, func() error {
				return runImportGNS3([]string{"--dir", dir, "--server", g.URL, "--project", "lab"})
			})
			if err == nil || !strings.Contains(err.Error(), "GET /v2/projects") {
				t.Fatalf("import-gns3 from a server that stopped answering = %v, want an error naming the call", err)
			}
			if string(hosts()) != string(before) {
				t.Error("the import wrote the inventory after it failed")
			}
		})
	}
}

// TestImportGNS3_AnOversizedAnswerWritesNothing: a node list past the
// client's bound fails the import, and the inventory is as it was.
func TestImportGNS3_AnOversizedAnswerWritesNothing(t *testing.T) {
	g := newGNS3Fake(t, telnetNode("pc1", 5000))
	huge := []byte(`[` + strings.Repeat(" ", 17<<20) + `]`)
	g.set(func() {
		g.onCall = func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/nodes") {
				return false
			}
			_, _ = w.Write(huge)
			return true
		}
	})
	dir, hosts := labProject(t)
	before := hosts()
	if err := runImportGNS3([]string{"--dir", dir, "--server", g.URL, "--project", "lab"}); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("import-gns3 of an oversized node list = %v, want it refused as over the bound", err)
	}
	if string(hosts()) != string(before) {
		t.Error("the import wrote the inventory after it failed")
	}
}

// TestImportGNS3_ANodeRenamedAfterTheListIsImportedAsListed: the nodes are
// read once, so a node renamed on the server after that read is imported
// as the one answer named it, at that answer's console, never under one
// answer's name with another's port.
func TestImportGNS3_ANodeRenamedAfterTheListIsImportedAsListed(t *testing.T) {
	g := newGNS3Fake(t, telnetNode("pc1", 5000))
	g.set(func() {
		g.onCall = func(w http.ResponseWriter, r *http.Request) bool {
			if !strings.HasSuffix(r.URL.Path, "/nodes") {
				return false
			}
			// Answered as it stands, then renamed and moved at once.
			g.answer(w, r)
			g.nodes["p1"] = []gns3.Node{telnetNode("pc1-renamed", 5009)}
			return true
		}
	})
	dir, _ := labProject(t)
	if err := runImportGNS3([]string{"--dir", dir, "--server", g.URL, "--project", "lab"}); err != nil {
		t.Fatal(err)
	}
	got, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "pc1" || got[0].Properties["telnet_port"] != 5000 {
		t.Errorf("the inventory = %+v, want pc1 at its listed console 5000 alone", got)
	}
	reads := 0
	g.set(func() {
		for _, c := range g.calls {
			if strings.HasSuffix(c, "/nodes") {
				reads++
			}
		}
	})
	if reads != 1 {
		t.Errorf("the import read the nodes %d times, want once", reads)
	}
}

// TestImportGNS3_AConsolePortNoTCPConnectionReachesIsSkipped: a node whose
// console the server says is port 0, -1 or 70000 is skipped and reported
// with that port, and the node beside it with a real port is imported.
func TestImportGNS3_AConsolePortNoTCPConnectionReachesIsSkipped(t *testing.T) {
	g := newGNS3Fake(t, telnetNode("ok", 5000), telnetNode("zero", 0), telnetNode("negative", -1), telnetNode("high", 70000))
	dir, _ := labProject(t)
	var err error
	out := captureStdout(t, func() {
		err = runImportGNS3([]string{"--dir", dir, "--server", g.URL, "--project", "lab", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var res gns3Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out)
	}
	want := map[string]string{"ok": "", "zero": "console port 0 ", "negative": "console port -1 ", "high": "console port 70000 "}
	for _, n := range res.Nodes {
		if w := want[n.Device]; (w == "") != (n.Skipped == "") || !strings.Contains(n.Skipped, w) {
			t.Errorf("node %s: skipped %q, want %q", n.Device, n.Skipped, w)
		}
	}
	hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 4 || len(hosts) != 1 || hosts[0].Name != "ok" {
		t.Errorf("reported %+v and imported %+v, want four reported and ok alone imported", res.Nodes, hosts)
	}
}

// TestImportGNS3_AProjectDeletedMidImportWritesNothing: a project deleted
// after the project list named it and before its nodes are read fails the
// import with the server's own words, and the inventory is as it was.
func TestImportGNS3_AProjectDeletedMidImportWritesNothing(t *testing.T) {
	g := newGNS3Fake(t, telnetNode("pc1", 5000))
	g.set(func() {
		g.onCall = func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/v2/projects" {
				return false
			}
			// Listed as it stands, then deleted.
			g.answer(w, r)
			delete(g.projects, "p1")
			return true
		}
	})
	dir, hosts := labProject(t)
	before := hosts()
	err := runImportGNS3([]string{"--dir", dir, "--server", g.URL, "--project", "lab"})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "Project ID p1 doesn't exist") {
		t.Fatalf("import-gns3 of a project deleted mid-import = %v, want the server's 404", err)
	}
	if string(hosts()) != string(before) {
		t.Error("the import wrote the inventory after it failed")
	}
}

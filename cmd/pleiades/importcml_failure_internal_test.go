// Failure-mode tests of import-cml against cmltest's controller, over real
// TLS, that misbehaves partway through an import: one that never finishes
// a TLS handshake or stops answering, one whose answer is too large, a
// node renamed or replaced between the node list and its console key, and
// a lab deleted mid-import. Each import runs in this process, as the
// binary's own dispatch runs it, so the bounds can be shortened.
package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// cmlLab starts a controller holding lab "cvd" with an IOL XE node for
// each label, logged in as admin, and returns it with the lab's id and
// each node's id by label.
func cmlLab(t *testing.T, labels ...string) (*cmltest.Controller, string, map[string]string) {
	t.Helper()
	ctl := cmltest.New(t, "admin", "pw")
	c, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := c.CreateLab(ctx, "cvd")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, label := range labels {
		if ids[label], err = c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"}); err != nil {
			t.Fatal(err)
		}
	}
	return ctl, lab.ID, ids
}

// cmlFlags are an import's flags for ctl by --server: its certificate
// pinned, the password from the environment, and R1 at an address given.
func cmlFlags(t *testing.T, dir, server, pin string) []string {
	t.Helper()
	t.Setenv(cmlPasswordEnv, "pw")
	return []string{"--dir", dir, "--server", server, "--username", "admin", "--tls-fingerprint", pin, "--lab", "cvd", "--address", "R1=10.0.0.1"}
}

// cmlOnCall sets ctl's OnCall under its lock.
func cmlOnCall(ctl *cmltest.Controller, f func(w http.ResponseWriter, r *http.Request) bool) {
	ctl.Mu.Lock()
	defer ctl.Mu.Unlock()
	ctl.OnCall = f
}

// TestImportCML_AControllerThatStopsAnsweringIsCutOff: a controller that
// takes the connection and never finishes the TLS handshake is cut off by
// the handshake bound; one that takes the lab list's request and sends
// nothing, by the header bound; one that sends part of its answer and then
// nothing, by the whole call's. Each case sets only its own bound short,
// so it fails if that bound goes; each import fails and writes nothing.
func TestImportCML_AControllerThatStopsAnsweringIsCutOff(t *testing.T) {
	const short, long = time.Second, time.Minute
	t.Run("in the TLS handshake", func(t *testing.T) {
		boundLab(t, long, long, short)
		// A listener that takes each connection and says nothing on it.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var conns []net.Conn
		t.Cleanup(func() {
			_ = ln.Close()
			mu.Lock()
			defer mu.Unlock()
			for _, c := range conns {
				_ = c.Close()
			}
		})
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				conns = append(conns, conn)
				mu.Unlock()
			}
		}()
		dir, hosts := labProject(t)
		before := hosts()
		args := cmlFlags(t, dir, "https://"+ln.Addr().String(), "sha256:"+strings.Repeat("0", 64))
		err = finishes(t, 20*time.Second, func() error { return runImportCML(args) })
		if err == nil || !strings.Contains(err.Error(), "handshake") {
			t.Fatalf("import-cml from a controller that never finished the handshake = %v", err)
		}
		if string(hosts()) != string(before) {
			t.Error("the import wrote the inventory after it failed")
		}
	})
	for name, tc := range map[string]struct {
		partway      bool
		header, call time.Duration
	}{
		"before its headers":         {false, short, long},
		"partway through its answer": {true, long, 1500 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			boundLab(t, tc.header, tc.call, long)
			ctl, _, _ := cmlLab(t, "R1")
			cmlOnCall(ctl, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v0/labs" {
					return false
				}
				if tc.partway {
					_, _ = w.Write([]byte(`["00000000-`))
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				return true
			})
			dir, hosts := labProject(t)
			before := hosts()
			args := cmlFlags(t, dir, ctl.URL, devicetls.Fingerprint(ctl.Certificate()))
			err := finishes(t, 20*time.Second, func() error { return runImportCML(args) })
			if err == nil || !strings.Contains(err.Error(), "GET /api/v0/labs") {
				t.Fatalf("import-cml from a controller that stopped answering = %v, want an error naming the call", err)
			}
			if string(hosts()) != string(before) {
				t.Error("the import wrote the inventory after it failed")
			}
		})
	}
}

// TestImportCML_AnOversizedAnswerWritesNothing: a node list past the
// client's bound fails the import, and the inventory is as it was.
func TestImportCML_AnOversizedAnswerWritesNothing(t *testing.T) {
	ctl, lab, _ := cmlLab(t, "R1")
	huge := []byte(`[` + strings.Repeat(" ", 33<<20) + `]`)
	cmlOnCall(ctl, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v0/labs/"+lab+"/nodes" {
			return false
		}
		_, _ = w.Write(huge)
		return true
	})
	dir, hosts := labProject(t)
	before := hosts()
	if err := runImportCML(cmlFlags(t, dir, ctl.URL, devicetls.Fingerprint(ctl.Certificate()))); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("import-cml of an oversized node list = %v, want it refused as over the bound", err)
	}
	if string(hosts()) != string(before) {
		t.Error("the import wrote the inventory after it failed")
	}
}

// TestImportCML_ALabDeletedMidImport: the lab being imported, deleted
// after the lab list named it, fails the import with the controller's 404
// and writes nothing, whether it goes before its nodes are read or between
// its nodes and their addresses. Another lab deleted between the list and
// its own read is left out, and the import goes on: it used to fail too.
func TestImportCML_ALabDeletedMidImport(t *testing.T) {
	for name, after := range map[string]string{"before its nodes": "", "between its nodes and their addresses": "/nodes"} {
		t.Run(name, func(t *testing.T) {
			ctl, lab, _ := cmlLab(t, "R1")
			cmlOnCall(ctl, func(_ http.ResponseWriter, r *http.Request) bool {
				// OnCall runs before a call is answered, so the lab is deleted
				// as the call that then finds it gone arrives.
				switch {
				case after == "" && r.URL.Path == "/api/v0/labs/"+lab+"/nodes",
					after == "/nodes" && r.URL.Path == "/api/v0/labs/"+lab+"/layer3_addresses":
					delete(ctl.Labs, lab)
				}
				return false
			})
			dir, hosts := labProject(t)
			before := hosts()
			err := runImportCML(cmlFlags(t, dir, ctl.URL, devicetls.Fingerprint(ctl.Certificate())))
			if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "lab not found") {
				t.Fatalf("import-cml of a lab deleted mid-import = %v, want the controller's 404", err)
			}
			if string(hosts()) != string(before) {
				t.Error("the import wrote the inventory after it failed")
			}
		})
	}
	t.Run("another lab, between the list and its read", func(t *testing.T) {
		ctl, lab, _ := cmlLab(t, "R1")
		ctl.Mu.Lock()
		// Its id sorts after the imported lab's, so the controller lists it
		// second and it is read after the imported lab is.
		ctl.Labs["ffffffff-0000-4000-8000-000000000000"] = &cmltest.Lab{ID: "ffffffff-0000-4000-8000-000000000000", Title: "theirs", Nodes: map[string]*cmltest.Node{}, Links: map[string]*cmltest.Link{}}
		ctl.Mu.Unlock()
		cmlOnCall(ctl, func(_ http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/api/v0/labs/"+lab {
				delete(ctl.Labs, "ffffffff-0000-4000-8000-000000000000")
			}
			return false
		})
		dir, _ := labProject(t)
		if err := runImportCML(cmlFlags(t, dir, ctl.URL, devicetls.Fingerprint(ctl.Certificate()))); err != nil {
			t.Fatalf("import-cml with another lab deleted mid-import = %v", err)
		}
		hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
		if err != nil || len(hosts) != 1 || hosts[0].Name != "R1" {
			t.Errorf("the inventory = %+v, %v; want R1", hosts, err)
		}
	})
}

// cmlConsoleProject is a project whose cml_controller device "cml" stands
// for ctl, with ctl's login stored, for an import --via console.
func cmlConsoleProject(t *testing.T, ctl *cmltest.Controller) (string, func() []byte) {
	t.Helper()
	dir, hosts := labProject(t)
	if err := runAddHost([]string{"cml", "--dir", dir, "--type", "cml_controller", "--set", "api_base=" + ctl.URL, "--set", "tls_fingerprint=" + devicetls.Fingerprint(ctl.Certificate())}); err != nil {
		t.Fatal(err)
	}
	if err := runAddCredential([]string{"cml", "--dir", dir, "--username", "admin", "--password", "pw"}); err != nil {
		t.Fatal(err)
	}
	return dir, hosts
}

// TestImportCML_ANodeRenamedOrReplacedMidImport: through the console, each
// node's line is read by the node's id after the node list. A node renamed
// in between is imported under its listed label with its own line, since
// its id is the same node; a node deleted and another made under its label
// fails the import, and not even the node before it is written.
func TestImportCML_ANodeRenamedOrReplacedMidImport(t *testing.T) {
	t.Run("renamed", func(t *testing.T) {
		ctl, lab, ids := cmlLab(t, "R1")
		cmlOnCall(ctl, func(_ http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/keys/console") {
				ctl.Labs[lab].Nodes[ids["R1"]].Label = "R1-renamed"
			}
			return false
		})
		dir, _ := cmlConsoleProject(t, ctl)
		if err := runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--via", "console"}); err != nil {
			t.Fatal(err)
		}
		hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
		if err != nil {
			t.Fatal(err)
		}
		// cmltest derives a node's console key from its id.
		line := ""
		for _, h := range hosts {
			if h.Name == "R1" {
				line, _ = h.Properties["console_line"].(string)
			}
		}
		if want := "11111111-2222-4333-8444-" + ids["R1"][len(ids["R1"])-12:]; line != want {
			t.Errorf("R1's console line = %q, want its own node's %q (inventory %+v)", line, want, hosts)
		}
	})
	t.Run("replaced", func(t *testing.T) {
		ctl, lab, ids := cmlLab(t, "R1", "R2")
		cmlOnCall(ctl, func(_ http.ResponseWriter, r *http.Request) bool {
			// As R1's line is read, R2 is deleted and another node made in its
			// place under its label.
			if strings.HasSuffix(r.URL.Path, ids["R1"]+"/keys/console") {
				delete(ctl.Labs[lab].Nodes, ids["R2"])
				ctl.Labs[lab].Nodes["00000000-0000-4000-8000-000000000999"] = &cmltest.Node{ID: "00000000-0000-4000-8000-000000000999", Label: "R2", Definition: "iol-xe", State: "DEFINED_ON_CORE"}
			}
			return false
		})
		dir, hosts := cmlConsoleProject(t, ctl)
		before := hosts()
		err := runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--via", "console"})
		if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "node not found") {
			t.Fatalf("import-cml with a node replaced mid-import = %v, want the controller's 404", err)
		}
		if string(hosts()) != string(before) {
			t.Error("the import wrote part of the lab after it failed")
		}
	})
}

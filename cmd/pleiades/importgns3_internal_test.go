// Tests of import-gns3's decisions and the lab importers' shared rules,
// each on the values a server or a person could hand them.
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// port returns a pointer to p, as a node's console is decoded.
func port(p int) *int { return &p }

// TestGNS3Console_ReachesOnlyWhatThisMachineCan: a telnet console on every
// address is the server's host, one on a named address is that address,
// and a node with another console, no port, a port out of range, an
// unreadable host or a console on the server's own loopback is skipped
// saying why.
func TestGNS3Console_ReachesOnlyWhatThisMachineCan(t *testing.T) {
	for name, tc := range map[string]struct {
		node       gns3.Node
		server     string
		host, skip string
		port       int
	}{
		"every address":           {gns3.Node{ConsoleType: "telnet", Console: port(5000), ConsoleHost: "0.0.0.0"}, "gns3.lab", "gns3.lab", "", 5000},
		"every IPv6 address":      {gns3.Node{ConsoleType: "telnet", Console: port(5001), ConsoleHost: "::"}, "10.0.0.5", "10.0.0.5", "", 5001},
		"a remote compute":        {gns3.Node{ConsoleType: "telnet", Console: port(5002), ConsoleHost: "10.0.0.9"}, "10.0.0.5", "10.0.0.9", "", 5002},
		"loopback, server here":   {gns3.Node{ConsoleType: "telnet", Console: port(5003), ConsoleHost: "127.0.0.1"}, "127.0.0.1", "127.0.0.1", "", 5003},
		"loopback, server remote": {gns3.Node{ConsoleType: "telnet", Console: port(5003), ConsoleHost: "127.0.0.1"}, "gns3.lab", "", "only the server reaches", 0},
		"a VNC console":           {gns3.Node{ConsoleType: "vnc", Console: port(5900)}, "gns3.lab", "", `"vnc", not telnet`, 0},
		"no console":              {gns3.Node{ConsoleType: ""}, "gns3.lab", "", `"none", not telnet`, 0},
		"no port":                 {gns3.Node{ConsoleType: "telnet"}, "gns3.lab", "", "no console port", 0},
		"a port out of range":     {gns3.Node{ConsoleType: "telnet", Console: port(70000)}, "gns3.lab", "", "no console port", 0},
		"an unreadable host":      {gns3.Node{ConsoleType: "telnet", Console: port(5000), ConsoleHost: "a b\x1b"}, "gns3.lab", "", "not an address or a host name", 0},
	} {
		host, p, skip := gns3Console(tc.node, tc.server)
		if host != tc.host || p != tc.port || !strings.Contains(skip, tc.skip) || (tc.skip == "") != (skip == "") {
			t.Errorf("%s: gns3Console = %q, %d, %q; want %q, %d, %q", name, host, p, skip, tc.host, tc.port, tc.skip)
		}
	}
}

// TestGNS3Select_TakesOnlyNamesADeviceCan: every node sorted, the named
// ones only, and a refusal for a name a device cannot take, a name two
// nodes share, or a node the project lacks.
func TestGNS3Select_TakesOnlyNamesADeviceCan(t *testing.T) {
	nodes := []gns3.Node{{Name: "r2"}, {Name: "r1"}, {Name: "pc1"}}
	got, err := gns3Select(nodes, "lab", "")
	if err != nil || len(got) != 3 || got[0].Name != "pc1" || got[2].Name != "r2" {
		t.Fatalf("every node = %+v, %v", got, err)
	}
	if got, err := gns3Select(nodes, "lab", "r2, r1"); err != nil || len(got) != 2 || got[0].Name != "r1" {
		t.Errorf("--nodes r2,r1 = %+v, %v", got, err)
	}
	for name, tc := range map[string]struct {
		nodes []gns3.Node
		only  string
		want  string
	}{
		"a node it lacks":     {nodes, "r9", `no node "r9"`},
		"a name with a space": {[]gns3.Node{{Name: "PC 1"}}, "", "not a name a device can take"},
		"a shared name":       {[]gns3.Node{{Name: "r1"}, {Name: "r1"}}, "", "two nodes"},
	} {
		if _, err := gns3Select(tc.nodes, "lab", tc.only); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// TestGNS3Pick_NamesOneProjectOnly: an id names a project exactly, a name
// must name one, and a name two projects share is refused.
func TestGNS3Pick_NamesOneProjectOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/version":
			http.NotFound(w, r)
		case "/v2/version":
			_, _ = w.Write([]byte(`{"version":"2.2.61"}`))
		case "/v2/projects":
			_, _ = w.Write([]byte(`[{"project_id":"a","name":"lab"},{"project_id":"b","name":"twin"},{"project_id":"c","name":"twin"}]`))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := gns3.New(srv.URL, srv.Client(), gns3.Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	for want, id := range map[string]string{"lab": "a", "c": "c"} {
		if p, err := gns3Pick(ctx, c, want); err != nil || p.ID != id {
			t.Errorf("gns3Pick(%q) = %+v, %v; want %s", want, p, err, id)
		}
	}
	if _, err := gns3Pick(ctx, c, "twin"); err == nil || !strings.Contains(err.Error(), "by its id") {
		t.Errorf("a shared name: %v", err)
	}
	if _, err := gns3Pick(ctx, c, "none"); err == nil || !strings.Contains(err.Error(), `no project "none"`) {
		t.Errorf("an unknown name: %v", err)
	}
}

// TestLabPassword_NeverFromAFlag: the first line of standard input, its
// line ending dropped and bounded, or else the environment variable.
func TestLabPassword_NeverFromAFlag(t *testing.T) {
	t.Setenv("PLEIADES_TEST_LAB_PASSWORD", "from-env")
	if pw, err := labPassword(false, "PLEIADES_TEST_LAB_PASSWORD", strings.NewReader("ignored\n")); err != nil || pw != "from-env" {
		t.Errorf("from the environment = %q, %v", pw, err)
	}
	if pw, err := labPassword(true, "PLEIADES_TEST_LAB_PASSWORD", strings.NewReader("s3cret\r\nsecond line\n")); err != nil || pw != "s3cret" {
		t.Errorf("from standard input = %q, %v", pw, err)
	}
	if pw, err := labPassword(true, "x", strings.NewReader("no newline")); err != nil || pw != "no newline" {
		t.Errorf("a last line with no newline = %q, %v", pw, err)
	}
	if _, err := labPassword(true, "x", strings.NewReader(strings.Repeat("x", maxPassword+10))); err == nil {
		t.Error("a password over the bound was read")
	}
}

// TestLabPlaintext_AskedForByName: a password over plain HTTP reaches only
// this machine without --allow-plaintext, and with it the run warns.
func TestLabPlaintext_AskedForByName(t *testing.T) {
	parse := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	for _, ok := range []string{"https://gns3.lab:3080", "http://127.0.0.1:3080", "http://localhost:3080", "http://[::1]:3080"} {
		if w, err := labPlaintext(parse(ok), true, false); err != nil || w != "" {
			t.Errorf("%s: %q, %v; want no refusal and no warning", ok, w, err)
		}
	}
	if w, err := labPlaintext(parse("http://gns3.lab:3080"), false, false); err != nil || w != "" {
		t.Errorf("no password: %q, %v", w, err)
	}
	if _, err := labPlaintext(parse("http://gns3.lab:3080"), true, false); err == nil || !strings.Contains(err.Error(), "--allow-plaintext") {
		t.Errorf("a password over plain HTTP: %v", err)
	}
	if w, err := labPlaintext(parse("http://gns3.lab:3080"), true, true); err != nil || !strings.Contains(w, "unencrypted") {
		t.Errorf("allowed: %q, %v; want a warning", w, err)
	}
}

// TestLabPlace_ReplacesOnlyWhenAskedAndOnlyItsOwnType: a new node is added,
// one already there is refused, replaced keeping its identity, tags and
// other properties when asked, and never replaced across types; a node its
// type refuses is not written.
func TestLabPlace_ReplacesOnlyWhenAskedAndOnlyItsOwnType(t *testing.T) {
	spec := func(name, typ string, props map[string]pkginventory.PropertyValue) inventory.HostSpec {
		return inventory.HostSpec{ID: "new-" + name, Name: name, Type: typ, Tags: []string{"gns3"}, Properties: props}
	}
	hosts := []inventory.HostSpec{{ID: "old", Name: "pc1", Type: gns3DeviceType, Tags: []string{"mine"}, Properties: map[string]pkginventory.PropertyValue{"telnet_host": "10.0.0.1", "telnet_port": 5000, "note": "kept"}}}
	moved := spec("pc1", gns3DeviceType, map[string]pkginventory.PropertyValue{"telnet_host": "10.0.0.2", "telnet_port": 5001})
	if _, err := labPlace(hosts, moved, false); err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Errorf("without --replace: %v", err)
	}
	got, err := labPlace(hosts, moved, true)
	if err != nil || len(got) != 1 || got[0].ID != "old" || got[0].Tags[0] != "mine" || got[0].Properties["note"] != "kept" || got[0].Properties["telnet_host"] != "10.0.0.2" {
		t.Fatalf("a replacement = %+v, %v", got, err)
	}
	if _, err := labPlace(got, spec("pc1", "generic_ssh", map[string]pkginventory.PropertyValue{"host": "10.0.0.3"}), true); err == nil || !strings.Contains(err.Error(), "cannot replace") {
		t.Errorf("across types: %v", err)
	}
	if got, err := labPlace(got, spec("pc2", gns3DeviceType, map[string]pkginventory.PropertyValue{"telnet_host": "10.0.0.4", "telnet_port": 5002}), false); err != nil || len(got) != 2 {
		t.Errorf("a new node = %+v, %v", got, err)
	}
	if _, err := labPlace(got, spec("pc3", gns3DeviceType, map[string]pkginventory.PropertyValue{"telnet_host": "10.0.0.4", "telnet_port": 0}), false); err == nil {
		t.Error("a node its type refuses was placed")
	}
}

// Package restconflive: what rousette says about fetching YANG sources, held to
// what was measured on 2026-11-16, so a client that finds no source URL there
// is reading the server rightly: rousette serves sources under /yang/ behind
// its login, and names no location for them in either library (RFC 8525's
// location, RFC 7895's schema leaf), which RFC 8040 section 3.7 has a server
// serving sources name. Its NETCONF listener, over the same sysrepo, answers
// get-schema for each.
package restconflive

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// TestLive_RousetteNamesNoSourceLocation reads both library resources and
// finds no location, and the same modules served over NETCONF's get-schema.
func TestLive_RousetteNamesNoSourceLocation(t *testing.T) {
	s := start(t)
	c := client(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, err := c.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := c.StateSchemas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Schemas) == 0 || len(state) == 0 {
		t.Fatalf("schemas: %d from yang-library, %d from modules-state; want rousette's modules in both", len(d.Schemas), len(state))
	}
	for _, list := range [][]string{locations(d.Schemas), locations(state)} {
		if len(list) != 0 {
			t.Fatalf("rousette names locations %v; this test pins that it names none, so a client finds nothing to fetch", list)
		}
	}
	ns := session(t, s)
	if !ns.CanGetSchema() {
		t.Fatal("the same server's NETCONF offers no get-schema")
	}
	for _, e := range d.Schemas[:3] {
		if _, err := ns.GetSchema(ctx, e.Name, e.Revision, 16<<20); err != nil {
			t.Errorf("get-schema of %s over the same server's NETCONF: %v", e.Key(), err)
		}
	}
}

// locations are every location schemas name, their submodules' included.
func locations(schemas []yanglib.Schema) []string {
	var out []string
	for _, s := range schemas {
		out = append(out, s.Locations...)
		out = append(out, locations(s.Submodules)...)
	}
	return out
}

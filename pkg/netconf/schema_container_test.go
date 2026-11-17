// Package netconf_test: get-schema against Netopeer2, an independent RFC 6022
// server, which prints each module it serves from its own parsed tree, so its
// text is not the file the module was installed from and is read here as it
// arrives.
package netconf_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// TestConformance_GetSchemaFetchesEveryModuleTheLibraryLists fetches the
// source of every module and submodule the server's YANG library lists,
// implemented and import-only, and holds each answer to being the module it
// was asked for.
func TestConformance_GetSchemaFetchesEveryModuleTheLibraryLists(t *testing.T) {
	s := notconfSession(t, netconf.Options{})
	if !s.CanGetSchema() {
		t.Fatal("CanGetSchema = false against Netopeer2, which implements ietf-netconf-monitoring")
	}
	schemas := s.Discovery().Schemas
	if len(schemas) < 10 {
		t.Fatalf("the library lists %d schema entries, want Netopeer2's whole module set", len(schemas))
	}
	fetched, importOnly := 0, 0
	for _, entry := range schemas {
		entries := append([]yanglib.Schema{entry}, entry.Submodules...)
		for i, e := range entries {
			src, err := s.GetSchema(context.Background(), e.Name, e.Revision, 16<<20)
			if err != nil {
				t.Errorf("GetSchema(%s): %v", e.Key(), err)
				continue
			}
			if err := yanglib.CheckSource(src, e, i > 0); err != nil {
				t.Errorf("%s: %v", e.Key(), err)
			}
			fetched++
		}
		if entry.ImportOnly {
			importOnly++
		}
	}
	if importOnly == 0 {
		t.Error("no import-only module was fetched, want Netopeer2's (ietf-inet-types among them)")
	}
	t.Logf("fetched %d sources, %d import-only modules among them", fetched, importOnly)
}

// TestConformance_GetSchemaOfAModuleTheServerLacks is refused by the server
// itself, as an rpc-error, never as an empty source.
func TestConformance_GetSchemaOfAModuleTheServerLacks(t *testing.T) {
	s := notconfSession(t, netconf.Options{})
	var rejected netconf.RPCErrors
	if src, err := s.GetSchema(context.Background(), "pleiades-no-such-module", "2001-01-01", 1<<20); !errors.As(err, &rejected) {
		t.Fatalf("GetSchema of a missing module = %q, %v; want the server's rpc-error", src, err)
	}
	t.Logf("refused with %s: %s", rejected[0].Tag, rejected[0].Message)
}

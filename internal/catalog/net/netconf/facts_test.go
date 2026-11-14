// Package netconf: tests of net.netconf.facts, the read-only discovery
// reading.
package netconf

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestFacts_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.netconf.facts")
	if !ok {
		t.Fatal("net.netconf.facts is not registered")
	}
	if d.Manifest.Status != collection.StatusImplemented || !d.Manifest.SupportsCheck || d.Check == nil || !d.Manifest.Reversibility.ReadOnly {
		t.Errorf("manifest = %+v, want an implemented, read-only method whose check runs it", d.Manifest)
	}
	for _, key := range discoveryFactKeys {
		if _, ok := collection.FactFor(d.Manifest.Facts, key); !ok {
			t.Errorf("fact %s is not declared", key)
		}
	}
}

func TestFacts_RecordsTheDiscoveryAndChangesNothing(t *testing.T) {
	f := &fakeSession{discovery: nativeDiscovery()}
	target := useSession(t, f)
	rc := newStubContext()
	res, err := Facts(context.Background(), rc, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || len(f.editCalls) != 0 || f.locked != 0 || !f.closed {
		t.Errorf("changed=%v edits=%d locks=%d closed=%v, want a read that changes nothing and closes", res.Changed, len(f.editCalls), f.locked, f.closed)
	}
	if *target != "" {
		t.Errorf("target = %q, want none: the session chooses", *target)
	}
	if rc.facts[factModuleCount] != 1 || rc.facts[factCompleteness] != "complete" {
		t.Errorf("facts = %v", rc.facts)
	}
}

func TestFacts_AFailedOpenIsTheTasksError(t *testing.T) {
	refused := errors.New("refused")
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		return nil, refused
	})
	if _, err := Facts(context.Background(), newStubContext(), nil, nil); !errors.Is(err, refused) {
		t.Errorf("err = %v, want the open's", err)
	}
}

func TestFacts_PassesOnTheDiscoverysWarnings(t *testing.T) {
	d := nativeDiscovery()
	d.LibraryError = "access denied"
	useSession(t, &fakeSession{discovery: d})
	rc := newStubContext()
	if _, err := Facts(context.Background(), rc, nil, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if w, _ := rc.stats[sdk.StatWarnings].([]string); len(w) != 1 {
		t.Errorf("warnings = %v, want the library's", rc.stats[sdk.StatWarnings])
	}
}

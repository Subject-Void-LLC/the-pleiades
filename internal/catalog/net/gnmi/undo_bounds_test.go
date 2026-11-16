// Package gnmi_test: net.gnmi.config's undo held to the bounds the run
// journal keeps an undo's values to, so a write is refused before it is sent
// when the journal would withhold its undo whole and a rollback refuse it,
// and every undo it does record is one the journal keeps.
package gnmi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// loopbacks is an update creating n loopbacks, each its own node.
func loopbacks(n int) map[string]any {
	var entries []any
	for i := range n {
		name := fmt.Sprintf("lo%d", i)
		entries = append(entries, map[string]any{"path": "/interface[name=" + name + "]", "val": map[string]any{"name": name}})
	}
	return map[string]any{"update": entries}
}

// named is an update creating one loopback named name.
func named(name string) map[string]any {
	return update("/interface[name="+name+"]", map[string]any{"name": name})
}

// TestConfig_RefusesAnUndoTheJournalCannotKeep sends no Set for a write
// whose undo would delete more nodes, or a longer path, than the journal
// keeps, and records an undo the journal keeps at each bound.
func TestConfig_RefusesAnUndoTheJournalCannotKeep(t *testing.T) {
	// The longest key value whose node's path, /interface[name=...], is
	// exactly the bound.
	longest := strings.Repeat("x", 256-len("/interface[name=]"))
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"too many nodes":  {loopbacks(129), "at most 128"},
		"too long a path": {named(longest + "x"), "at most 256"},
	} {
		s := gnmitest.New(t, login)
		_, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "nothing was sent") || s.Sets() != 0 {
			t.Errorf("%s: Invoke = %v after %d Sets, want it refused before the Set, naming %q", name, err, s.Sets(), tc.want)
		}
	}
	for name, params := range map[string]map[string]any{
		"as many nodes as it keeps":  loopbacks(128),
		"as long a path as it keeps": named(longest),
	} {
		s := gnmitest.New(t, login)
		rc := newRecorder(gnmitest.Secrets(s))
		if res, err := run(t, "net.gnmi.config", s, rc, params); err != nil || !res.Changed {
			t.Fatalf("%s: Invoke = %+v, %v", name, res, err)
		}
		p, _ := inverse(rc)["params"].(map[string]any)
		if _, kept := engine.RecordableTexts(p["delete"]); !kept {
			t.Errorf("%s: the journal would withhold the undo's delete %v", name, p["delete"])
		}
	}
}

// TestUndoBounds_AreTheJournals holds the method's two bounds to the
// journal's own: one node past either is a list or a path the journal
// refuses.
func TestUndoBounds_AreTheJournals(t *testing.T) {
	path := func(n int) string { return "/" + strings.Repeat("a", n-1) }
	at := make([]any, 128)
	for i := range at {
		at[i] = path(256)
	}
	if _, kept := engine.RecordableTexts(at); !kept {
		t.Error("the journal refuses 128 paths of 256 bytes, which the method records")
	}
	if _, kept := engine.RecordableTexts(append(at, path(256))); kept {
		t.Error("the journal keeps 129 paths, which the method refuses: its bound is out of date")
	}
	if _, kept := engine.RecordableTexts([]any{path(257)}); kept {
		t.Error("the journal keeps a path of 257 bytes, which the method refuses: its bound is out of date")
	}
}

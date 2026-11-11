// Tests of the frozen graph digest encoding.
package runbook_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// goldenGraph is a fixed graph: a method with a rollback: list, a marker, a
// method with an empty rollback: list, and one with none.
var goldenGraph = map[string]runbook.Node{
	"tasks[0]":             {Method: "file.copy", Authored: true, Rollback: []string{"file.remove", "noop"}},
	"tasks[1]":             {Marker: true},
	"tasks[1].parallel[0]": {Method: "pkg.install", Authored: true},
	"tasks[2]":             {Method: "acme.inventory.read"},
}

// goldenGraphDigest is goldenGraph's digest under graph/1. It is frozen: a
// change to it is a change to the scheme, which is never made; a new
// encoding is a new scheme beside this one.
const goldenGraphDigest = "sha256:442292ab34a16f8a72b0137f6ef76c3d0a37bdf278e40fb371bf2aabb6044136"

// TestGraphDigest_GraphV1IsFrozen holds graph/1 to its golden vector, and to
// its encoding as the scheme's doc states it, built here by hand.
func TestGraphDigest_GraphV1IsFrozen(t *testing.T) {
	got, err := runbook.DigestUnder(runbook.GraphSchemeV1, goldenGraph)
	if err != nil {
		t.Fatal(err)
	}
	if got != goldenGraphDigest {
		t.Errorf("graph/1 digests the golden graph to %s, want the frozen %s", got, goldenGraphDigest)
	}
	// The documented encoding, written out by hand.
	h := sha256.New()
	str := func(s string) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(s)))
		h.Write(b[:])
		h.Write([]byte(s))
	}
	num := func(n int) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		h.Write(b[:])
	}
	flag := func(f bool) {
		if f {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	}
	str("graph/1")
	for _, id := range []string{"tasks[0]", "tasks[1]", "tasks[1].parallel[0]", "tasks[2]"} {
		n := goldenGraph[id]
		str(id)
		str(n.Method)
		flag(n.Marker)
		flag(n.Authored)
		num(len(n.Rollback))
		for _, step := range n.Rollback {
			str(step)
		}
	}
	if want := runbook.GraphDigestPrefix + hex.EncodeToString(h.Sum(nil)); got != want {
		t.Errorf("graph/1 = %s, want the documented encoding's %s", got, want)
	}
	if runbook.GraphDigest(goldenGraph) != got || runbook.GraphScheme != runbook.GraphSchemeV1 {
		t.Errorf("GraphDigest is not graph/1's digest")
	}
}

// TestGraphDigest_EachFieldCounts: each field the scheme covers moves the
// digest, and a string's length prefix keeps two splits of one text apart.
func TestGraphDigest_EachFieldCounts(t *testing.T) {
	base := runbook.GraphDigest(goldenGraph)
	for name, edit := range map[string]func(map[string]runbook.Node){
		"a method":         func(g map[string]runbook.Node) { g["tasks[2]"] = runbook.Node{Method: "acme.inventory.write"} },
		"a marker":         func(g map[string]runbook.Node) { g["tasks[1]"] = runbook.Node{} },
		"an authored flag": func(g map[string]runbook.Node) { g["tasks[1].parallel[0]"] = runbook.Node{Method: "pkg.install"} },
		"a rollback: step": func(g map[string]runbook.Node) {
			g["tasks[0]"] = runbook.Node{Method: "file.copy", Authored: true, Rollback: []string{"file.remove"}}
		},
		"a node id": func(g map[string]runbook.Node) { g["tasks[3]"] = g["tasks[2]"]; delete(g, "tasks[2]") },
		"a split of one text": func(g map[string]runbook.Node) {
			g["tasks[0]"] = runbook.Node{Method: "file.copy", Authored: true, Rollback: []string{"file.removenoop"}}
		},
	} {
		g := map[string]runbook.Node{}
		for k, v := range goldenGraph {
			g[k] = v
		}
		edit(g)
		if runbook.GraphDigest(g) == base {
			t.Errorf("changing %s left the digest as it was", name)
		}
	}
	if _, err := runbook.DigestUnder("graph/0", goldenGraph); !errors.Is(err, runbook.ErrUnknownGraphScheme) {
		t.Errorf("an unknown scheme: %v, want ErrUnknownGraphScheme", err)
	}
}

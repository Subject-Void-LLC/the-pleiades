// Package journal_test: the Controller, which stores a Runner's journal
// rows, is their writer, so it holds each delivered Collection identity and
// undo digest to what the engine's projection writes (LESSONS 413), as the
// facts subscriber does: one that fails is stored as none, with a warning,
// and the entry is kept.
package journal_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// delivered is one entry of a delivered batch, the identity and undo digest
// it carries, and whether the store keeps them.
type delivered struct {
	node     string
	identity wire.CollectionIdentity
	program  string
	undo     string
	kept     bool
}

// digestOf is a well-formed digest of c repeated.
func digestOf(c string) string { return wire.ManifestDigestPrefix + strings.Repeat(c, 64) }

// identityCases are the delivered identities the store tells apart.
func identityCases() []delivered {
	builtin := wire.CollectionIdentity{Group: "exec", Version: "0.4.0", Manifests: digestOf("a")}
	formatChar := builtin
	formatChar.Group = "ex" + string(rune(0x202e)) + "ec"
	short := builtin
	short.Manifests = short.Manifests[:len(short.Manifests)-1]
	program := wire.CollectionIdentity{Group: "acme", Program: true, Manifests: digestOf("c")}
	return []delivered{
		{node: "tasks[0]", identity: builtin, undo: digestOf("b"), kept: true},
		{node: "tasks[1]", identity: formatChar, undo: digestOf("b")},
		{node: "tasks[2]", identity: program, undo: digestOf("b")},
		{node: "tasks[3]", identity: builtin, program: "acme", undo: digestOf("b")},
		{node: "tasks[4]", identity: program, program: "acme", kept: true},
		{node: "tasks[5]", identity: short, undo: digestOf("b")},
	}
}

func TestSubscriberStoresNoIdentityTheEngineWouldNotWrite(t *testing.T) {
	store, _ := newEntStore(t)
	var logged bytes.Buffer
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)))

	cases := identityCases()
	batch := journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1}
	for i, c := range cases {
		e := walkEntry("job-1", "device-1", 1, i+1, c.node)
		e.Collection, e.ProviderProgram, e.InverseSpecDigest = c.identity, c.program, c.undo
		batch.Entries = append(batch.Entries, e)
	}
	// A malformed undo digest beside a sound identity: the digest alone goes.
	badUndo := walkEntry("job-1", "device-1", 1, len(cases)+1, "tasks[6]")
	badUndo.Collection, badUndo.InverseSpecDigest = cases[0].identity, "sha256:short"
	batch.Entries = append(batch.Entries, badUndo)

	if err := sub.Handle(batchEvent(t, batch)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(batch.Entries) {
		t.Fatalf("stored %d entries, want every one of the %d delivered kept", len(stored), len(batch.Entries))
	}
	byNode := map[string]engine.JournalEntry{}
	for _, e := range stored {
		byNode[e.NodeID] = e
	}
	for _, c := range cases {
		got := byNode[c.node]
		switch {
		case c.kept && (got.Collection != c.identity || got.InverseSpecDigest != c.undo):
			t.Errorf("%s: stored %+v and %q, want %+v and %q kept", c.node, got.Collection, got.InverseSpecDigest, c.identity, c.undo)
		case !c.kept && !got.Collection.IsZero():
			t.Errorf("%s: stored %+v, an identity no build writes, want none", c.node, got.Collection)
		case got.ProviderProgram != c.program:
			t.Errorf("%s: the entry's program became %q", c.node, got.ProviderProgram)
		}
	}
	if got := byNode["tasks[6]"]; got.InverseSpecDigest != "" || got.Collection != cases[0].identity {
		t.Errorf("tasks[6]: stored %+v and %q, want the identity kept and the malformed undo digest dropped", got.Collection, got.InverseSpecDigest)
	}

	log := logged.String()
	for _, node := range []string{"tasks[1]", "tasks[2]", "tasks[3]", "tasks[5]", "tasks[6]"} {
		if !strings.Contains(log, "node="+node) {
			t.Errorf("no warning names %s:\n%s", node, log)
		}
	}
	for _, node := range []string{"tasks[0]", "tasks[4]"} {
		if strings.Contains(log, "node="+node) {
			t.Errorf("a warning names %s, whose identity is one the engine writes:\n%s", node, log)
		}
	}
	if strings.Contains(log, string(rune(0x202e))) {
		t.Errorf("a warning prints a group's format character raw:\n%s", log)
	}
}

// TestEntStoreSavesNoIdentityTheEngineWouldNotWrite: the store holds any
// caller to the same rule, so no path writes a row a reader is not meant to
// see, and says what it cleared, naming the job, device, node and method.
// The store logs to the process default unless given a logger, which is
// what a caller other than the subscriber gets.
func TestEntStoreSavesNoIdentityTheEngineWouldNotWrite(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	store, _ := newEntStore(t)
	var entries []engine.JournalEntry
	for i, c := range identityCases() {
		e := walkEntry("job-2", "device-1", 1, i+1, c.node)
		e.Collection, e.ProviderProgram, e.InverseSpecDigest = c.identity, c.program, c.undo
		entries = append(entries, e)
	}
	if _, err := store.Save(context.Background(), entries); err != nil {
		t.Fatalf("Save: %v", err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-2", 0)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, c := range identityCases() {
		kept[c.node] = c.kept
	}
	for _, e := range stored {
		if !kept[e.NodeID] && !e.Collection.IsZero() {
			t.Errorf("%s: saved %+v, an identity no build writes", e.NodeID, e.Collection)
		}
	}
	for node, keep := range kept {
		said := strings.Contains(logged.String(), "node="+node)
		if said == keep {
			t.Errorf("%s: logged %v, want a warning only for what was cleared:\n%s", node, said, logged.String())
		}
	}
	if !strings.Contains(logged.String(), "job=job-2") || !strings.Contains(logged.String(), "device=device-1") || !strings.Contains(logged.String(), "fqcn=exec.command") {
		t.Errorf("the warnings do not name the job, device and method:\n%s", logged.String())
	}
	if strings.Contains(logged.String(), string(rune(0x202e))) {
		t.Errorf("a warning prints a group's format character raw:\n%s", logged.String())
	}
}

// Tests that the run journal never keeps a fact's own name or value: a task
// whose method emits facts beside its stats is journaled with the names of
// the results the method declares, and with no item fact's name, no fact's
// value and no undeclared stat's name anywhere in the bytes the Crawl tier's
// journal file store writes.
package engine_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// journalFactsMethod is the fixture method name for the journal's fact tests.
const journalFactsMethod = "enginejournalfacts.gather"

// journalFactNames are the item fact names the fixture emits: the names an
// attacker reading a journal would guess at to learn what a device holds. A
// path is percent-encoded in its key, as facts.gather encodes any item name.
var journalFactNames = []string{"package.openssh-server", "user.alice", "file.%2Fsrv%2Fpayroll%2Fledger.db"}

// journalFactValues are distinctive texts the fixture's facts and its
// undeclared stat hold, none of them made only of hex digits and dashes, so
// none could turn up in a journal entry's random IDs by chance.
var journalFactValues = []string{"Quartz9Meridian", "Velvet3Orchard", "Tundra5Lantern", "Obsidian4Harbor", "Marigold8Spire"}

// journalUndeclaredStat is a stat the fixture sets that its Doc does not
// declare, named like an item fact.
const journalUndeclaredStat = "package.nginx"

// registerJournalFactsMethod registers the fixture: it emits three item
// facts and one fixed-name fact, sets two declared stats and one undeclared
// stat named like a fact, and declares every fact it emits.
func registerJournalFactsMethod(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	gather := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		emitted := map[string]any{
			"package.openssh-server":            map[string]any{"version": "Quartz9Meridian"},
			"user.alice":                        map[string]any{"home": "/home/Velvet3Orchard"},
			"file.%2Fsrv%2Fpayroll%2Fledger.db": map[string]any{"owner": "Tundra5Lantern"},
			"os.kernel.release":                 "Obsidian4Harbor",
		}
		for key, value := range emitted {
			if err := rc.EmitFact(key, value); err != nil {
				return collection.Result{}, err
			}
		}
		if err := rc.SetStat("package_count", 1); err != nil {
			return collection.Result{}, err
		}
		if err := rc.SetStat("elapsed_ms", 12); err != nil {
			return collection.Result{}, err
		}
		// An undeclared stat whose name reads as an item fact's and whose
		// value is distinctive: it is counted, never named.
		if err := rc.SetStat(journalUndeclaredStat, "Marigold8Spire"); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: journalFactsMethod, Invoke: gather,
		// Not declared read-only: the strict reader refuses a read-only call
		// from a method no first-party Collection holds, which this is.
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"},
			Doc: collection.Doc{
				Summary: "A fixture that emits facts beside its stats.",
				// The two declared results, and the fixed fact name the method
				// also declares as a result, as facts.gather declares host.name
				// both ways. An item family is declared the way facts.gather
				// declares one, with a placeholder no emitted name equals.
				Returns: []collection.ReturnField{
					{Name: "package_count", Type: "integer", Description: "A declared result."},
					{Name: "elapsed_ms", Type: "integer", Description: "A declared result."},
					{Name: "os.kernel.release", Type: "string", Description: "A fact the method also declares as a result."},
					{Name: "package.<name>", Type: "object", Description: "One fact per package."},
				},
			},
			Facts: []collection.FactSpec{
				{Key: "package.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a package"},
				{Key: "user.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a user"},
				{Key: "file.", Prefix: true, ItemNames: true, Kind: collection.FactStable, Type: collection.FactObject, Description: "a file"},
				{Key: "os.kernel.release", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"},
			}}}); err != nil {
		t.Fatal(err)
	}
}

// TestJournal_NeverKeepsAFactsOwnNameOrValue runs the fixture through the
// real executor with the Crawl tier's real journal file store and a fact
// recorder, then searches every byte the store wrote. The facts did reach
// the recorder, so they were real facts; the journal names the declared
// results, counts the stats it may not name, and holds no item fact's name,
// no fact's value and no undeclared stat's name anywhere.
func TestJournal_NeverKeepsAFactsOwnNameOrValue(t *testing.T) {
	registerJournalFactsMethod(t)
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	sink := &factSink{got: map[string][]facts.Entry{}}
	result, err := newCheckExecutor(factFleet(), engine.WithJournal(store), engine.WithFactRecorder(sink)).Run(context.Background(),
		buildDAG(t, `{"id": "journal-facts", "tasks": [{"name": "g", "register": "r", "`+journalFactsMethod+`": {"target": "fleet"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.HasErrors() {
		t.Fatalf("expected a clean run, got %+v", result.Nodes)
	}
	if err := store.Seal(result.RunID, result.Nodes[len(result.Nodes)-1].FinishedAt); err != nil {
		t.Fatal(err)
	}

	// The control: every fact the method emitted was stored as a fact, so
	// the journal below is the record of a run that really observed them.
	stored := map[string]bool{}
	for _, e := range sink.got["dev-1"] {
		stored[e.Key] = true
	}
	for _, name := range append(append([]string(nil), journalFactNames...), "os.kernel.release") {
		if !stored[name] {
			t.Fatalf("the fact %s was not recorded (recorded %v), so this run proves nothing about the journal", name, stored)
		}
	}

	// The two-sided half: the entry the strict reader reads back names the
	// method's declared results and counts the two stat keys no Doc names.
	run, err := journal.ReadRun(root, result.RunID)
	if err != nil {
		t.Fatalf("the strict reader refused the journal: %v", err)
	}
	if len(run.Entries) != 1 {
		t.Fatalf("read %d entries, want one", len(run.Entries))
	}
	e := run.Entries[0]
	assertKeys(t, "StatKeys", e.StatKeys, []string{"elapsed_ms", "os.kernel.release", "package_count"})
	// The three item facts and the undeclared stat are counted, not named.
	if e.UndeclaredStatCount != 4 {
		t.Errorf("recorded %d undeclared stats, want 4: three item facts and %s", e.UndeclaredStatCount, journalUndeclaredStat)
	}

	// The guarantee: no name or value is anywhere in what the store wrote,
	// in any file, searched as bytes rather than field by field.
	written := journalBytes(t, root)
	if !strings.Contains(written, `"elapsed_ms"`) || !strings.Contains(written, `"package_count"`) {
		t.Fatalf("the control failed: the declared result names are not in the written journal:\n%s", written)
	}
	forbidden := append(append(append([]string(nil), journalFactNames...), journalFactValues...), journalUndeclaredStat,
		"openssh-server", "alice", "payroll", sdk.StatFactKeys)
	for _, text := range forbidden {
		if strings.Contains(written, text) {
			t.Errorf("the written journal holds %q; no fact's name or value, and no undeclared stat's name, may reach it", text)
		}
	}
}

// journalBytes is every byte of every file under root, the project the
// journal file store wrote into, joined.
func journalBytes(t *testing.T, root string) string {
	t.Helper()
	var all strings.Builder
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		all.Write(raw)
		all.WriteByte(0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("the journal store wrote no file")
	}
	return all.String()
}

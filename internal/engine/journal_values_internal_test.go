// Tests for kind 7: which undo parameter values the projection records
// (JournalEntry.InverseParams), and when it calls an undo complete.
//
// The rule under test is an allowlist, so each case is one way a value
// could slip past it: a key the emitter does not declare, a withheld key,
// a value that is not a scalar, text too long or unsafe to keep, an
// emitter that is an external program, and an undo naming a method the
// emitter never declared. The positive case is here too, since a rule
// that refused everything would pass the rest.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerUndoFixtures registers an emitter and the method its undo calls,
// under names only this file uses, and returns the emitter's name.
func registerUndoFixtures(t testing.TB, provider *collection.Provider, mayBePartial bool) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	noop := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	target := collection.Descriptor{
		Name: "valuestest.target",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "fixture"},
			Doc: collection.Doc{Params: []collection.Param{
				{Name: "name"}, {Name: "size"}, {Name: "on"}, {Name: "ratio"}, {Name: "content"}, {Name: "note"},
			}}},
		Invoke: noop,
	}
	emitter := collection.Descriptor{
		Name: "valuestest.emitter",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{Reversible: true, Inverses: []sdk.InverseSpec{
				{FQCN: "valuestest.target", Record: []string{"name", "size", "on", "ratio", "note"}, Withhold: []string{"content"}, MayBePartial: mayBePartial},
			}}},
		Invoke:   noop,
		Provider: provider,
	}
	for _, d := range []collection.Descriptor{target, emitter} {
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
	return emitter.Name
}

// projectUndo projects one changed node of emitter whose recorded undo is
// fqcn with params (and partial, when set).
func projectUndo(t *testing.T, emitter, fqcn string, params map[string]any, partial bool) JournalEntry {
	t.Helper()
	r := &run{
		dag:   buildInternalDAG(t, `{"id": "values", "tasks": [{"name": "a", "fqcn": "`+emitter+`"}]}`),
		runID: "run-under-test",
	}
	record := map[string]any{sdk.InverseFQCNKey: fqcn, sdk.InverseParamsKey: params}
	if partial {
		record[sdk.InversePartialKey] = true
	}
	n := NodeResult{NodeID: "tasks[0]", Device: "dev-1", Changed: true}
	n.journalStats = map[string]interface{}{sdk.StatInverse: record}
	n.journalChanged = true
	entry, err := r.projectResult(n)
	if err != nil {
		t.Fatalf("projectResult: %v", err)
	}
	return entry
}

// recorded returns the entry's recorded values by key, as their JSON.
func recorded(t *testing.T, entry JournalEntry) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range entry.InverseParams {
		v, ok := p.Value()
		if !ok {
			t.Fatalf("recorded param %q holds no value", p.Key)
		}
		b, _ := json.Marshal(v)
		out[p.Key] = string(b)
	}
	return out
}

func TestProjectInverse_RecordsTheDeclaredIdentifiers(t *testing.T) {
	emitter := registerUndoFixtures(t, nil, false)
	entry := projectUndo(t, emitter, "valuestest.target", map[string]any{
		"name": "bsd-scratch", "size": 4096, "on": true, "ratio": 0.5,
	}, false)
	got := recorded(t, entry)
	want := map[string]string{"name": `"bsd-scratch"`, "size": "4096", "on": "true", "ratio": "0.5"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("recorded %s = %s, want %s (all: %v)", k, got[k], v, got)
		}
	}
	if !entry.InverseComplete || entry.InversePartial {
		t.Errorf("complete %v partial %v, want a complete, whole undo", entry.InverseComplete, entry.InversePartial)
	}
	if !entry.ActionChanged {
		t.Error("ActionChanged false for an action that reported a change")
	}
	// Key order is fixed, whatever the map's.
	for i := 1; i < len(entry.InverseParams); i++ {
		if entry.InverseParams[i-1].Key > entry.InverseParams[i].Key {
			t.Errorf("recorded params out of order: %v", entry.InverseParams)
		}
	}
}

func TestProjectInverse_WithholdsWhatIsNotAnIdentifier(t *testing.T) {
	emitter := registerUndoFixtures(t, nil, false)
	const secret = "the-prior-file-held-this-password"
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"a withheld key", map[string]any{"name": "a", "content": secret}},
		{"text past 256 bytes", map[string]any{"name": "a", "note": secret + strings.Repeat("x", 300)}},
		{"a newline", map[string]any{"name": "a", "note": secret + "\nsecond line"}},
		{"an escape sequence", map[string]any{"name": "a", "note": secret + "\x1b[2J"}},
		{"invalid UTF-8", map[string]any{"name": "a", "note": secret + "\xff"}},
		{"a map", map[string]any{"name": "a", "note": map[string]any{"k": secret}}},
		{"a list", map[string]any{"name": "a", "note": []any{secret}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := projectUndo(t, emitter, "valuestest.target", tc.params, false)
			encoded, _ := json.Marshal(entry)
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("the entry carries the withheld value: %s", encoded)
			}
			if entry.InverseComplete {
				t.Error("an undo with a value left out is marked complete, so a rollback would replay it without that value")
			}
			if got := recorded(t, entry); got["name"] != `"a"` {
				t.Errorf("the identifier beside it was not recorded: %v", got)
			}
		})
	}
}

func TestProjectInverse_TrustsOnlyABuiltinDeclarationForItsOwnTarget(t *testing.T) {
	t.Run("an external program", func(t *testing.T) {
		emitter := registerUndoFixtures(t, &collection.Provider{Program: "/p", Digest: "sha256:x"}, false)
		entry := projectUndo(t, emitter, "valuestest.target", map[string]any{"name": "a"}, false)
		if len(entry.InverseParams) != 0 || entry.InverseComplete {
			t.Errorf("an external program's declaration was honored: %v complete %v", entry.InverseParams, entry.InverseComplete)
		}
	})
	t.Run("an undo naming another method", func(t *testing.T) {
		emitter := registerUndoFixtures(t, nil, false)
		// valuestest.emitter declares valuestest.target only; an undo
		// naming the emitter itself is recorded by key name alone.
		entry := projectUndo(t, emitter, emitter, map[string]any{}, false)
		if len(entry.InverseParams) != 0 || entry.InverseComplete {
			t.Errorf("an undo through an undeclared method recorded values: %v complete %v", entry.InverseParams, entry.InverseComplete)
		}
	})
}

func TestProjectInverse_APartialUndoIsNeverComplete(t *testing.T) {
	for _, mayBePartial := range []bool{true, false} {
		t.Run(map[bool]string{true: "declared", false: "undeclared"}[mayBePartial], func(t *testing.T) {
			emitter := registerUndoFixtures(t, nil, mayBePartial)
			entry := projectUndo(t, emitter, "valuestest.target", map[string]any{"name": "a"}, true)
			if !entry.InversePartial || entry.InverseComplete {
				t.Errorf("partial %v complete %v, want partial and not complete", entry.InversePartial, entry.InverseComplete)
			}
		})
	}
}

func TestProjectResult_ActionChangedSurvivesALaterFailure(t *testing.T) {
	r := &run{dag: buildInternalDAG(t, `{"id": "later", "tasks": [{"name": "a", "fqcn": "noop"}]}`), runID: "r"}
	n := NodeResult{NodeID: "tasks[0]", Device: "dev-1"}
	n.journalChanged = true
	n.fail(FailureStageRecord, errTestUntagged)
	entry, err := r.projectResult(n)
	if err != nil {
		t.Fatalf("projectResult: %v", err)
	}
	if entry.Outcome != OutcomeFailed || !entry.ActionChanged {
		t.Errorf("outcome %q action changed %v, want a failed node that still says it changed the device", entry.Outcome, entry.ActionChanged)
	}
}

func TestProjectResult_WithRollbackLinksEachNodeToWhatItUndoes(t *testing.T) {
	x := NewExecutor(nil, nil, nil, nil, nil, 0, WithRollback("run-being-undone", map[string]Undo{"tasks[0]": {Node: "tasks[3]", Step: 1}}))
	r := &run{x: x, dag: buildInternalDAG(t, `{"id": "rb", "tasks": [{"name": "a", "fqcn": "noop"}]}`), runID: "r"}
	entry, err := r.projectResult(NodeResult{NodeID: "tasks[0]", Device: "dev-1"})
	if err != nil {
		t.Fatalf("projectResult: %v", err)
	}
	if entry.RollbackOf != "run-being-undone" || entry.UndoesNode != "tasks[3]" || entry.UndoesStep != 1 {
		t.Errorf("rollback link %q %q %d, want run-being-undone tasks[3] 1", entry.RollbackOf, entry.UndoesNode, entry.UndoesStep)
	}
	plain, err := (&run{x: NewExecutor(nil, nil, nil, nil, nil, 0), dag: r.dag, runID: "r"}).projectResult(NodeResult{NodeID: "tasks[0]", Device: "dev-1"})
	if err != nil || plain.RollbackOf != "" || plain.UndoesNode != "" {
		t.Errorf("an ordinary run's entry %+v (%v) names a rollback", plain, err)
	}
}

// FuzzProjectRecordedValues drives arbitrary values through the kind 7
// path against a real declared emitter: name and note are declared
// recordable, content withheld, and extra is a key the emitter never
// declares. Whatever the values, only a declared key's value may be
// recorded, exactly as given and only when it passes the text rules; the
// withheld and undeclared values never are; and the undo reads complete
// exactly when every value it carried was recorded.
func FuzzProjectRecordedValues(f *testing.F) {
	f.Add("bsd-scratch", "a note", "the prior file", "extra", false)
	f.Add("", "", "", "", true)
	f.Add("line\nbreak", "\x1b[2J", "-----BEGIN RSA PRIVATE KEY-----", "k", false)
	f.Add(strings.Repeat("n", 257), "ok", "c", "", false)
	emitter := registerUndoFixtures(f, nil, true)

	f.Fuzz(func(t *testing.T, name, note, content, extra string, withContent bool) {
		params := map[string]any{"name": name, "note": note}
		if withContent {
			params["content"] = content
		}
		if extra != "" && extra != "name" && extra != "note" && extra != "content" {
			params[extra] = content
		}
		entry := projectUndo(t, emitter, "valuestest.target", params, false)

		recordedAll := true
		for _, key := range []string{"name", "note"} {
			v := params[key].(string)
			if len(v) > maxRecordedText || !utf8.ValidString(v) || termsafe.CheckLine(v) != nil {
				recordedAll = false
			}
		}
		for _, p := range entry.InverseParams {
			if p.Key != "name" && p.Key != "note" {
				t.Fatalf("recorded the value of %q, which the emitter does not declare recordable", p.Key)
			}
			if p.Text == nil || *p.Text != params[p.Key] {
				t.Fatalf("recorded %q as %v, want exactly the value the undo carried", p.Key, p.Text)
			}
			if len(*p.Text) > maxRecordedText || !utf8.ValidString(*p.Text) || termsafe.CheckLine(*p.Text) != nil {
				t.Fatalf("recorded %q with a value the text rules refuse", p.Key)
			}
		}
		// Undeclared keys never reach InverseParamKeys either, so the
		// undeclared count says an extra key was there.
		carriedOnlyDeclared := !withContent && (extra == "" || extra == "name" || extra == "note" || extra == "content")
		if want := recordedAll && carriedOnlyDeclared; entry.InverseComplete != want {
			t.Fatalf("complete %v, want %v (recorded %v, carried only declared keys %v)", entry.InverseComplete, want, entry.InverseParams, carriedOnlyDeclared)
		}
	})
}

// BenchmarkProjectLevelWithValues is BenchmarkProjectLevel for a method
// that records its undo's values: the same large withheld content, and
// the declared identifiers checked and kept. It is read against that
// benchmark, since recording values must not be what makes a run slower.
func BenchmarkProjectLevelWithValues(b *testing.B) {
	emitter := registerUndoFixtures(b, nil, false)
	stats := benchStats()
	stats[sdk.StatInverse] = map[string]interface{}{
		sdk.InverseFQCNKey: "valuestest.target",
		sdk.InverseParamsKey: map[string]interface{}{
			"name": strings.Repeat("n", 200), "size": 3, "on": true, "ratio": 1.5,
			"note": "a note", "content": benchRunningConfig,
		},
	}
	for _, devices := range []int{1, 5, 50} {
		b.Run(fmt.Sprintf("devices=%d", devices), func(b *testing.B) {
			out := make([]NodeResult, devices)
			for i := range out {
				out[i] = NodeResult{NodeID: "tasks[0]", Device: fmt.Sprintf("device-%d", i), Changed: true, Stats: stats, journalStats: stats, journalChanged: true}
			}
			level := [][]NodeResult{out}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := benchProjectionRun()
				r.dag.Nodes["tasks[0]"].FQCN = emitter
				entries, err := r.projectLevel(level)
				if err != nil || len(entries) != devices || len(entries[0].InverseParams) != 5 {
					b.Fatalf("projected %d entries (%v), the first holding %d values", len(entries), err, len(entries[0].InverseParams))
				}
			}
		})
	}
}

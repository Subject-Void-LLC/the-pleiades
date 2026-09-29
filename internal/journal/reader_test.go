// Package journal_test: reading a run's journal back, and what the reader
// refuses.
package journal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// readerRun is a run id shaped like the engine's.
const readerRun = "7d9f1c1e-2b1a-4c3d-9e8f-0a1b2c3d4e5f"

// digest is a DAG.Version-shaped digest.
var digest = "sha256:" + strings.Repeat("ab", 32)

// writtenRun records two entries for readerRun through the real sink and
// seals it, returning the project root.
func writtenRun(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	e := func(seq int, node string) engine.JournalEntry {
		return engine.JournalEntry{RunID: readerRun, Sequence: seq, NodeID: node, DAGVersion: digest,
			Outcome: engine.OutcomeChanged, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	}
	if err := store.Record(context.Background(), []engine.JournalEntry{e(1, "tasks[0]"), e(2, "tasks[1].block[0]")}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := store.Seal(readerRun, time.Now()); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return root
}

func journalFile(root string) string {
	return filepath.Join(root, ".pleiades", "journal", readerRun+".jsonl")
}

func TestReadRun_ReadsWhatTheSinkWrote(t *testing.T) {
	root := writtenRun(t)
	run, err := journal.ReadRun(root, readerRun)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if run.ID != readerRun || !run.Sealed || len(run.Entries) != 2 || run.Entries[1].NodeID != "tasks[1].block[0]" {
		t.Errorf("read %+v", run)
	}
	runs, err := journal.ListRuns(root)
	if err != nil || len(runs) != 1 || runs[0].ID != readerRun {
		t.Errorf("ListRuns = %v, %v", runs, err)
	}
	// An unsealed run reads as such.
	if err := os.Remove(filepath.Join(root, ".pleiades", "journal", readerRun+".end")); err != nil {
		t.Fatal(err)
	}
	if run, err := journal.ReadRun(root, readerRun); err != nil || run.Sealed {
		t.Errorf("without its seal: sealed %v, %v", run.Sealed, err)
	}
	if _, err := journal.ReadRun(root, "0000aaaa-0000-4000-8000-000000000000"); !errors.Is(err, journal.ErrNoSuchRun) {
		t.Errorf("a run with no journal = %v, want ErrNoSuchRun", err)
	}
}

// TestReadRun_RefusesWhatNoRunWrote: a rollback replays what the reader
// returns, so a file anyone but the run could have made is refused.
func TestReadRun_RefusesWhatNoRunWrote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes and owners")
	}
	line := func(s string) func(t *testing.T, root string) {
		return func(t *testing.T, root string) {
			if err := os.WriteFile(journalFile(root), []byte(s+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	valid := `{"run_id":"` + readerRun + `","sequence":1,"node_id":"tasks[0]","dag_version":"` + digest + `","outcome":"changed","failure_stage":"","skip_kind":""}`
	for _, tc := range []struct {
		name string
		make func(t *testing.T, root string)
		want string
	}{
		{"a file others may read", func(t *testing.T, root string) {
			if err := os.Chmod(journalFile(root), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "mode"},
		{"a directory others may enter", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, ".pleiades", "journal"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "mode"},
		{"a symbolic link", func(t *testing.T, root string) {
			other := filepath.Join(t.TempDir(), "elsewhere.jsonl")
			if err := os.WriteFile(other, []byte(valid+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(journalFile(root)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, journalFile(root)); err != nil {
				t.Fatal(err)
			}
		}, "symbolic link"},
		{"an unknown key", line(strings.Replace(valid, `"sequence":1`, `"sequence":1,"shell":"rm -rf /"`, 1)), "unknown field"},
		{"another run's entry", line(strings.Replace(valid, readerRun, "another-run", 1)), "names run"},
		{"a made-up node", line(strings.Replace(valid, "tasks[0]", "../../etc", 1)), "not a graph id"},
		{"a made-up outcome", line(strings.Replace(valid, `"changed"`, `"teleported"`, 1)), "outcome"},
		{"a made-up version", line(strings.Replace(valid, digest, "sha256:x", 1)), "not a digest"},
		{"a line past the limit", line(strings.Replace(valid, `"node_id"`, `"task_name":"`+strings.Repeat("x", 2<<20)+`","node_id"`, 1)), "too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writtenRun(t)
			tc.make(t, root)
			_, err := journal.ReadRun(root, readerRun)
			if err == nil || (!strings.Contains(err.Error(), tc.want) && !(tc.want == "symbolic link" && strings.Contains(err.Error(), "too many levels"))) {
				t.Fatalf("ReadRun = %v, want a refusal mentioning %q", err, tc.want)
			}
		})
	}
}

// FuzzReadJournalLine throws arbitrary lines at the reader: it never
// panics, and anything it accepts names this run and a graph node.
func FuzzReadJournalLine(f *testing.F) {
	f.Add(`{"run_id":"` + readerRun + `","node_id":"tasks[0]","outcome":"ran"}`)
	f.Add(`{"run_id":"` + readerRun + `","node_id":"tasks[0]","outcome":"changed","inverse_params":[{"key":"name","text":"x"}]}`)
	f.Add(`{}`)
	f.Add(`not json`)
	root := writtenRun(f)
	f.Fuzz(func(t *testing.T, line string) {
		if err := os.WriteFile(journalFile(root), []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run, err := journal.ReadRun(root, readerRun)
		if err != nil {
			return
		}
		for _, e := range run.Entries {
			if e.RunID != readerRun || !strings.HasPrefix(e.NodeID, "tasks[") && !strings.HasPrefix(e.NodeID, "pretasks[") && !strings.HasPrefix(e.NodeID, "posttasks[") {
				t.Fatalf("accepted an entry naming run %q node %q", e.RunID, e.NodeID)
			}
		}
	})
}

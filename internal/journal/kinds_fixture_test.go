// Tests that every failure stage and skip kind the engine records
// (engine.RecordedFailureStages, engine.RecordedSkipKinds) is held by a frozen
// journal fixture, reads back through the strict reader, and is listed in the
// production guide's journal field table, so a kind cannot be added in one
// place and forgotten in the others (FAILURE_PATTERNS 959's class).
package journal_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestJournalFormatThreeReadsTheKindsAddedSinceFormatOne holds every later
// build to read the frozen format-3 journal of the kinds the engine began to
// record after format 1's fixture was frozen: the limit skip, a task whose
// target names only devices outside the run's --limit or serial: batch.
func TestJournalFormatThreeReadsTheKindsAddedSinceFormatOne(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-3", "every-new-kind.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, goldenRunID+".jsonl"), frozen, 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := journal.ReadRun(root, goldenRunID)
	if err != nil {
		t.Fatalf("a format-3 journal holding the newer kinds no longer reads: %v", err)
	}
	if len(run.Entries) != 1 || run.Entries[0].SkipKind != engine.SkipKindLimit || run.Entries[0].DeviceID != "" {
		t.Errorf("read %+v", run.Entries)
	}
}

// TestFrozenJournalsHoldEveryRecordedKind: the frozen fixtures together hold
// every stage and skip kind the engine records, so a kind added without one
// fails here rather than in an older build's reader.
func TestFrozenJournalsHoldEveryRecordedKind(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "format-*", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no frozen journals: %v", err)
	}
	stages, kinds := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(file)
		lines.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for lines.Scan() {
			var line struct {
				FailureStage string `json:"failure_stage"`
				SkipKind     string `json:"skip_kind"`
			}
			if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			stages[line.FailureStage], kinds[line.SkipKind] = true, true
		}
		_ = file.Close()
	}
	for _, s := range engine.RecordedFailureStages() {
		if !stages[string(s)] {
			t.Errorf("no frozen journal holds failure stage %q", s)
		}
	}
	for _, k := range engine.RecordedSkipKinds() {
		if !kinds[string(k)] {
			t.Errorf("no frozen journal holds skip kind %q", k)
		}
	}
}

// TestProductionGuideListsEveryJournalKind: the journal field table in
// docs/10-running-in-production.md names every failure stage and skip kind
// the engine records.
func TestProductionGuideListsEveryJournalKind(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "10-running-in-production.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := func(field string) string {
		for _, line := range strings.Split(string(page), "\n") {
			if strings.HasPrefix(line, "| `"+field+"` |") {
				return line
			}
		}
		t.Fatalf("the guide has no row for %s", field)
		return ""
	}
	stages, kinds := row("failure_stage"), row("skip_kind")
	for _, s := range engine.RecordedFailureStages() {
		if !strings.Contains(stages, "`"+string(s)+"`") {
			t.Errorf("the guide's failure_stage row does not list %q", s)
		}
	}
	for _, k := range engine.RecordedSkipKinds() {
		if !strings.Contains(kinds, "`"+string(k)+"`") {
			t.Errorf("the guide's skip_kind row does not list %q", k)
		}
	}
}

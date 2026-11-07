// Package journal_test: the journal line's format version.
package journal_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestReadRunRefusesANewerFormatByName proves a line written by a newer
// build is refused for its version, before an unknown key it carries can be
// all the error says, and that a line with no format (written before the
// field existed) and one at this build's format both read.
func TestReadRunRefusesANewerFormatByName(t *testing.T) {
	store, root := newStore(t)
	if err := store.Record(context.Background(), []engine.JournalEntry{entry("run-a", 1, "tasks[0]")}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	path := filepath.Join(root, ".pleiades", "journal", "run-a.jsonl")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(string(written), "\n")
	if !strings.HasPrefix(line, `{"format":1,`) {
		t.Fatalf("a written line does not lead with its format: %s", line)
	}
	for _, tc := range []struct {
		name, line, wantErr string
	}{
		{name: "this build's format", line: line},
		{name: "no format, from before the field", line: strings.Replace(line, `"format":1,`, "", 1)},
		{name: "a newer format with a key this build lacks", line: strings.Replace(line, `"format":1,`, `"format":2,"a_newer_key":true,`, 1),
			wantErr: "is journal format 2, and this build reads formats up to 1"},
		{name: "a newer format alone", line: strings.Replace(line, `"format":1,`, `"format":2,`, 1),
			wantErr: "is journal format 2"},
		{name: "a negative format", line: strings.Replace(line, `"format":1,`, `"format":-1,`, 1),
			wantErr: "names format -1, which is not a format version"},
		{name: "not an object at all", line: `["format",2]`, wantErr: "not a journal entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := journal.ReadRun(root, "run-a")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

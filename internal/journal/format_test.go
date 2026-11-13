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
	if !strings.HasPrefix(line, `{"format":3,`) {
		t.Fatalf("a written line does not lead with its format: %s", line)
	}
	for _, tc := range []struct {
		name, line, wantErr string
	}{
		{name: "this build's format", line: line},
		{name: "no format, from before the field", line: strings.Replace(line, `"format":3,`, "", 1)},
		{name: "format 1, from before the Collection identity", line: strings.Replace(line, `"format":3,`, `"format":1,`, 1)},
		{name: "format 2, from before the rendered rollback steps", line: strings.Replace(line, `"format":3,`, `"format":2,`, 1)},
		{name: "a newer format with a key this build lacks", line: strings.Replace(line, `"format":3,`, `"format":4,"a_newer_key":true,`, 1),
			wantErr: "is journal format 4, and this build reads formats up to 3"},
		{name: "a newer format alone", line: strings.Replace(line, `"format":3,`, `"format":4,`, 1),
			wantErr: "is journal format 4"},
		{name: "a negative format", line: strings.Replace(line, `"format":3,`, `"format":-1,`, 1),
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

// TestReadRunHoldsTheCollectionIdentityToWhatABuildWrites: a format-2 line's
// identity and undo digest read as written; the same line is refused when
// it claims an earlier format, when its identity fails Shape (a manifest
// digest of the wrong shape, a group holding an escape), and when its undo
// digest is not a digest. A group holding a character only Go's Unicode
// tables call a format character (a soft hyphen) reads, though Valid, which
// the engine holds an identity to before writing it, refuses it: those
// tables change with the Go release, and a journal written correctly under
// one must stay readable under the next (LESSONS 415).
func TestReadRunHoldsTheCollectionIdentityToWhatABuildWrites(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-2", "entries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(string(frozen), "\n", 2)[0]
	manifests := `"manifests":"sha256:` + strings.Repeat("c", 64) + `"`
	undo := `"inverse_spec_digest":"sha256:` + strings.Repeat("d", 64) + `"`
	for _, want := range []string{`"format":2,`, manifests, undo, `"group":"acme-files"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("the frozen line lacks %s, so the cases below would test nothing:\n%s", want, line)
		}
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, goldenRunID+".jsonl")
	for _, tc := range []struct {
		name, old, new, wantErr string
		// unicodeOnly marks a group whose only fault is one Go's Unicode
		// tables decide, which the reader takes and Valid refuses.
		unicodeOnly bool
	}{
		{name: "as written"},
		{name: "claiming format 1", old: `"format":2,`, new: `"format":1,`, wantErr: "is journal format 1 and carries a Collection identity"},
		{name: "claiming no format", old: `"format":2,`, new: ``, wantErr: "is journal format 1 and carries a Collection identity"},
		{name: "a manifest digest of the wrong shape", old: manifests, new: `"manifests":"sha256:00"`, wantErr: "records a Collection identity no build writes"},
		{name: "a group holding an escape", old: `"group":"acme-files"`, new: `"group":"acme\u001bfiles"`, wantErr: "holds a control code"},
		{name: "a group holding a soft hyphen", old: `"group":"acme-files"`, new: `"group":"acme\u00adfiles"`, unicodeOnly: true},
		{name: "a group holding a direction override", old: `"group":"acme-files"`, new: `"group":"acme\u202efiles"`, unicodeOnly: true},
		{name: "an undo digest of the wrong shape", old: undo, new: `"inverse_spec_digest":"md5:00"`, wantErr: "carries undo digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := line
			if tc.old != "" {
				text = strings.Replace(line, tc.old, tc.new, 1)
			}
			if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			entries, err := journal.ReadRun(root, goldenRunID)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error %v, want %q", err, tc.wantErr)
			}
			// The identity read back is one the engine would not have
			// written under this toolchain: the reader accepts what a writer
			// of another release could have, and the writer's rule still
			// holds here.
			if tc.unicodeOnly && err == nil {
				if len(entries.Entries) == 0 || entries.Entries[0].Collection.Valid() == nil {
					t.Errorf("the identity read back passes Valid, so this case tests nothing: %+v", entries.Entries)
				}
			}
		})
	}
}

// TestReadRunNamesNoRecordedTextUnbounded: a hand-edited journal line can
// carry an undo digest, an outcome or a node of half a megabyte holding
// direction overrides; the refusal names it bounded and escaped.
func TestReadRunNamesNoRecordedTextUnbounded(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-2", "entries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(string(frozen), "\n", 2)[0]
	undo := `"inverse_spec_digest":"sha256:` + strings.Repeat("d", 64) + `"`
	if !strings.Contains(line, undo) {
		t.Fatalf("the frozen line lacks %s", undo)
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	override := string(rune(0x202e))
	huge := strings.Repeat("zq9"+override, 1<<19/6)
	text := strings.Replace(line, undo, `"inverse_spec_digest":"`+huge+`"`, 1)
	if err := os.WriteFile(filepath.Join(dir, goldenRunID+".jsonl"), []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = journal.ReadRun(root, goldenRunID)
	if err == nil || !strings.Contains(err.Error(), "undo digest") {
		t.Fatalf("ReadRun: %v, want the undo digest refused", err)
	}
	if len(err.Error()) > 2048 || strings.Contains(err.Error(), override) {
		t.Errorf("the refusal of %d bytes repeats the digest: %.300q", len(err.Error()), err.Error())
	}
}

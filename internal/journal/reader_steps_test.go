// Tests that the strict journal reader holds a line's rendered rollback steps
// (engine.JournalEntry.AuthoredSteps) to what a build writes, on the frozen
// format-3 line.
package journal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// TestReadRunHoldsTheRenderedStepsToWhatABuildWrites: the frozen format-3
// line's steps read as written; the same line is refused when it claims an
// earlier format, and when a step carries an unknown reason, a reason beside
// values, values beside a reason, a value a terminal would act on, a digest of
// the wrong shape, or an index given twice.
func TestReadRunHoldsTheRenderedStepsToWhatABuildWrites(t *testing.T) {
	frozen, err := os.ReadFile(filepath.Join("testdata", "format-3", "entries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(string(frozen), "\n", 2)[0]
	complete := `"params":[{"key":"path","text":"/etc/motd"}],"complete":true`
	withheld := `"complete":false,"withheld":"not_recordable"`
	digest := `"step_digest":"sha256:` + strings.Repeat("f", 64) + `"`
	for _, want := range []string{`"format":3,`, complete, withheld, digest, `"index":2`} {
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
	}{
		{name: "as written"},
		{name: "claiming format 2", old: `"format":3,`, new: `"format":2,`, wantErr: "is journal format 2 and carries rendered rollback steps"},
		{name: "an unknown reason", old: withheld, new: `"complete":false,"withheld":"because"`, wantErr: "unknown reason"},
		{name: "a reason beside values", old: complete, new: `"params":[{"key":"path","text":"/etc/motd"}],"complete":true,"withheld":"secret"`, wantErr: "complete but carries a reason"},
		{name: "values beside a reason", old: withheld, new: `"params":[{"key":"path","text":"/x"}],` + withheld, wantErr: "incomplete with an unknown reason or with values"},
		{name: "a value a terminal acts on", old: complete, new: `"params":[{"key":"path","text":"/etc/\u001b[2Jmotd"}],"complete":true`, wantErr: "never holds"},
		{name: "a digest of the wrong shape", old: digest, new: `"step_digest":"md5:00"`, wantErr: "no step digest"},
		{name: "an index given twice", old: `"index":2`, new: `"index":1`, wantErr: "negative or repeated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := line
			if tc.old != "" {
				text = strings.Replace(line, tc.old, tc.new, 1)
			}
			if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := journal.ReadRun(root, goldenRunID)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}

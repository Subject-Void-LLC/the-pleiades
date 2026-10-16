// Tests for what a failure-pattern review of build.digests found: an undo
// record dropped while a file it could restore was left changed, one too
// large to read back, a set whose undo could not run, a path given twice,
// and device text of any size in an error.
package digests_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/build/digests"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// undoRecords lists the undo records kept in the login's state directory.
func undoRecords(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "pleiades", "build-digests"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids
}

// TestRefresh_KeepsItsUndoWhenAFileCannotBePutBack: a refresh that fails
// after rewriting a file it then cannot put back keeps its undo record
// and names it, and replaying that record restores the file while
// skipping the one the run never wrote.
func TestRefresh_KeepsItsUndoWhenAFileCannotBePutBack(t *testing.T) {
	testsupport.Require(t, "non-root", os.Geteuid() != 0, "the put-back is made to fail by a read-only directory, which root writes into anyway")
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11"), "nginx:1.27": digest("22")}}
	h := newHarness(t, reg)
	first := h.write(t, "a/Dockerfile", "FROM alpine:3.20\n", 0o644)
	second := h.write(t, "b/Dockerfile", "FROM nginx:1.27\n", 0o644)
	digests.SetAfterWrite(t, func(p string) {
		if p == first {
			// a's directory refuses the put-back's new file, and b is
			// edited, so b's write fails and a cannot be put back.
			_ = os.Chmod(filepath.Dir(first), 0o555)
			_ = os.WriteFile(second, []byte("FROM nginx:1.27\n# edited by someone else\n"), 0o644)
		}
	})
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(first), 0o755) })

	_, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(first, second)}))
	ids := undoRecords(t)
	if err == nil || len(ids) != 1 || !strings.Contains(err.Error(), "except") || !strings.Contains(err.Error(), "undo record "+ids[0]) {
		t.Fatalf("Refresh = %v; records %v", err, ids)
	}
	if read(t, first) != "FROM alpine:3.20@"+digest("11")+"\n" {
		t.Fatalf("a = %q, want it left as written, which is what the record restores", read(t, first))
	}

	_ = os.Chmod(filepath.Dir(first), 0o755)
	digests.SetAfterWrite(t, nil)
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"undo": ids[0]})); err != nil {
		t.Fatalf("replaying the kept record: %v", err)
	}
	if read(t, first) != "FROM alpine:3.20\n" || !strings.Contains(read(t, second), "edited by someone else") {
		t.Errorf("after the replay: a %q, b %q", read(t, first), read(t, second))
	}
}

// TestRefresh_RefusesAnUndoRecordTooLargeToReadBack: a refresh whose undo
// record would be over what a replay reads is refused before any file is
// written, rather than writing a record no replay can read.
func TestRefresh_RefusesAnUndoRecordTooLargeToReadBack(t *testing.T) {
	h := newHarness(t, nil)
	var files []any
	var set []any
	dir := strings.Repeat("d", 120)
	for i := range 200 {
		p := h.write(t, fmt.Sprintf("%s/%03d/Dockerfile", dir, i), strings.Repeat("FROM alpine:3.20\n", 40), 0o644)
		files = append(files, p)
		for line := 1; line <= 40; line++ {
			set = append(set, map[string]any{"file": p, "line": line, "from": "alpine:3.20", "to": "alpine:3.20@" + digest("55")})
		}
	}
	_, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": files, "set": set}))
	if err == nil || !strings.Contains(err.Error(), "nothing was written") || !strings.Contains(err.Error(), "over the 1048576 one is read back at") {
		t.Fatalf("Refresh = %v", err)
	}
	if got := read(t, files[0].(string)); got != strings.Repeat("FROM alpine:3.20\n", 40) {
		t.Errorf("a file was written: %q", got[:40])
	}
	if ids := undoRecords(t); len(ids) != 0 {
		t.Errorf("records %v left behind", ids)
	}
}

// TestRefresh_SetRefusesAFromThatDoesNotParse: a set's from is what its
// undo writes back, and a replay holds it to Parse, so one that does not
// parse is refused before anything is written.
func TestRefresh_SetRefusesAFromThatDoesNotParse(t *testing.T) {
	h := newHarness(t, nil)
	file := h.write(t, "Dockerfile", "FROM Alpine:3.20\n", 0o644)
	set := []any{map[string]any{"file": file, "line": 1, "from": "Alpine:3.20", "to": "alpine:3.20@" + digest("55")}}
	_, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file), "set": set}))
	if err == nil || !strings.Contains(err.Error(), "set[0]: from:") {
		t.Fatalf("Refresh = %v", err)
	}
	if read(t, file) != "FROM Alpine:3.20\n" {
		t.Errorf("file = %q", read(t, file))
	}
}

// TestRefresh_ASetAlreadyAppliedIsNoChange: running the same set twice
// changes nothing the second time rather than failing.
func TestRefresh_ASetAlreadyAppliedIsNoChange(t *testing.T) {
	h := newHarness(t, nil)
	file := h.write(t, "Dockerfile", "FROM alpine:3.20\n", 0o644)
	set := []any{map[string]any{"file": file, "line": 1, "from": "alpine:3.20", "to": "alpine:3.20@" + digest("55")}}
	for run := range 2 {
		res, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file), "set": set}))
		if err != nil || res.Changed != (run == 0) {
			t.Fatalf("run %d: %+v, %v", run+1, res, err)
		}
	}
}

// TestRefresh_RefusesAPathGivenTwice: a file named twice would be read
// twice and its second write refused as edited since it was read.
func TestRefresh_RefusesAPathGivenTwice(t *testing.T) {
	h := newHarness(t, nil)
	file := h.write(t, "Dockerfile", "FROM alpine:3.20\n", 0o644)
	_, err := digests.CheckDigests(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file, filepath.Dir(file)+"/./Dockerfile")}))
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("CheckDigests = %v", err)
	}
}

// TestCheck_BoundsDeviceTextInItsError: a reference of any size from the
// device reaches the error cut and escaped, not whole.
func TestCheck_BoundsDeviceTextInItsError(t *testing.T) {
	h := newHarness(t, nil)
	huge := strings.Repeat("a", 100_000) + "\x1b[2J:1"
	file := h.write(t, "Dockerfile", "FROM "+huge+"\n", 0o644)
	_, err := digests.CheckDigests(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file)}))
	if err == nil || len(err.Error()) > 4096 || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "more bytes") {
		t.Fatalf("CheckDigests error is %d bytes: %.200q", len(fmt.Sprint(err)), err)
	}
	refs, _ := h.rc.stats["references"].([]any)
	if len(refs) != 1 || len(refs[0].(map[string]any)["reference"].(string)) > 1024 {
		t.Errorf("the references stat holds %d entries; the first is not bounded", len(refs))
	}
}

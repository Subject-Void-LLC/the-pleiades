// Tests for build.digests.check and build.digests.refresh against real
// files on a real SSH session.
package digests_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/build/digests"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestRegistered declares what the design note promises.
func TestRegistered(t *testing.T) {
	for name, want := range map[string]struct{ readOnly, reversible bool }{
		"build.digests.check": {true, false}, "build.digests.refresh": {false, true},
	} {
		d, ok := collection.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		r := d.Manifest.Reversibility
		if r.ReadOnly != want.readOnly || r.Reversible != want.reversible || !d.Manifest.SupportsCheck {
			t.Errorf("%s = %+v", name, d.Manifest)
		}
	}
}

// TestCheck_NamesEveryOffenderAndPassesWhenPinned: an unpinned, a
// tag-only, a digest-only and an unresolvable reference are offenders, by
// file and line; a file whose every reference is pinned passes.
func TestCheck_NamesEveryOffenderAndPassesWhenPinned(t *testing.T) {
	h := newHarness(t, nil)
	dockerfile := h.write(t, "Dockerfile", "ARG BASE\nFROM ${BASE} AS base\nFROM alpine:3.20\nCOPY --from=nginx@"+digest("ab")+" /a /a\n", 0o644)
	compose := h.write(t, "compose.yaml", "services:\n  web:\n    image: nginx\n  db:\n    image: postgres:16@"+digest("cd")+"\n", 0o644)
	_, err := digests.CheckDigests(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(dockerfile, compose)}))
	if err == nil {
		t.Fatal("unpinned references passed")
	}
	for _, want := range []string{"4 of 5 image reference(s)", "Dockerfile:2 ${BASE}: it cannot be read as written", "Dockerfile:3 alpine:3.20: it names a tag but no digest", "Dockerfile:4 nginx@" + digest("ab") + ": it names a digest but no tag", "compose.yaml:3 nginx: it names neither"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v\nwant it to say %q", err, want)
		}
	}
	pinned := h.write(t, "pinned/Dockerfile", "FROM alpine:3.20@"+digest("ef")+"\n", 0o644)
	if _, err := digests.CheckDigests(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(pinned)})); err != nil {
		t.Fatalf("a pinned file = %v", err)
	}
	if h.rc.stats["offenders"] != 0 {
		t.Errorf("offenders = %v", h.rc.stats["offenders"])
	}
}

// TestCheck_ResolveReportsDrift: a pinned digest its tag no longer names is
// drift; one it still names is not; resolving needs an engine.
func TestCheck_ResolveReportsDrift(t *testing.T) {
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11"), "nginx:1.27": digest("22")}}
	h := newHarness(t, reg)
	file := h.write(t, "Dockerfile", "FROM alpine:3.20@"+digest("11")+"\nFROM nginx:1.27@"+digest("99")+"\n", 0o644)
	_, err := digests.CheckDigests(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file), "resolve": true}))
	if err == nil || !strings.Contains(err.Error(), "its tag 1.27 now names "+digest("22")) || strings.Contains(err.Error(), "alpine") {
		t.Fatalf("drift = %v", err)
	}
	noEngine := newHarness(t, nil)
	file = noEngine.write(t, "Dockerfile", "FROM alpine:3.20@"+digest("11")+"\n", 0o644)
	if _, err := digests.CheckDigests(context.Background(), noEngine.rc, noEngine.device, params(map[string]any{"files": paths(file), "resolve": true})); err == nil || !strings.Contains(err.Error(), "no") {
		t.Fatalf("resolve without an engine = %v", err)
	}
}

// TestCheck_RefusesAFileOfNoKnownKind, a missing one, and a symlink.
func TestCheck_RefusesWhatItCannotRead(t *testing.T) {
	h := newHarness(t, nil)
	notes := h.write(t, "notes.txt", "FROM alpine\n", 0o644)
	target := h.write(t, "real/Dockerfile", "FROM alpine:3.20\n", 0o644)
	link := h.project + "/Dockerfile"
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{"no known kind": notes, "missing": h.project + "/absent/Dockerfile", "a symlink": link} {
		if _, err := digests.CheckDigests(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file)})); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}

// TestRefresh_PinsKeepsModeAndRecordsItsUndo rewrites each reference to
// what its tag names now, in a Dockerfile (through an ARG's default) and a
// Compose file, keeps each file's mode, reports the changes, and records
// an undo that, run, puts them back exactly.
func TestRefresh_PinsKeepsModeAndRecordsItsUndo(t *testing.T) {
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11"), "nginx:1.27": digest("22"), "postgres:16": digest("33")}}
	h := newHarness(t, reg)
	dockerfileText := "ARG BASE=alpine:3.20\nFROM ${BASE}\n# a comment stays\nCOPY --from=nginx:1.27@" + digest("99") + " /a /a\n"
	composeText := "services:\n  db:\n    image: \"postgres:16\"   # kept\n"
	dockerfile := h.write(t, "Dockerfile", dockerfileText, 0o640)
	compose := h.write(t, "compose.yaml", composeText, 0o644)

	res, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(dockerfile, compose)}))
	if err != nil || !res.Changed {
		t.Fatalf("Refresh = %+v, %v", res, err)
	}
	if got, want := read(t, dockerfile), "ARG BASE=alpine:3.20@"+digest("11")+"\nFROM ${BASE}\n# a comment stays\nCOPY --from=nginx:1.27@"+digest("22")+" /a /a\n"; got != want {
		t.Errorf("Dockerfile =\n%s\nwant\n%s", got, want)
	}
	if got := read(t, compose); got != "services:\n  db:\n    image: \"postgres:16@"+digest("33")+"\"   # kept\n" {
		t.Errorf("compose.yaml = %q", got)
	}
	if info, _ := os.Stat(dockerfile); info.Mode().Perm() != 0o640 {
		t.Errorf("the Dockerfile's mode became %v", info.Mode().Perm())
	}
	if changes, _ := h.rc.stats["changes"].([]any); len(changes) != 3 {
		t.Errorf("changes = %v", h.rc.stats["changes"])
	}

	// The undo, run as a rollback would run it.
	inverse := h.rc.stats["inverse"].(map[string]any)
	if inverse["fqcn"] != "build.digests.refresh" {
		t.Fatalf("inverse = %v", inverse)
	}
	// The journal keeps only the record's id; the record is the login's own.
	recorded := inverse["params"].(map[string]any)
	id, _ := recorded["undo"].(string)
	if len(recorded) != 1 || len(id) != 32 {
		t.Fatalf("the undo's params = %v, want only a record id", recorded)
	}
	dir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "pleiades", "build-digests")
	record := filepath.Join(dir, id+".json")
	if info, err := os.Stat(record); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the undo record: %v, %v", info, err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the undo record directory: %v, %v", info, err)
	}

	undo := params(recorded)
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, undo); err != nil {
		t.Fatalf("the undo: %v", err)
	}
	if read(t, dockerfile) != dockerfileText || read(t, compose) != composeText {
		t.Errorf("the undo left\n%s\n%s", read(t, dockerfile), read(t, compose))
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Errorf("the replayed record is still there: %v", err)
	}
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, undo); err == nil || !strings.Contains(err.Error(), "does not exist on the device") {
		t.Errorf("replaying a spent record = %v", err)
	}
}

// TestRefresh_AnUndoRefusesAFileEditedSince: the undo writes a reference
// back only where the file still holds what the run wrote, and a record
// id that is not one is refused before anything is read.
func TestRefresh_AnUndoRefusesAFileEditedSince(t *testing.T) {
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11")}}
	h := newHarness(t, reg)
	dockerfile := h.write(t, "Dockerfile", "FROM alpine:3.20\n", 0o644)
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(dockerfile)})); err != nil {
		t.Fatal(err)
	}
	undo := params(h.rc.stats["inverse"].(map[string]any)["params"].(map[string]any))
	h.write(t, "Dockerfile", "FROM alpine:3.21\n", 0o644)
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, undo); err == nil || !strings.Contains(err.Error(), "holds no image reference") {
		t.Fatalf("an undo over an edited file = %v", err)
	}
	if read(t, dockerfile) != "FROM alpine:3.21\n" {
		t.Errorf("the edited file became %q", read(t, dockerfile))
	}
	for _, id := range []string{"../../etc/passwd", "ABC", strings.Repeat("a", 33)} {
		if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"undo": id})); err == nil || !strings.Contains(err.Error(), "is not an undo record's id") {
			t.Errorf("undo %q = %v", id, err)
		}
	}
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"undo": strings.Repeat("a", 32), "set": []any{map[string]any{"file": dockerfile, "line": 1, "from": "a", "to": "b:1"}}})); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("undo with set = %v", err)
	}
}

// TestRefresh_ACheckWritesNothing reports the changes a run would make.
func TestRefresh_ACheckWritesNothing(t *testing.T) {
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11")}}
	h := newHarness(t, reg)
	text := "FROM alpine:3.20\n"
	file := h.write(t, "Dockerfile", text, 0o644)
	res, err := digests.CheckRefresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file)}))
	if err != nil || !res.Changed || read(t, file) != text {
		t.Fatalf("CheckRefresh = %+v, %v; file %q", res, err, read(t, file))
	}
	if changes, _ := h.rc.stats["changes"].([]any); len(changes) != 1 {
		t.Errorf("changes = %v", h.rc.stats["changes"])
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "pleiades")); !os.IsNotExist(err) {
		t.Errorf("a check left an undo record behind: %v", err)
	}
}

// TestRefresh_RefusesBeforeWritingAnything: a reference the registry does
// not know, one with no tag, and one from an ARG with no default each
// refuse the task with every file as it was.
func TestRefresh_RefusesBeforeWritingAnything(t *testing.T) {
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11")}}
	for name, tc := range map[string]struct{ text, want string }{
		"unknown to the registry": {"FROM alpine:3.20\nFROM gone:1\n", "gone:1"},
		"no tag":                  {"FROM alpine:3.20\nFROM busybox@" + digest("44") + "\n", "has no tag"},
		"an ARG with no default":  {"FROM alpine:3.20\nARG X\nFROM $X\n", "cannot be refreshed"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, reg)
			file := h.write(t, "Dockerfile", tc.text, 0o644)
			if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file)})); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Refresh = %v, want it to say %q", err, tc.want)
			}
			if read(t, file) != tc.text {
				t.Error("a refused refresh wrote the file")
			}
		})
	}
}

// TestRefresh_AFileEditedMeanwhileIsNotOverwritten: a file changed after
// it was read refuses the task, and a file already rewritten is put back.
func TestRefresh_AFileEditedMeanwhileIsNotOverwritten(t *testing.T) {
	reg := &registry{digests: map[string]string{"alpine:3.20": digest("11"), "nginx:1.27": digest("22")}}
	h := newHarness(t, reg)
	first := h.write(t, "a/Dockerfile", "FROM alpine:3.20\n", 0o644)
	second := h.write(t, "b/Dockerfile", "FROM nginx:1.27\n", 0o644)
	reg.during = func(ref string) {
		if ref == "nginx:1.27" {
			_ = os.WriteFile(second, []byte("FROM nginx:1.27\n# edited by someone else\n"), 0o644)
		}
	}
	_, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(first, second)}))
	if err == nil || !strings.Contains(err.Error(), "changed on the device after it was read") || !strings.Contains(err.Error(), "put back") {
		t.Fatalf("Refresh = %v", err)
	}
	if read(t, first) != "FROM alpine:3.20\n" || !strings.Contains(read(t, second), "edited by someone else") {
		t.Errorf("files after = %q, %q", read(t, first), read(t, second))
	}
}

// TestRefresh_SetRewritesExactlyAndRefusesAMismatch.
func TestRefresh_SetRewritesExactlyAndRefusesAMismatch(t *testing.T) {
	h := newHarness(t, nil)
	file := h.write(t, "Dockerfile", "FROM alpine:3.20\n", 0o644)
	set := []any{map[string]any{"file": file, "line": 1, "from": "alpine:3.20", "to": "alpine:3.20@" + digest("55")}}
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file), "set": set})); err != nil {
		t.Fatal(err)
	}
	if read(t, file) != "FROM alpine:3.20@"+digest("55")+"\n" {
		t.Errorf("file = %q", read(t, file))
	}
	stale := []any{map[string]any{"file": file, "line": 1, "from": "alpine:3.20", "to": "alpine:3.21"}}
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file), "set": stale})); err == nil || !strings.Contains(err.Error(), "holds no image reference") {
		t.Fatalf("a stale set = %v", err)
	}
	hostile := []any{map[string]any{"file": file, "line": 1, "from": "alpine:3.20@" + digest("55"), "to": "x;rm -rf /"}}
	if _, err := digests.Refresh(context.Background(), h.rc, h.device, params(map[string]any{"files": paths(file), "set": hostile})); err == nil {
		t.Fatal("a set writing a hostile reference was accepted")
	}
}

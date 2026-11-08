// In-process tests of `pleiades rollback --allow-skew` (Phase 136b): journal
// fixtures written through the real file journal, read back by the strict
// reader, and planned by the real planner against the real built-in
// catalog, each one through the command itself. A plan the command accepts
// then runs and fails on the device's missing credential, before any
// device is contacted; what these hold is the plan and the refusals.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// installedDirectory is this build's identity of file.directory's group and
// the digest of its undo through file.remove.
func installedDirectory(t *testing.T) (wire.CollectionIdentity, string) {
	t.Helper()
	id, err := collection.BuiltinIdentity("file.directory", buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	emitter, _ := collection.BuiltinManifest("file.directory")
	target, _ := collection.ManifestOf("file.remove")
	for _, spec := range emitter.Reversibility.Inverses {
		if spec.FQCN == "file.remove" {
			return id, collection.UndoDigest(spec, fragment.ByNameIn(target))
		}
	}
	t.Fatal("file.directory declares no undo through file.remove")
	return id, ""
}

// foreign is a digest no build computes for file.directory.
func foreign(c string) string { return wire.ManifestDigestPrefix + strings.Repeat(c, 64) }

// skewProject is rollbackProject's project, with the directory's entry
// recorded under id and undoDigest, and its recorded undo parameters edited
// by params when given.
func skewProject(t *testing.T, id wire.CollectionIdentity, undoDigest string, params func(*engine.JournalEntry)) string {
	t.Helper()
	dir := rollbackProject(t, false)
	path := filepath.Join(dir, ".pleiades", "journal", cliRunID+".jsonl")
	run, err := journal.ReadRun(dir, cliRunID)
	if err != nil {
		t.Fatal(err)
	}
	entries := run.Entries
	entries[0].Collection, entries[0].InverseSpecDigest = id, undoDigest
	if params != nil {
		params(&entries[0])
	}
	// Written again through the file journal, as a run writes it; the seal
	// rollbackProject wrote stays.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store, err := journal.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	return dir
}

// rollbackJSON runs `pleiades rollback --json` with args and returns its
// report.
func rollbackJSON(t *testing.T, dir string, args ...string) (runReport, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		err = runRollback(append([]string{cliRunID, "--dir", dir, "--leave", "tasks[1]", "--json"}, args...))
	})
	var rep runReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || rep.Rollback == nil {
		t.Fatalf("rollback --json %v printed no report (%v):\n%s", args, jerr, out)
	}
	return rep, err
}

// olderDirectory is an identity an older build recorded for file.directory's
// group: another version and other declarations.
func olderDirectory(id wire.CollectionIdentity) wire.CollectionIdentity {
	id.Version, id.Manifests = "0.3.9", foreign("a")
	return id
}

func TestRollbackCommand_AnUndoRecordedUnderThisBuildPlansWithNoWarning(t *testing.T) {
	id, undo := installedDirectory(t)
	rep, _ := rollbackJSON(t, skewProject(t, id, undo, nil))
	if len(rep.Rollback.Problems) != 0 || len(rep.Rollback.Levels) != 1 || rep.Rollback.Levels[0][0].SkewAccepted || len(rep.Rollback.Warnings) != 0 {
		t.Errorf("rollback = %+v, want the undo planned with no skew and no warning", rep.Rollback)
	}
}

func TestRollbackCommand_AChangedGroupWithItsUndoUnchangedWarns(t *testing.T) {
	id, undo := installedDirectory(t)
	dir := skewProject(t, olderDirectory(id), undo, nil)
	rep, _ := rollbackJSON(t, dir)
	if len(rep.Rollback.Problems) != 0 || len(rep.Rollback.Levels) != 1 || len(rep.Rollback.Warnings) != 1 {
		t.Fatalf("rollback = %+v, want the undo planned with one warning", rep.Rollback)
	}
	for _, want := range []string{strconv.Quote("0.3.9"), strconv.Quote(buildinfo.Version()), foreign("a"), id.Manifests} {
		if !strings.Contains(rep.Rollback.Warnings[0].Message, want) {
			t.Errorf("the warning does not name %s: %s", want, rep.Rollback.Warnings[0].Message)
		}
	}
	// The text view prints it before the plan.
	out := captureStdout(t, func() { _ = runRollback([]string{cliRunID, "--dir", dir, "--leave", "tasks[1]"}) })
	if !strings.Contains(out, "warning: tasks[0] on web1: file.directory's Collection declarations changed since the run") {
		t.Errorf("the text view does not print the warning:\n%s", out)
	}
}

// TestRollbackCommand_AChangedUndoNeedsAllowSkew: refused naming the flag,
// both versions and both undo digests; accepted with it, the plan's step
// carries the mark and a warning names the digests it accepted.
func TestRollbackCommand_AChangedUndoNeedsAllowSkew(t *testing.T) {
	id, undo := installedDirectory(t)
	dir := skewProject(t, olderDirectory(id), foreign("b"), nil)
	var err error
	out := captureStdout(t, func() { err = runRollback([]string{cliRunID, "--dir", dir, "--leave", "tasks[1]"}) })
	if err == nil || !strings.Contains(out, "accept it with --allow-skew tasks[0]") || !strings.Contains(out, foreign("b")) || !strings.Contains(out, undo) {
		t.Fatalf("rollback = %v, want a refusal naming --allow-skew tasks[0] and both undo digests:\n%s", err, out)
	}
	rep, _ := rollbackJSON(t, dir)
	if len(rep.Rollback.Problems) != 1 || rep.Rollback.Problems[0].Accept != "--allow-skew tasks[0]" || rep.Outcome.Status != "invalid" {
		t.Errorf("rollback --json = %+v, want the one problem accepted by --allow-skew tasks[0]", rep.Rollback.Problems)
	}

	rep, _ = rollbackJSON(t, dir, "--allow-skew", "tasks[0]")
	if len(rep.Rollback.Problems) != 0 || len(rep.Rollback.Levels) != 1 || !rep.Rollback.Levels[0][0].SkewAccepted {
		t.Fatalf("rollback --allow-skew = %+v, want the undo planned and marked", rep.Rollback)
	}
	if len(rep.Rollback.Warnings) != 1 || !strings.Contains(rep.Rollback.Warnings[0].Message, "--allow-skew accepted") ||
		!strings.Contains(rep.Rollback.Warnings[0].Message, foreign("b")) || !strings.Contains(rep.Rollback.Warnings[0].Message, undo) {
		t.Errorf("warnings %+v, want one naming the undo digests --allow-skew accepted", rep.Rollback.Warnings)
	}
}

// TestRollbackCommand_AllowSkewNeverAcceptsAnUndeclaredOrMissingKey: a
// recorded key this build's undo does not record, and a parameter its undo
// method requires that the run did not record, are refused naming --leave,
// with and without --allow-skew.
func TestRollbackCommand_AllowSkewNeverAcceptsAnUndeclaredOrMissingKey(t *testing.T) {
	id, _ := installedDirectory(t)
	yes := true
	for name, tc := range map[string]struct {
		edit func(*engine.JournalEntry)
		want string
	}{
		"a key Record lacks": {func(e *engine.JournalEntry) {
			e.InverseParamKeys = append(e.InverseParamKeys, "recurse")
			e.InverseParams = append(e.InverseParams, engine.InverseParam{Key: "recurse", Bool: &yes})
		}, `"recurse"`},
		"a required key not recorded": {func(e *engine.JournalEntry) {
			e.InverseParamKeys, e.InverseParams = []string{}, []engine.InverseParam{}
		}, `requires "path"`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := skewProject(t, olderDirectory(id), foreign("b"), tc.edit)
			for _, extra := range [][]string{nil, {"--allow-skew", "tasks[0]"}} {
				rep, err := rollbackJSON(t, dir, extra...)
				if err == nil || len(rep.Rollback.Problems) != 1 || rep.Rollback.Problems[0].Accept != "--leave tasks[0]" ||
					!strings.Contains(rep.Rollback.Problems[0].Reason, tc.want) || !strings.Contains(rep.Rollback.Problems[0].Reason, strconv.Quote("0.3.9")) {
					t.Errorf("%v: problems %+v, want one naming %s and the recorded version, accepted by --leave alone", extra, rep.Rollback.Problems, tc.want)
				}
			}
		})
	}
}

// TestRollbackCommand_AMalformedIdentityStopsTheReader: a digest of 63
// digits is no identity a build writes, so the strict reader refuses the
// journal by its line before anything is planned.
func TestRollbackCommand_AMalformedIdentityStopsTheReader(t *testing.T) {
	id, undo := installedDirectory(t)
	id.Manifests = id.Manifests[:len(id.Manifests)-1]
	dir := skewProject(t, id, undo, nil)
	err := runRollback([]string{cliRunID, "--dir", dir, "--leave", "tasks[1]", "--allow-skew", "tasks[0]"})
	if err == nil || !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "Collection identity no build writes") {
		t.Errorf("rollback = %v, want the reader's refusal of line 1", err)
	}
}

// TestRollbackCommand_AFormatOneEntryWarnsThatItHasNoIdentity: a journal
// line written before the identity existed plans as it always has, and says
// it was checked against this build alone.
func TestRollbackCommand_AFormatOneEntryWarnsThatItHasNoIdentity(t *testing.T) {
	dir := rollbackProject(t, false)
	path := filepath.Join(dir, ".pleiades", "journal", cliRunID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The lines as 0.3.x wrote them: no format key at all.
	var lines [][]byte
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "format")
		out, _ := json.Marshal(m)
		lines = append(lines, out)
	}
	if err := os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, _ := rollbackJSON(t, dir)
	if len(rep.Rollback.Problems) != 0 || len(rep.Rollback.Levels) != 1 || len(rep.Rollback.Warnings) != 1 ||
		!strings.Contains(rep.Rollback.Warnings[0].Message, "recorded with no Collection identity") {
		t.Errorf("rollback = %+v, want the undo planned with the no-identity warning", rep.Rollback)
	}
}

// TestRollbackCommand_RefusesANeedlessAllowSkew: the CLI plans and runs in
// one process, so a node named to --allow-skew with no skew, or one the
// journal does not name, is refused, and nothing but dropping the name
// answers it.
func TestRollbackCommand_RefusesANeedlessAllowSkew(t *testing.T) {
	id, undo := installedDirectory(t)
	dir := skewProject(t, id, undo, nil)
	for node, want := range map[string]string{
		"tasks[0]": "drop --allow-skew tasks[0]",
		"tasks[9]": "no entry of run " + cliRunID,
	} {
		rep, err := rollbackJSON(t, dir, "--allow-skew", node)
		if err == nil || len(rep.Rollback.Problems) != 1 || rep.Rollback.Problems[0].Accept != "" || !strings.Contains(rep.Rollback.Problems[0].Reason, want) {
			t.Errorf("--allow-skew %s: problems %+v, want one nothing accepts, saying %q", node, rep.Rollback.Problems, want)
		}
	}
}

// TestRollbackCommand_PrintsAGroupEscaped: a stored group holding a
// direction override, which the reader's Shape lets through, is printed
// escaped in the refusal on the terminal and in the report.
func TestRollbackCommand_PrintsAGroupEscaped(t *testing.T) {
	id, _ := installedDirectory(t)
	override := string(rune(0x202e))
	odd := olderDirectory(id)
	odd.Group = "fi" + override + "le"
	dir := skewProject(t, odd, foreign("b"), nil)
	out := captureStdout(t, func() { _ = runRollback([]string{cliRunID, "--dir", dir, "--leave", "tasks[1]"}) })
	if strings.Contains(out, override) || !strings.Contains(out, "u202e") {
		t.Errorf("the refusal does not print the group escaped:\n%s", out)
	}
	rep, _ := rollbackJSON(t, dir)
	if len(rep.Rollback.Problems) != 1 || strings.Contains(rep.Rollback.Problems[0].Reason, override) {
		t.Errorf("the report's reason holds the group raw: %+v", rep.Rollback.Problems)
	}
}

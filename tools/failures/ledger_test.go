// Tests of the ledger file: appending without duplicates, refusing an entry
// of an unknown major version or a line that is not JSON, ignoring a field
// it does not know, and the command line around it.
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAppendLedger_Dedups: an entry already in the ledger, or twice in one
// append, is added once; one differing in any key field is its own.
func TestAppendLedger_Dedups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "pr-check.jsonl")
	e := Entry{Schema: EntrySchema, Source: SourcePRCheck, Commit: testCommit, Kind: KindTest, Leg: "fast", Package: "x/a", Test: "TestA", Verdict: VerdictReal, Run: "r1"}
	other := e
	other.Run = "r2"
	n, err := appendLedger(path, []Entry{e, e})
	if err != nil || n != 1 {
		t.Fatalf("first append: %d, %v", n, err)
	}
	e.Note = "a field outside the key"
	if n, err = appendLedger(path, []Entry{e, other}); err != nil || n != 1 {
		t.Fatalf("second append: %d, %v", n, err)
	}
	got, err := readLedger(path)
	if err != nil || len(got) != 2 || got[0].Note != "" {
		t.Errorf("the ledger holds %+v, %v", got, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the ledger's directory: %v %v", info, err)
	}
}

// TestReadLedger_Contract: a newer schema and a line that is not JSON are
// refused with their line, an unknown field is ignored, an unknown verdict
// kept, and a ledger not yet written is empty.
func TestReadLedger_Contract(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := `{"schema":1,"source":"actions","commit":"abc","kind":"test","leg":"fast","verdict":"flaky","future_field":true}`
	got, err := readLedger(write("ok.jsonl", ok+"\n\n"))
	if err != nil || len(got) != 1 || got[0].Verdict != "flaky" {
		t.Errorf("an entry with a new field and verdict: %+v, %v", got, err)
	}
	for name, text := range map[string]string{
		"newer.jsonl":   `{"schema":2,"source":"actions","commit":"abc","kind":"test","leg":"fast","verdict":"real"}`,
		"garbage.jsonl": ok + "\nnot json\n",
		"empty.jsonl":   `{"schema":1}`,
	} {
		if _, err := readLedger(write(name, text)); err == nil || !strings.Contains(err.Error(), name+":") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, err := readLedger(filepath.Join(dir, "none.jsonl")); err != nil || got != nil {
		t.Errorf("a ledger not written: %v, %v", got, err)
	}
	if _, err := readAll(filepath.Join(write("newer2.jsonl", ""), "x")); err == nil {
		t.Error("a ledger under a file was read")
	}
}

// TestAbsentUnderDirectories: a ledger whose directories do not exist yet is
// absent, and one under a file is an error, however deep. This is the branch
// Windows takes for a ledger under a file, reached here directly, since a
// Unix read of one fails with ENOTDIR before it.
func TestAbsentUnderDirectories(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := absentUnderDirectories(filepath.Join(dir, "a", "b", "pr-check.jsonl")); err != nil {
		t.Errorf("a ledger under directories not made yet: %v", err)
	}
	for _, p := range []string{filepath.Join(file, "pr-check.jsonl"), filepath.Join(file, "a", "pr-check.jsonl")} {
		if err := absentUnderDirectories(p); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// TestRun_Usage: no command, an unknown one, a stray argument and an
// unknown flag are usage errors.
func TestRun_Usage(t *testing.T) {
	for _, args := range [][]string{nil, {"frobnicate"}, {"report", "extra"}, {"report", "-nope"}} {
		var stderr bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &stderr, nil); code != 2 || stderr.Len() == 0 {
			t.Errorf("%v: exit %d, %q", args, code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	if code := run([]string{"record-prcheck", "-dir", t.TempDir(), "-report", filepath.Join(t.TempDir(), "none.json")}, &bytes.Buffer{}, &stderr, nil); code != 1 {
		t.Errorf("no report: exit %d, %s", code, stderr.String())
	}
}

// TestResolveCommit_RefusesWhatIsNotASHA: a name that is not hex never
// reaches git, where it could read as an option.
func TestResolveCommit_RefusesWhatIsNotASHA(t *testing.T) {
	for _, c := range []string{"", "--all", "HEAD", "abc"} {
		if _, err := resolveCommit(c); err == nil {
			t.Errorf("%q resolved", c)
		}
	}
}

// TestGitHelpers_AgainstThisRepository: a prefix of HEAD resolves to HEAD
// in full through git itself, and gh is run as gh api with the path.
func TestGitHelpers_AgainstThisRepository(t *testing.T) {
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skipf("needs git: %v", err)
	}
	full := strings.TrimSpace(string(head))
	if got, err := resolveCommit(full[:12]); err != nil || got != full {
		t.Errorf("resolveCommit(%s) = %q, %v", full[:12], got, err)
	}
	if _, err := resolveCommit("0000000000000000000000000000000000000000"); err == nil {
		t.Error("a commit that does not exist resolved")
	}
	if runtime.GOOS == "windows" {
		return // the fake gh below is a shell script
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = api ] && [ \"$2\" = ok ]; then echo answer; exit 0; fi\necho refused >&2; exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if out, err := (ghCLI{}).API("ok"); err != nil || strings.TrimSpace(string(out)) != "answer" {
		t.Errorf("gh api ok: %q, %v", out, err)
	}
	if _, err := (ghCLI{}).API("no"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("gh api no: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := (ghCLI{}).API("ok"); err == nil {
		t.Error("gh ran with no gh on PATH")
	}
}

// Tests for binsize: real cross builds of a small main package, the ledger
// and the comparison a run prints.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hello is the small main package in testdata the real builds use, so a
// test does not build all of pleiades.
const hello = "./testdata/hello"

// sha is the commit the tests record under: a machine leg tests a tree with
// no git history, so none reads HEAD.
const sha = "0123456789abcdef0123456789abcdef01234567"

// TestRun_BuildsEachPlatformAndRecordsItsSize builds the testdata package
// for two platforms through the real go command, twice, and checks the
// ledger holds a size for each and the second run compares with nothing it
// built at the same commit.
func TestRun_BuildsEachPlatformAndRecordsItsSize(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ci", "binsize.jsonl")
	args := []string{"-pkg", hello, "-platforms", "linux/amd64,windows/arm64", "-ledger", ledger, "-commit", sha}
	var out, errs bytes.Buffer
	if code := run(args, &out, &errs); code != 0 {
		t.Fatalf("run = %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "linux/amd64") || !strings.Contains(out.String(), "(first recorded)") {
		t.Errorf("table:\n%s", out.String())
	}
	entries, err := readLedger(ledger)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ledger: %v, %v", entries, err)
	}
	for _, e := range entries {
		if e.Bytes <= 0 || e.Flags != Flags || e.Schema != Schema || len(e.Commit) != 40 {
			t.Errorf("entry %+v", e)
		}
	}
	info, err := os.Stat(ledger)
	if err != nil || info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("ledger mode: %v, %v", info, err)
	}
	out.Reset()
	if code := run(append(args, "-json"), &out, &errs); code != 0 {
		t.Fatalf("second run = %d: %s", code, errs.String())
	}
	var again []Entry
	if err := json.Unmarshal(out.Bytes(), &again); err != nil || len(again) != 2 {
		t.Fatalf("-json printed %q: %v", out.String(), err)
	}
}

// TestRun_APlatformThatDoesNotBuildFails: a pair go does not know fails the
// run and names it, and the platforms that built are still recorded.
func TestRun_APlatformThatDoesNotBuildFails(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "binsize.jsonl")
	var out, errs bytes.Buffer
	code := run([]string{"-pkg", hello, "-platforms", "linux/amd64,plan10/amd64", "-ledger", ledger, "-commit", sha}, &out, &errs)
	if code != 1 || !strings.Contains(errs.String(), "plan10/amd64 does not build") {
		t.Fatalf("run = %d: %s", code, errs.String())
	}
	if entries, err := readLedger(ledger); err != nil || len(entries) != 1 {
		t.Errorf("ledger: %v, %v", entries, err)
	}
}

// TestRun_Usage: a stray argument, an unknown flag and a platform that is
// not a plain GOOS/GOARCH pair are usage errors, built nothing.
func TestRun_Usage(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"-nope"}, {"-platforms", "linux"}, {"-platforms", "linux/amd64;rm"}, {"-platforms", "Linux/amd64"}} {
		var errs bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &errs); code != 2 {
			t.Errorf("run(%q) = %d: %s", args, code, errs.String())
		}
	}
}

// TestRun_ALedgerItCannotReadFails: a ledger of another schema, or under a
// file, fails before anything is built.
func TestRun_ALedgerItCannotReadFails(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "newer.jsonl")
	if err := os.WriteFile(newer, []byte(`{"schema":2}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ledger := range []string{newer, filepath.Join(file, "binsize.jsonl")} {
		var errs bytes.Buffer
		if code := run([]string{"-pkg", hello, "-platforms", "linux/amd64", "-ledger", ledger, "-commit", sha}, &bytes.Buffer{}, &errs); code != 1 {
			t.Errorf("%s: run = %d: %s", ledger, code, errs.String())
		}
	}
	garbage := filepath.Join(dir, "garbage.jsonl")
	if err := os.WriteFile(garbage, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLedger(garbage); err == nil || !strings.Contains(err.Error(), ":1:") {
		t.Errorf("garbage: %v", err)
	}
}

// TestPrintTable_ComparesWithTheLastCommitBuiltTheSameWay: the change is
// measured from the newest entry at another commit with the same platform,
// toolchain and flags, never from one built another way.
func TestPrintTable_ComparesWithTheLastCommitBuiltTheSameWay(t *testing.T) {
	at := func(commit, goVersion string, size int64) Entry {
		return Entry{Schema: Schema, Commit: commit, Package: "./cmd/pleiades", Platform: "linux/amd64", GoVersion: goVersion, Flags: Flags, Bytes: size, RecordedAt: time.Now()}
	}
	have := []Entry{at("aaaa", "go1.26.10", 100<<20), at("bbbb", "go1.25.0", 50<<20), at("cccc", "go1.26.10", 110<<20)}
	var out bytes.Buffer
	printTable(&out, []Entry{at("dddd", "go1.26.10", 121<<20)}, append(have, at("dddd", "go1.26.10", 1)))
	if want := "+11.00 MiB (+10.0%) since cccc"; !strings.Contains(out.String(), want) {
		t.Errorf("table:\n%s\nwant %q", out.String(), want)
	}
	out.Reset()
	printTable(&out, []Entry{at("eeee", "go1.27.0", 1<<20)}, have)
	if !strings.Contains(out.String(), "(first recorded)") {
		t.Errorf("a new toolchain compared with an old one:\n%s", out.String())
	}
	out.Reset()
	printTable(&out, nil, have)
	if out.Len() != 0 {
		t.Errorf("no entries printed %q", out.String())
	}
}

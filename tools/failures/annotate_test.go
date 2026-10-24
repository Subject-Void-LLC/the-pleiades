// Tests of annotate: a pattern and note set on the entries one test names,
// in both ledgers, optionally at one commit, and nothing else touched.
package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestAnnotate_SetsMatchingEntriesOnly: PKG.Test with a tail of the
// package's path matches in both ledgers, -commit narrows by prefix, and
// every other entry is left as it was.
func TestAnnotate_SetsMatchingEntriesOnly(t *testing.T) {
	dir := t.TempDir()
	mk := func(source, pkg, test, commit string) Entry {
		return Entry{Schema: EntrySchema, Source: source, Commit: commit, Kind: KindTest, Leg: "fast", Package: pkg, Test: test, Verdict: VerdictReal}
	}
	const pkg = "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	local := []Entry{mk(SourcePRCheck, pkg, "TestConnection_X", testCommit), mk(SourcePRCheck, pkg, "TestOther", testCommit), mk(SourcePRCheck, pkg, "", testCommit)}
	remote := []Entry{mk(SourceActions, pkg, "TestConnection_X", "9fc2bd0ea76ad99bd34ae7fb3475c04609e5f1db")}
	if err := writeLedger(ledgerFile(dir, SourcePRCheck), local); err != nil {
		t.Fatal(err)
	}
	if err := writeLedger(ledgerFile(dir, SourceActions), remote); err != nil {
		t.Fatal(err)
	}
	if out := runTool(t, "annotate", "-dir", dir, "-test", "catalog/wait.TestConnection_X", "-pattern", "631", "-note", "seen from someone@192.0.2.10"); !strings.Contains(out, "annotated 2 entries") {
		t.Errorf("annotate said %q", out)
	}
	if out := runTool(t, "annotate", "-dir", dir, "-test", pkg, "-commit", "1fa9", "-pattern", "61"); !strings.Contains(out, "annotated 1 entries") {
		t.Errorf("the package-level annotation said %q", out)
	}
	got, _ := readLedger(ledgerFile(dir, SourcePRCheck))
	if got[0].Pattern != 631 || got[0].Note != "seen from <user@host>" || got[1].Pattern != 0 || got[2].Pattern != 61 {
		t.Errorf("pr-check ledger: %+v", got)
	}
	gotRemote, _ := readLedger(ledgerFile(dir, SourceActions))
	if gotRemote[0].Pattern != 631 {
		t.Errorf("actions ledger: %+v", gotRemote)
	}
	var stderr bytes.Buffer
	if code := run([]string{"annotate", "-dir", dir, "-test", "x.TestNone", "-pattern", "1"}, &bytes.Buffer{}, &stderr, nil); code != 1 || !strings.Contains(stderr.String(), "no entry names") {
		t.Errorf("no match: exit %d, %s", code, stderr.String())
	}
	for _, args := range [][]string{{"annotate", "-pattern", "1"}, {"annotate", "-test", "x.TestA", "-dir", dir}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}, nil); code == 0 {
			t.Errorf("%v was accepted", args)
		}
	}
}

// TestSplitTest: a test name splits at its test, wherever the package's
// path has dots, and a name with no test is a package.
func TestSplitTest(t *testing.T) {
	for in, want := range map[string][2]string{
		"github.com/x/y.TestA/sub": {"github.com/x/y", "TestA/sub"},
		"x/y.FuzzParse":            {"x/y", "FuzzParse"},
		"github.com/x/y":           {"github.com/x/y", ""},
	} {
		if p, tst := splitTest(in); p != want[0] || tst != want[1] {
			t.Errorf("splitTest(%q) = %q, %q", in, p, tst)
		}
	}
}

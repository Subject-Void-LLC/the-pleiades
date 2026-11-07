// Package main: tests of the release policy, through a real git repository.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBaseTagIsTheNewestReleaseAtOrBelowTheLine(t *testing.T) {
	tags := []string{"v0.2.0", "v0.3.0", "v0.3.1", "v0.4.0-rc.1", "v0.5.0", "archive/Old", "v1"}
	for line, want := range map[string]string{"0.4.0": "v0.3.1", "0.3.1": "v0.3.1", "0.3.0": "v0.3.0", "0.1.0": "", "0.5.0": "v0.5.0"} {
		v, _ := parseVersion(line)
		if got := baseTag(tags, v); got != want {
			t.Errorf("line %s: base %q, want %q", line, got, want)
		}
	}
}

func TestJudgeAcceptsOnlyOnAMinorLineAndRefusesStaleTokens(t *testing.T) {
	f := []Finding{{Rule: "property-removed", Path: "run-report.json#/properties/a"}}
	accepted := map[string]string{"property-removed run-report.json#/properties/a": "a.breaking.md"}
	if _, _, pass := judge(f, accepted, false); !pass {
		t.Error("a named break on a minor line failed")
	}
	if _, _, pass := judge(f, accepted, true); pass {
		t.Error("a named break on a patch line passed: a point release never breaks a contract")
	}
	if _, _, pass := judge(f, nil, false); pass {
		t.Error("an unnamed break passed")
	}
	stale := map[string]string{"type-narrowed run-report.json#/properties/b": "b.breaking.md"}
	if _, s, pass := judge(nil, stale, false); pass || len(s) != 1 {
		t.Errorf("a token naming no break: pass %v, stale %q; want a failure naming it", pass, s)
	}
}

func TestTokensAreReadFromBreakingFragmentsOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rename.breaking.md"), "Facts are renamed: `fact-removed module-catalog.json#/facts.gather/facts/ansible_os_family` and\n`property-removed run-report.json#/properties/a`.\n")
	write(t, filepath.Join(dir, "other.added.md"), "`method-removed module-catalog.json#/x.y`\n")
	got, err := acceptedTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"fact-removed module-catalog.json#/facts.gather/facts/ansible_os_family": "rename.breaking.md",
		"property-removed run-report.json#/properties/a":                         "rename.breaking.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokens %v, want %v", got, want)
	}
}

// write writes a test file.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repo makes a git repository holding one release, tagged v0.3.0, whose
// schemas are the given files, and moves the test into it.
func repo(t *testing.T, files map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git: not on PATH")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range files {
		write(t, filepath.Join(dir, schemaDir, name), body)
	}
	if err := os.MkdirAll(filepath.Join(dir, "changelog"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "release"},
		{"tag", "v0.3.0"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestTheCheckReadsTheReleaseThroughGit(t *testing.T) {
	report := `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"]}`
	repo(t, map[string]string{"run-report.json": report})

	// Unchanged: passes, on both lines.
	for _, line := range []string{"0.3.0", "0.4.0"} {
		v, err := run(gitShow{}, "", line, schemaDir, "changelog")
		if err != nil || !v.Pass || v.Base != "v0.3.0" {
			t.Fatalf("line %s, unchanged tree: %+v, %v", line, v, err)
		}
	}

	// A required property removed fails until a fragment names it, and on a
	// patch line even then.
	write(t, filepath.Join(schemaDir, "run-report.json"), `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`)
	if v, _ := run(gitShow{}, "", "0.4.0", schemaDir, "changelog"); v.Pass || len(v.Findings) != 1 {
		t.Fatalf("a removed property passed or was missed: %+v", v)
	}
	write(t, filepath.Join("changelog", "drop-b.breaking.md"), "The run report no longer has `b` (`property-removed run-report.json#/properties/b`).\n")
	if v, _ := run(gitShow{}, "", "0.4.0", schemaDir, "changelog"); !v.Pass {
		t.Fatalf("a named break on 0.4.0 failed: %+v", v)
	}
	if v, _ := run(gitShow{}, "", "0.3.0", schemaDir, "changelog"); v.Pass || !v.Patch {
		t.Fatalf("a break on the 0.3 patch line passed: %+v", v)
	}

	// A new generated file nobody classified is a problem, not a pass.
	write(t, filepath.Join(schemaDir, "new-thing.json"), `{}`)
	if v, _ := run(gitShow{}, "", "0.4.0", schemaDir, "changelog"); v.Pass || len(v.Problems) != 1 {
		t.Fatalf("an unclassified file passed: %+v", v)
	}
	_ = os.Remove(filepath.Join(schemaDir, "new-thing.json"))

	// A whole schema removed is a break.
	_ = os.Remove(filepath.Join(schemaDir, "run-report.json"))
	v, _ := run(gitShow{}, "", "0.4.0", schemaDir, "changelog")
	if v.Pass || len(v.Findings) != 1 || v.Findings[0].Token() != "file-removed run-report.json#" {
		t.Fatalf("a removed schema file: %+v", v)
	}
}

func TestWithNoTagTheCheckSaysHowToFetchOne(t *testing.T) {
	repo(t, map[string]string{"run-report.json": `{}`})
	if out, err := exec.Command("git", "tag", "-d", "v0.3.0").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := run(gitShow{}, "", "0.4.0", schemaDir, "changelog"); err == nil {
		t.Fatal("no tag and no error")
	}
}

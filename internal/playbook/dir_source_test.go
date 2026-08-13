package playbook_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/playbook"
)

// These tests moved here with the implementation, from
// internal/adapters/legacy, when the source was pulled out so the
// Controller could import it. Same contract, same trust boundary.

// writeTree writes files at project-relative paths under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("failed to make fixture directory for %q: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write fixture %q: %v", rel, err)
		}
	}
}

func TestDirSource_ResolvesPathsInSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site.yml": "---\n- hosts: all\n",
		// The exact shape a production Ascender job template stores.
		"tripplite_python/tripplite_config.yml": "---\n- hosts: all\n  tasks: []\n",
	})

	src, err := playbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource returned unexpected error: %v", err)
	}

	got, err := src.Get(context.Background(), "site.yml")
	if err != nil {
		t.Fatalf("Get(\"site.yml\") returned unexpected error: %v", err)
	}
	if string(got) != "---\n- hosts: all\n" {
		t.Errorf("Get(\"site.yml\") = %q, want the fixture's own bytes", got)
	}

	// The case the whole grammar exists for. A flat-id resolver cannot
	// name this file at all, which is what made the previous version of
	// this package unable to run any playbook a real project contains.
	got, err = src.Get(context.Background(), "tripplite_python/tripplite_config.yml")
	if err != nil {
		t.Fatalf("Get on a subdirectory path returned unexpected error: %v", err)
	}
	if string(got) != "---\n- hosts: all\n  tasks: []\n" {
		t.Errorf("Get on a subdirectory path = %q, want the fixture's own bytes", got)
	}
}

func TestNewDirSource_RejectsMissingDirectory(t *testing.T) {
	_, err := playbook.NewDirSource(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing directory, got nil")
	}
}

func TestNewDirSource_RejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	_, err := playbook.NewDirSource(file)
	if err == nil {
		t.Fatal("expected an error for a non-directory path, got nil")
	}
}

func TestDirSource_UnknownIDReturnsErrNotFound(t *testing.T) {
	src, err := playbook.NewDirSource(t.TempDir())
	if err != nil {
		t.Fatalf("NewDirSource returned unexpected error: %v", err)
	}

	_, err = src.Get(context.Background(), "does-not-exist.yml")
	if !errors.Is(err, playbook.ErrNotFound) {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

// TestDirSource_RejectsPathTraversal proves a hostile id is rejected
// before any path construction, the same trust boundary
// internal/runbook/dir_source.go's own validRunbookID closes.
func TestDirSource_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	// A real file outside dir, so a successful traversal would have
	// something real to read: proves rejection, not a coincidental
	// ErrNotFound from a merely-missing file.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.yml"), []byte("should never be read"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	src, err := playbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource returned unexpected error: %v", err)
	}

	for _, hostile := range []string{
		"../" + filepath.Base(outside) + "/secret.yml",
		"../../etc/passwd.yml",
		"/etc/shadow.yml",
		"a/../../b.yml",
		"a\x00b.yml",
		`back\\slash.yml`,
	} {
		if _, err := src.Get(context.Background(), hostile); err == nil {
			t.Errorf("Get(%q) succeeded, want rejection", hostile)
		} else if errors.Is(err, playbook.ErrNotFound) {
			t.Errorf("Get(%q) failed with ErrNotFound, want a validation rejection instead (id should never reach path construction)", hostile)
		}
	}
}

func TestDirSource_ListWalksTheTreeAndReturnsWhatGetResolves(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site.yml":                              "---\n",
		"upgrade.yaml":                          "---\n",
		"tripplite_python/tripplite_config.yml": "---\n",
		"network/edge/versa_scan.yml":           "---\n",
		// A dotted filename is an ordinary path, unlike under the flat-id
		// grammar that refused it.
		"has.dots.in.name.yml": "---\n",
		// Not a playbook extension at all.
		"README.md": "x",
		// Conventional role layout: never a playbook, and listing every
		// vars file is the difference between a usable dropdown and four
		// hundred rows of noise.
		"roles/common/tasks/main.yml": "---\n",
		"group_vars/all.yml":          "---\n",
		"host_vars/edge-01.yml":       "---\n",
		// A synced checkout's object store, which must never be walked.
		".git/config.yml": "---\n",
	})

	src, err := playbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource returned unexpected error: %v", err)
	}

	refs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List returned unexpected error: %v", err)
	}
	want := []string{
		"has.dots.in.name.yml",
		"network/edge/versa_scan.yml",
		"site.yml",
		"tripplite_python/tripplite_config.yml",
		"upgrade.yaml",
	}
	if !slices.Equal(refs, want) {
		t.Fatalf("List = %v, want %v", refs, want)
	}

	// The contract that makes List worth trusting: every reference it
	// returns resolves through Get.
	for _, ref := range refs {
		if _, err := src.Get(context.Background(), ref); err != nil {
			t.Errorf("List offered %q but Get refuses it: %v", ref, err)
		}
	}
}

func TestValidateReference_IsTheOneGrammarAndItIsPaths(t *testing.T) {
	// The launch kind's shape validation defers to this exact function.
	// It accepts PATHS because that is what AWX stores in a job
	// template's Playbook field; an earlier pass unified the two
	// disagreeing grammars onto flat ids instead, which was tidy and left
	// the platform unable to name any real customer's playbook
	// (FAILURE_PATTERNS.md #113).
	for _, ok := range []string{
		"site.yml",
		"upgrade-ios.yaml",
		"tripplite_python/tripplite_config.yml",
		"a/b/c/d.yml",
		"v1.2.3/patch.yml",
	} {
		if !playbook.ValidReference(ok) {
			t.Errorf("ValidReference(%q) = false, want true: %v", ok, playbook.ValidateReference(ok))
		}
	}
	for _, bad := range []string{
		"", "site", "/abs.yml", "../x.yml", `a\b.yml`, "a/./b.yml", "a//b.yml",
		"notes.txt", "trailing/", string(make([]byte, 8)) + ".yml",
	} {
		if playbook.ValidReference(bad) {
			t.Errorf("ValidReference(%q) = true, want false", bad)
		}
	}
}

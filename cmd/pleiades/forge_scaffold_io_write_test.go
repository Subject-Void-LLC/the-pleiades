// Package main tests, for writeGeneratedFile and firstExistingFile: generated
// files are created new only, never through a symlink at their name or along
// their path (FAILURE_PATTERNS 792).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGeneratedFile is the one place every `forge new-*` subcommand writes
// a generated file. These tests hold it to FAILURE_PATTERNS 792: it creates
// new files only, never writes through a symlink at the file's name, and
// never lets a symlinked directory carry the write outside the target.

// TestWriteGeneratedFile_CreatesTheFileAndItsDirectories is the ordinary
// case the hardening must not break.
func TestWriteGeneratedFile_CreatesTheFileAndItsDirectories(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "out")

	full, err := writeGeneratedFile(dir, "pkg/collections/svc/apt/install.go", []byte("package apt\n"))
	if err != nil {
		t.Fatalf("writeGeneratedFile: %v", err)
	}
	if want := filepath.Join(dir, "pkg", "collections", "svc", "apt", "install.go"); full != want {
		t.Errorf("returned %s, want %s", full, want)
	}
	got, err := os.ReadFile(full)
	if err != nil || string(got) != "package apt\n" {
		t.Errorf("file holds %q (%v)", got, err)
	}
}

// TestWriteGeneratedFile_RefusesAnExistingFile keeps forge's promise not to
// overwrite, now made in the same step as the create.
func TestWriteGeneratedFile_RefusesAnExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("hand-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := writeGeneratedFile(dir, "a.go", []byte("generated\n"))
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite existing file") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "hand-written\n" {
		t.Errorf("the existing file was changed to %q", got)
	}
}

// TestWriteGeneratedFile_DoesNotWriteThroughASymlinkAtTheName is the
// reported weakness: os.WriteFile followed a link planted at the name and
// wrote wherever it pointed, live or dangling.
func TestWriteGeneratedFile_DoesNotWriteThroughASymlinkAtTheName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		existing bool // whether the link's target exists
	}{
		{"link to an existing file", true},
		{"dangling link", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "victim")
			if tc.existing {
				if err := os.WriteFile(outside, []byte("untouched\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, filepath.Join(dir, "a.go")); err != nil {
				t.Fatal(err)
			}

			if _, err := writeGeneratedFile(dir, "a.go", []byte("generated\n")); err == nil {
				t.Fatal("writing over a symlink must be refused")
			}
			got, err := os.ReadFile(outside)
			switch {
			case tc.existing && string(got) != "untouched\n":
				t.Errorf("the link's target was written: %q", got)
			case !tc.existing && err == nil:
				t.Errorf("the dangling link's target was created, holding %q", got)
			}
		})
	}
}

// TestWriteGeneratedFile_DoesNotFollowASymlinkedDirectory covers the path
// on the way to the file: a directory link inside the target must not carry
// the write somewhere else.
func TestWriteGeneratedFile_DoesNotFollowASymlinkedDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "internal")); err != nil {
		t.Fatal(err)
	}

	if _, err := writeGeneratedFile(dir, "internal/x/a.go", []byte("generated\n")); err == nil {
		t.Fatal("writing through a symlinked directory must be refused")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was written outside the target: %v", entries)
	}
}

// TestFirstExistingFile_CountsASymlinkAsExisting keeps --skip-existing in
// step with writeGeneratedFile: a link at a name, even a dangling one, is
// something the write would refuse, so the whole entry is skipped.
func TestFirstExistingFile_CountsASymlinkAsExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	link := filepath.Join(dir, "b.go")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	got, err := firstExistingFile(dir, []string{"a.go", "b.go"})
	if err != nil {
		t.Fatalf("firstExistingFile: %v", err)
	}
	if got != link {
		t.Errorf("firstExistingFile = %q, want the dangling link %q", got, link)
	}
}

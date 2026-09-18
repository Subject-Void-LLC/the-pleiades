// Tests for the directory setup writes into: file modes, refusals, and writes
// that cannot escape it.
package setup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// openTestDir opens a fresh directory as a setup.Dir.
func openTestDir(t *testing.T) (*setup.Dir, string) {
	t.Helper()
	path := t.TempDir()
	d, err := setup.OpenDir(path, "")
	if err != nil {
		t.Fatalf("OpenDir() error = %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, path
}

// TestDir_CreateNewWritesOwnerOnlyAndNeverReplaces covers the first write:
// mode 0600, no temporary file left behind, and a second create refused
// with the original content intact.
func TestDir_CreateNewWritesOwnerOnlyAndNeverReplaces(t *testing.T) {
	d, path := openTestDir(t)

	if err := d.CreateNew(".env", []byte("FIRST=1\n")); err != nil {
		t.Fatalf("CreateNew() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(path, ".env"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the file was written at mode %v, want 0600", info.Mode().Perm())
	}

	err = d.CreateNew(".env", []byte("SECOND=2\n"))
	if !errors.Is(err, setup.ErrChangedUnderneath) {
		t.Fatalf("second CreateNew() error = %v, want ErrChangedUnderneath", err)
	}
	if data, _ := os.ReadFile(filepath.Join(path, ".env")); string(data) != "FIRST=1\n" {
		t.Fatalf("the file now holds %q; a refused create must leave it alone", data)
	}

	entries, _ := os.ReadDir(path)
	if len(entries) != 1 {
		t.Fatalf("the directory holds %d entries after two creates, want only the file: a temporary file was left behind", len(entries))
	}
}

// TestDir_ReadExistingRefusesWhatIsNotTheFileItWasAskedFor covers the three
// things ReadExisting refuses rather than read.
func TestDir_ReadExistingRefusesWhatIsNotTheFileItWasAskedFor(t *testing.T) {
	d, path := openTestDir(t)

	if _, exists, err := d.ReadExisting(".env"); err != nil || exists {
		t.Fatalf("ReadExisting(missing) = %v, %v; want absent and no error", exists, err)
	}

	target := filepath.Join(path, "real.env")
	if err := os.WriteFile(target, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(path, ".env")); err != nil {
		t.Fatalf("planting a symlink: %v", err)
	}
	if _, _, err := d.ReadExisting(".env"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("ReadExisting(symlink) error = %v, want a refusal naming the link", err)
	}

	if err := os.Mkdir(filepath.Join(path, "adir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, err := d.ReadExisting("adir"); err == nil {
		t.Fatal("ReadExisting accepted a directory")
	}

	big := make([]byte, (1<<20)+1)
	if err := os.WriteFile(filepath.Join(path, "big.env"), big, 0o600); err != nil {
		t.Fatalf("writing a large file: %v", err)
	}
	if _, _, err := d.ReadExisting("big.env"); err == nil {
		t.Fatal("ReadExisting accepted a file larger than any setup writes")
	}
}

// TestDir_ReplaceRefusesAFileThatChangedSinceItWasRead is the compare and
// swap: a replacement goes through only over the exact bytes setup decided
// from.
func TestDir_ReplaceRefusesAFileThatChangedSinceItWasRead(t *testing.T) {
	d, path := openTestDir(t)
	file := filepath.Join(path, ".env")
	if err := os.WriteFile(file, []byte("A=read\n"), 0o644); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if err := d.Replace(".env", []byte("A=something else\n"), []byte("A=new\n")); !errors.Is(err, setup.ErrChangedUnderneath) {
		t.Fatalf("Replace(stale expectation) error = %v, want ErrChangedUnderneath", err)
	}
	if data, _ := os.ReadFile(file); string(data) != "A=read\n" {
		t.Fatalf("a refused replace changed the file to %q", data)
	}

	if err := d.Replace(".env", []byte("A=read\n"), []byte("A=new\n")); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	data, _ := os.ReadFile(file)
	info, _ := os.Stat(file)
	if string(data) != "A=new\n" || info.Mode().Perm() != 0o600 {
		t.Fatalf("after Replace the file holds %q at %v; want the new content at 0600", data, info.Mode().Perm())
	}
}

// TestDir_NoNameEscapesTheDirectory proves os.Root confines every write,
// whatever name reaches it.
func TestDir_NoNameEscapesTheDirectory(t *testing.T) {
	outside := t.TempDir()
	d, path := openTestDir(t)
	if err := os.Symlink(outside, filepath.Join(path, "link")); err != nil {
		t.Fatalf("planting a symlink to another directory: %v", err)
	}

	for _, name := range []string{"../escaped", "link/escaped", filepath.Join(outside, "escaped")} {
		if err := d.CreateNew(name, []byte("X=1\n")); err == nil {
			t.Errorf("CreateNew(%q) succeeded", name)
		}
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("a write escaped into %s: %v", outside, entries)
	}
}

// TestDir_CheckWritableFailsBeforeAnythingIsGenerated proves a directory
// setup cannot write into is found out first.
func TestDir_CheckWritableFailsBeforeAnythingIsGenerated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through directory permissions")
	}
	d, path := openTestDir(t)
	if err := d.CheckWritable(); err != nil {
		t.Fatalf("CheckWritable() on a writable directory: %v", err)
	}
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
	if err := d.CheckWritable(); err == nil {
		t.Fatal("CheckWritable() passed on a read-only directory")
	}
}

// TestOpenDir_RefusesAControlCharacter proves a path that would rewrite a
// terminal line when printed back is refused.
func TestOpenDir_RefusesAControlCharacter(t *testing.T) {
	if _, err := setup.OpenDir(t.TempDir()+"\x1b[2K", ""); err == nil {
		t.Fatal("OpenDir accepted a path with an escape sequence in it")
	}
}

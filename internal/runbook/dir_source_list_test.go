package runbook_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

func TestDirSource_ListReturnsSortedIDs(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "zulu", "noop")
	writeRunbook(t, dir, "alpha", "noop")
	writeRunbook(t, dir, "mike", "noop")

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	got, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alpha", "mike", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List = %v, want %v", got, want)
		}
	}
}

func TestDirSource_ListOnAnEmptyDirectoryIsEmpty(t *testing.T) {
	src, err := runbook.NewDirSource(t.TempDir())
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	got, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty", got)
	}
}

// A catalog must not offer something Get would refuse. Every id List
// returns is run through the same validRunbookID guard that protects path
// construction, so a file dropped in with a name this package will not
// resolve never appears as runnable.
func TestDirSource_ListSkipsWhatGetWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "good", "noop")

	// Not YAML at all.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	// A YAML file whose stem is not a valid runbook id.
	if err := os.WriteFile(filepath.Join(dir, "bad id!.yaml"), []byte("id: x\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	// A subdirectory: Get resolves "<id>.yaml" directly under dir, nothing
	// else, so a directory named like a runbook is not one.
	if err := os.Mkdir(filepath.Join(dir, "nested.yaml"), 0o750); err != nil {
		t.Fatalf("creating fixture dir: %v", err)
	}

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	got, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("List = %v, want just [good]", got)
	}

	// And what List offered really does resolve.
	if _, err := src.Get(t.Context(), got[0]); err != nil {
		t.Errorf("Get(%q) after List offered it: %v", got[0], err)
	}
}

func TestDirSource_ListAcceptsBothYAMLExtensions(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "long", "noop")
	if err := os.WriteFile(filepath.Join(dir, "short.yml"), []byte("id: short\ntasks: []\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	got, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %v, want both extensions", got)
	}
}

// A file that will not compile is still a catalog entry: List reads
// directory entries and never compiles, so one broken runbook does not
// make the whole catalog unreadable. Get is where it fails, individually.
func TestDirSource_ListDoesNotCompile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("this: [is not: a runbook\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	writeRunbook(t, dir, "fine", "noop")

	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatalf("NewDirSource: %v", err)
	}

	got, err := src.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %v, want both entries listed despite one being broken", got)
	}
	if _, err := src.Get(t.Context(), "broken"); err == nil {
		t.Error("Get(broken) = nil error, want the compile failure surfaced per-runbook")
	}
}

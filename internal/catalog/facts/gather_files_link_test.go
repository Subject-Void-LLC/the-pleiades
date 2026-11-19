// Package facts_test: a file reached through a symbolic link keeps no plain
// digest (Phase 41c, owner decision 2026-11-19). The world-readable walk reads
// the directories of the path as written; a link among them leads to
// directories the walk never reads, which may keep everyone else out.
package facts_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestGather_AFileThroughALinkedDirectoryKeepsNoPlainDigest(t *testing.T) {
	gatherNeedsPOSIX(t)
	root := worldDir(t)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "private"), 0o700) })
	// private (0700) holds open (0755) holds secret (0644): nobody but this
	// login reaches secret, since private keeps them out.
	open := filepath.Join(root, "private", "open")
	if err := os.MkdirAll(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(open, "secret"), []byte("pin1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// pub (0755) holds l, a link to private/open.
	pub := filepath.Join(root, "pub")
	if err := os.Mkdir(pub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(open, filepath.Join(pub, "l")); err != nil {
		t.Fatal(err)
	}
	through := filepath.Join(pub, "l", "secret")

	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"!all", "!min"}, "files": []any{through}}); err != nil {
		t.Fatal(err)
	}
	got := gatherItem(t, rc, gatherFileKey(through))
	if got["sha256"] != nil || got["world_readable"] != false {
		t.Fatalf("file.%s = %v: a file only this login reaches kept a plain digest", through, got)
	}
	if got["readable"] != true {
		t.Fatalf("file.%s = %v, want it read, its digest only keyed", through, got)
	}
	if _, ok := got[collection.RawDigestField]; !ok {
		t.Fatalf("file.%s = %v, want its digest handed over raw, for the engine to key", through, got)
	}
}

// TestGather_AHardLinkedFileKeepsNoPlainDigest: a hard link in a public
// directory to a file inside a private one is the same file, with the same
// mode, reached by a path the walk finds public; a file with more than one
// link may be reached by a path nobody else can, so it is private.
func TestGather_AHardLinkedFileKeepsNoPlainDigest(t *testing.T) {
	gatherNeedsPOSIX(t)
	root := worldDir(t)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "private"), 0o700) })
	if err := os.Mkdir(filepath.Join(root, "pub"), 0o755); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "secret"), []byte("pin1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "pub", "secret")
	if err := os.Link(filepath.Join(private, "secret"), link); err != nil {
		t.Skipf("no hard link here: %v", err)
	}
	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"!all", "!min"}, "files": []any{link}}); err != nil {
		t.Fatal(err)
	}
	got := gatherItem(t, rc, gatherFileKey(link))
	if got["sha256"] != nil || got["world_readable"] != false || got["readable"] != true {
		t.Fatalf("file.%s = %v: a file also linked inside a private directory kept a plain digest", link, got)
	}
}

// The image methods' undos against this machine's real Docker: each is
// guarded by what it found, never deletes what predates its task, and
// removes nothing a container uses unless forced.
package image_test

import (
	"strings"
	"testing"
)

// TestUndos_GuardedAndHonest walks the undos a tag, an import and a remove
// record, replaying them as a rollback would.
func TestUndos_GuardedAndHonest(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	first := h.imported(t, repo+":a", "first")
	second := h.imported(t, repo+":b", "second")
	third := h.imported(t, repo+":c", "third")
	target := repo + ":release"
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": first, "tag": target}); err != nil {
		t.Fatal(err)
	}

	// Moving the tag records pointing it back, guarded by what it names now.
	rc := fresh()
	if _, err := call(t, h, rc, "container.image.tag", false, map[string]any{"source": second, "tag": target}); err != nil {
		t.Fatal(err)
	}
	fqcn, undo := inverseOf(rc)
	if fqcn != "container.image.tag" || undo["source"] != first || undo["id"] != second || undo["engine"] != "docker" {
		t.Fatalf("a moved tag's undo = %s %v", fqcn, undo)
	}
	// Someone moves it again: the undo is refused rather than moving it.
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": third, "tag": target}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, h, fresh(), fqcn, false, undo); err == nil || !strings.Contains(err.Error(), "does nothing to it") || h.idOf(t, target) != third {
		t.Errorf("replaying the undo after another move = %v; tag names %s", err, h.idOf(t, target))
	}
	// Moved back in time, the undo restores it.
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": second, "tag": target}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, h, fresh(), fqcn, false, undo); err != nil || h.idOf(t, target) != first {
		t.Errorf("replaying the undo = %v; tag names %s, want %s", err, h.idOf(t, target), first)
	}

	// An import over a tag in use records a partial, guarded undo.
	rc = fresh()
	if _, err := call(t, h, rc, "container.image.import", false, map[string]any{"path": rootfs(t, t.TempDir(), "fourth"), "image": repo + ":a"}); err != nil {
		t.Fatal(err)
	}
	fourth, _ := rc.stats["image_id"].(string)
	inv, _ := rc.stats["inverse"].(map[string]any)
	if fqcn, undo := inverseOf(rc); fqcn != "container.image.tag" || undo["source"] != first || undo["id"] != fourth || inv["partial"] != true {
		t.Errorf("an import over a tag's undo = %s %v (partial %v)", fqcn, undo, inv["partial"])
	}

	// first has no name left now that :a and :release moved on: tagging it
	// records no undo, since removing its only name would delete it.
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": second, "tag": target}); err != nil {
		t.Fatal(err)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.image.tag", false, map[string]any{"source": first, "tag": repo + ":only"}); err != nil {
		t.Fatal(err)
	}
	if fqcn, undo := inverseOf(rc); fqcn != "" {
		t.Errorf("tagging an image with no other name recorded %s %v, which would delete it", fqcn, undo)
	}

	// second has two names (:b and :release) and a container: untagging one
	// needs no force and leaves the image; removing it by ID without force
	// is refused while the container uses it.
	h.container(t, repo+":b")
	if _, err := call(t, h, fresh(), "container.image.remove", false, map[string]any{"image": target}); err != nil || h.idOf(t, target) != "" || h.idOf(t, repo+":b") != second {
		t.Errorf("untagging one of an in-use image's names = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.image.remove", false, map[string]any{"image": second}); err == nil || !strings.Contains(err.Error(), "set force") {
		t.Errorf("removing an in-use image by ID = %v", err)
	}

	// third has two names and no container: by ID, without force, every
	// name goes and the image with them.
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": third, "tag": repo + ":third2"}); err != nil {
		t.Fatal(err)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.image.remove", false, map[string]any{"image": third}); err != nil || h.idOf(t, repo+":c") != "" || h.idOf(t, repo+":third2") != "" || h.idOf(t, third) != "" {
		t.Errorf("removing a two-name image by ID = %v; removed %v", err, rc.stats["removed"])
	}
}

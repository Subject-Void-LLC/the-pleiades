// Tag, remove, import and info against this machine's real Docker engine.
package image_test

import (
	"strings"
	"testing"
)

// TestTagAndRemove_ALifeOfNames: import makes an image and records its
// removal; tag adds a name (undo: remove it, by ID), is then no change, and
// moving it records moving it back; remove refuses an image a container
// uses and a tag that names another image than the one recorded, removes a
// tag, and is then no change. Every check changes nothing.
func TestTagAndRemove_ALifeOfNames(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	first := h.imported(t, repo+":a", "first")
	second := h.imported(t, repo+":b", "second")
	if first == second {
		t.Fatalf("two archives with different content imported as one image %s", first)
	}
	target := repo + ":release"

	if res, err := call(t, h, fresh(), "container.image.tag", true, map[string]any{"source": repo + ":a", "tag": target}); err != nil || !res.Changed || h.idOf(t, target) != "" {
		t.Fatalf("a check of tag = %+v, %v; target %q", res, err, h.idOf(t, target))
	}
	rc := fresh()
	if res, err := call(t, h, rc, "container.image.tag", false, map[string]any{"source": repo + ":a", "tag": target}); err != nil || !res.Changed || h.idOf(t, target) != first {
		t.Fatalf("tag = %+v, %v; target names %q", res, err, h.idOf(t, target))
	}
	if fqcn, p := inverseOf(rc); fqcn != "container.image.remove" || p["image"] != target || p["id"] != first {
		t.Errorf("a new tag's undo = %s %v", fqcn, p)
	}
	if res, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": first, "tag": target}); err != nil || res.Changed {
		t.Errorf("tagging again = %+v, %v; want no change", res, err)
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.image.tag", false, map[string]any{"source": repo + ":b", "tag": target}); err != nil || !res.Changed || h.idOf(t, target) != second {
		t.Fatalf("moving the tag = %+v, %v", res, err)
	}
	if fqcn, p := inverseOf(rc); fqcn != "container.image.tag" || p["source"] != first || p["tag"] != target {
		t.Errorf("a moved tag's undo = %s %v", fqcn, p)
	}
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": repo + ":missing", "tag": target}); err == nil || !strings.Contains(err.Error(), "no image") {
		t.Errorf("tagging a missing image = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": first, "tag": "--privileged"}); err == nil {
		t.Error("a target beginning with - was accepted")
	}

	// remove: the recorded ID guards the tag, which now names second.
	if _, err := call(t, h, fresh(), "container.image.remove", false, map[string]any{"image": target, "id": first}); err == nil || !strings.Contains(err.Error(), "does nothing to it") {
		t.Errorf("removing a tag recorded against another image = %v", err)
	}
	h.container(t, repo+":b")
	if _, err := call(t, h, fresh(), "container.image.remove", false, map[string]any{"image": second}); err == nil || !strings.Contains(err.Error(), "set force") {
		t.Errorf("removing an image a container uses = %v", err)
	}
	if res, err := call(t, h, fresh(), "container.image.remove", true, map[string]any{"image": repo + ":a"}); err != nil || !res.Changed || h.idOf(t, repo+":a") == "" {
		t.Errorf("a check of remove = %+v, %v", res, err)
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.image.remove", false, map[string]any{"image": repo + ":a", "id": first}); err != nil || !res.Changed || h.idOf(t, repo+":a") != "" {
		t.Fatalf("remove = %+v, %v", res, err)
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Error("remove recorded an undo")
	}
	if res, err := call(t, h, fresh(), "container.image.remove", false, map[string]any{"image": repo + ":a"}); err != nil || res.Changed {
		t.Errorf("removing an absent image = %+v, %v; want no change", res, err)
	}
}

// TestImport_RecordsItsUndo: an import onto a new tag records removing the
// image by ID, and one onto a tag in use records pointing it back; a
// digest the archive does not have is refused before the engine reads it.
func TestImport_RecordsItsUndo(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	dir := t.TempDir()
	archive := rootfs(t, dir, "one")

	rc := fresh()
	if _, err := call(t, h, rc, "container.image.import", false, map[string]any{"path": archive, "image": repo + ":x"}); err != nil {
		t.Fatal(err)
	}
	first, _ := rc.stats["image_id"].(string)
	digest, _ := rc.stats["digest"].(string)
	if fqcn, p := inverseOf(rc); fqcn != "container.image.remove" || p["image"] != first || first == "" {
		t.Errorf("a new tag's import undo = %s %v", fqcn, p)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest = %q", digest)
	}

	if res, err := call(t, h, fresh(), "container.image.import", true, map[string]any{"path": rootfs(t, dir, "two"), "image": repo + ":x"}); err != nil || !res.Changed || h.idOf(t, repo+":x") != first {
		t.Errorf("a check of import = %+v, %v", res, err)
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.image.import", false, map[string]any{"path": rootfs(t, dir, "two"), "image": repo + ":x"}); err != nil {
		t.Fatal(err)
	}
	if fqcn, p := inverseOf(rc); fqcn != "container.image.tag" || p["source"] != first || p["tag"] != repo+":x" {
		t.Errorf("an import over a tag's undo = %s %v", fqcn, p)
	}

	wrong := "sha256:" + strings.Repeat("0", 64)
	if _, err := call(t, h, fresh(), "container.image.import", false, map[string]any{"path": archive, "image": repo + ":y", "digest": wrong}); err == nil || !strings.Contains(err.Error(), "not the") || h.idOf(t, repo+":y") != "" {
		t.Errorf("an import of an archive with another digest = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.image.import", false, map[string]any{"path": "relative.tar", "image": repo + ":y"}); err == nil {
		t.Error("a relative path was accepted")
	}
	if _, err := call(t, h, fresh(), "container.image.import", false, map[string]any{"path": dir + "/../x.tar", "image": repo + ":y"}); err == nil {
		t.Error("a path that is not clean was accepted")
	}
}

// TestInfo_ReportsNamedAndMissing: info reports a named image's ID and
// tags, lists a missing one rather than failing, and never the image's
// environment.
func TestInfo_ReportsNamedAndMissing(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	id := h.imported(t, repo+":1", "info")
	rc := fresh()
	if res, err := call(t, h, rc, "container.image.info", false, map[string]any{"images": []any{repo + ":1", repo + ":absent"}}); err != nil || res.Changed {
		t.Fatalf("info = %+v, %v", res, err)
	}
	images, _ := rc.stats["images"].([]map[string]any)
	if len(images) != 1 || images[0]["id"] != id {
		t.Fatalf("images = %v", rc.stats["images"])
	}
	if tags, _ := images[0]["tags"].([]string); len(tags) != 1 || tags[0] != repo+":1" {
		t.Errorf("tags = %v", images[0]["tags"])
	}
	if _, ok := images[0]["env"]; ok {
		t.Error("info reported the image's environment")
	}
	// A named image is described whole: what a container from it runs, as
	// lists even where the image declares nothing, and its platform.
	for _, k := range []string{"exposed_ports", "volumes", "env_names"} {
		if _, ok := images[0][k].([]string); !ok {
			t.Errorf("%s = %#v, want a list", k, images[0][k])
		}
	}
	// The entrypoint and command are a program and an argument count, never
	// the arguments: one baked into an image can be a secret.
	for _, k := range []string{"entrypoint", "command"} {
		m, ok := images[0][k].(map[string]any)
		if _, isInt := m["arg_count"].(int); !ok || !isInt {
			t.Errorf("%s = %#v, want program and arg_count", k, images[0][k])
		}
		if _, isString := m["program"].(string); !isString {
			t.Errorf("%s = %#v, want a program", k, images[0][k])
		}
	}
	if images[0]["architecture"] == "" || images[0]["os"] == "" {
		t.Errorf("platform = %v/%v", images[0]["os"], images[0]["architecture"])
	}
	if missing, _ := rc.stats["missing"].([]string); len(missing) != 1 || missing[0] != repo+":absent" {
		t.Errorf("missing = %v", rc.stats["missing"])
	}
	rc = fresh()
	if _, err := call(t, h, rc, "container.image.info", true, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	all, _ := rc.stats["images"].([]map[string]any)
	found := false
	for _, img := range all {
		found = found || img["id"] == id
	}
	if !found {
		t.Errorf("listing every image left out %s", id)
	}
}

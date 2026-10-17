// Save and load against this machine's real Docker engine, the archive on
// this machine's own files (the local carrier's host).
package image_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// fileDigest is the SHA-256 of the file at p.
func fileDigest(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestSaveAndLoad_OneArchiveByItsDigest: a check of save predicts the
// digest and writes nothing; save writes the archive mode 0600 and returns
// its digest; saving again is no change; with the image gone, a load naming
// another digest is refused and loads nothing, a check loads nothing, and a
// load naming the archive's digest brings the image back.
func TestSaveAndLoad_OneArchiveByItsDigest(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	id := h.imported(t, repo+":1", "saved")
	archive := filepath.Join(t.TempDir(), "saved.tar")
	params := map[string]any{"images": []any{repo + ":1"}, "path": archive}

	rc := fresh()
	if res, err := call(t, h, rc, "container.image.save", true, params); err != nil || !res.Changed {
		t.Fatalf("a check of save = %+v, %v", res, err)
	}
	predicted, _ := rc.stats["digest"].(string)
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("a check of save wrote %s (%v)", archive, err)
	}

	rc = fresh()
	if res, err := call(t, h, rc, "container.image.save", false, params); err != nil || !res.Changed {
		t.Fatalf("save = %+v, %v", res, err)
	}
	digest, _ := rc.stats["digest"].(string)
	if digest != fileDigest(t, archive) || digest != predicted {
		t.Errorf("save returned %s, the check predicted %s, and the file is %s", digest, predicted, fileDigest(t, archive))
	}
	if info, err := os.Stat(archive); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the archive's mode = %v, %v; want 0600", info.Mode(), err)
	}
	if ids, _ := rc.stats["images"].([]string); len(ids) != 1 || ids[0] != id {
		t.Errorf("images = %v", rc.stats["images"])
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Error("save recorded an undo")
	}
	if res, err := call(t, h, fresh(), "container.image.save", false, params); err != nil || res.Changed {
		t.Errorf("saving again = %+v, %v; want no change", res, err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(archive), ".pleiades-archive.*"))
	if len(matches) != 0 {
		t.Errorf("save left temporaries: %v", matches)
	}

	if _, err := h.api.ImageRemove(t.Context(), id, client.ImageRemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	wrong := "sha256:" + strings.Repeat("1", 64)
	if _, err := call(t, h, fresh(), "container.image.load", false, map[string]any{"path": archive, "digest": wrong}); err == nil || !strings.Contains(err.Error(), "it is not loaded") || h.idOf(t, repo+":1") != "" {
		t.Errorf("a load of an archive with another digest = %v; image %q", err, h.idOf(t, repo+":1"))
	}
	if res, err := call(t, h, fresh(), "container.image.load", true, map[string]any{"path": archive, "digest": digest}); err != nil || !res.Changed || h.idOf(t, repo+":1") != "" {
		t.Errorf("a check of load = %+v, %v", res, err)
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.image.load", false, map[string]any{"path": archive, "digest": digest}); err != nil || !res.Changed {
		t.Fatalf("load = %+v, %v", res, err)
	}
	if got := h.idOf(t, repo+":1"); got != id {
		t.Errorf("after load, %s:1 is %q, want %s", repo, got, id)
	}
	if loaded, _ := rc.stats["images"].([]string); len(loaded) == 0 {
		t.Errorf("load named no image: %v", rc.stats)
	}
	if _, err := call(t, h, fresh(), "container.image.load", false, map[string]any{"path": archive + ".absent"}); err == nil || !strings.Contains(err.Error(), "no archive") {
		t.Errorf("a load of a missing archive = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.image.save", false, map[string]any{"images": []any{repo + ":absent"}, "path": archive}); err == nil || !strings.Contains(err.Error(), "no image") {
		t.Errorf("saving a missing image = %v", err)
	}
}

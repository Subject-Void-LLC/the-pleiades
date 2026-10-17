// Pull, resolve, search and push against this machine's real Docker engine
// and the public registry it pulls testsupport.NginxImage from. Nothing is
// pushed to a public registry: push is tested against a registry address
// nothing listens on, which is the engine's error path.
package image_test

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// ociIndex is the part of an OCI index these tests read.
type ociIndex struct {
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform *struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
}

// platformManifest is the digest of NginxImage's manifest for the engine's
// own platform, read from the image's saved index: a reference to the same
// content that no image on the engine is named by, so pulling it pulls
// nothing anyone shares a name with and removing it touches no one's image.
func platformManifest(t *testing.T, h *harness) (string, string) {
	t.Helper()
	ctx := context.Background()
	img, err := h.api.ImageInspect(ctx, testsupport.NginxImage)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := h.api.ImageSave(ctx, []string{testsupport.NginxImage})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	blobs := map[string][]byte{}
	tr := tar.NewReader(stream)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Size > 1<<20 {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		blobs[hdr.Name] = data
	}
	var top, index ociIndex
	if err := json.Unmarshal(blobs["index.json"], &top); err != nil || len(top.Manifests) == 0 {
		t.Skipf("the engine saves no OCI index for %s (%v), so there is no manifest digest to pull", testsupport.NginxImage, err)
	}
	if err := json.Unmarshal(blobs["blobs/sha256/"+strings.TrimPrefix(top.Manifests[0].Digest, "sha256:")], &index); err != nil {
		t.Fatal(err)
	}
	for _, m := range index.Manifests {
		if m.Platform != nil && m.Platform.OS == img.Os && m.Platform.Architecture == img.Architecture {
			return "nginx@" + m.Digest, m.Digest
		}
	}
	t.Skipf("%s's index names no %s/%s manifest", testsupport.NginxImage, img.Os, img.Architecture)
	return "", ""
}

// TestPull_PresentAndAbsent: NginxImage, present and tagged, is no change
// to pull or check; its platform manifest, absent, is pulled by a run and
// not by a check, its removal recorded as the undo, which then removes it
// and leaves NginxImage as it was.
func TestPull_PresentAndAbsent(t *testing.T) {
	h := newHarness(t)
	before := h.idOf(t, testsupport.NginxImage)
	// An image restored from a cache has its tag and no repository digest,
	// which a pull by digest does not take for present.
	if len(h.repoDigests(t, testsupport.NginxImage)) == 0 {
		if _, err := call(t, h, fresh(), "container.image.pull", false, map[string]any{"image": testsupport.NginxImage}); err != nil {
			t.Fatal(err)
		}
		before = h.idOf(t, testsupport.NginxImage)
	}
	rc := fresh()
	if res, err := call(t, h, rc, "container.image.pull", false, map[string]any{"image": testsupport.NginxImage}); err != nil || res.Changed {
		t.Fatalf("pulling a present image = %+v, %v; want no change", res, err)
	}
	if rc.stats["image_id"] != before || !strings.HasPrefix(rc.stats["image"].(string), "nginx@sha256:") {
		t.Errorf("stats = %v", rc.stats)
	}

	ref, digest := platformManifest(t, h)
	t.Cleanup(func() {
		_, _ = h.api.ImageRemove(context.Background(), ref, client.ImageRemoveOptions{})
	})
	if h.idOf(t, ref) != "" {
		t.Skipf("%s is already on the engine", ref)
	}
	if res, err := call(t, h, fresh(), "container.image.pull", true, map[string]any{"image": ref}); err != nil || !res.Changed || h.idOf(t, ref) != "" {
		t.Fatalf("a check of an absent pull = %+v, %v", res, err)
	}
	rc = fresh()
	if res, err := call(t, h, rc, "container.image.pull", false, map[string]any{"image": ref}); err != nil || !res.Changed || rc.stats["pulled"] != true {
		t.Fatalf("pull = %+v, %v; stats %v", res, err, rc.stats)
	}
	id := h.idOf(t, ref)
	if rc.stats["digest"] != digest || id == "" {
		t.Errorf("digest = %v, ID %q", rc.stats["digest"], id)
	}
	fqcn, p := inverseOf(rc)
	if fqcn != "container.image.remove" || p["image"] != id {
		t.Fatalf("pull's undo = %s %v", fqcn, p)
	}
	if res, err := call(t, h, fresh(), fqcn, false, p); err != nil || !res.Changed || h.idOf(t, ref) != "" {
		t.Fatalf("the undo = %+v, %v; %s is %q", res, err, ref, h.idOf(t, ref))
	}
	if h.idOf(t, testsupport.NginxImage) != before {
		t.Errorf("removing the pulled manifest changed %s", testsupport.NginxImage)
	}
}

// TestResolve_ATagToItsIndex: resolve gives NginxImage's index digest, the
// one the engine holds it by, with its platforms.
func TestResolve_ATagToItsIndex(t *testing.T) {
	h := newHarness(t)
	rc := fresh()
	if res, err := call(t, h, rc, "container.image.resolve", true, map[string]any{"image": testsupport.NginxImage}); err != nil || res.Changed {
		t.Fatalf("resolve = %+v, %v", res, err)
	}
	digest, _ := rc.stats["digest"].(string)
	if held := h.repoDigests(t, testsupport.NginxImage); len(held) > 0 && !slices.ContainsFunc(held, func(d string) bool { return strings.HasSuffix(d, "@"+digest) }) {
		t.Errorf("digest = %s, the engine holds %s by %v", digest, testsupport.NginxImage, held)
	}
	if rc.stats["image"] != "nginx@"+digest {
		t.Errorf("image = %v", rc.stats["image"])
	}
	if platforms, _ := rc.stats["platforms"].([]string); !slices.Contains(platforms, "linux/amd64") && !slices.Contains(platforms, "linux/arm64/v8") {
		t.Errorf("platforms = %v", platforms)
	}
	if _, err := call(t, h, fresh(), "container.image.resolve", false, map[string]any{"image": "-x"}); err == nil {
		t.Error("a reference beginning with - was accepted")
	}
}

// TestSearch_ReturnsResults: a search of the public registry returns
// results, and a term that could be read as an option is refused.
func TestSearch_ReturnsResults(t *testing.T) {
	h := newHarness(t)
	rc := fresh()
	if _, err := call(t, h, rc, "container.image.search", false, map[string]any{"term": "nginx", "limit": 3, "official_only": true}); err != nil {
		t.Fatal(err)
	}
	results, _ := rc.stats["results"].([]map[string]any)
	if len(results) == 0 || len(results) > 3 {
		t.Fatalf("results = %v", results)
	}
	for _, r := range results {
		if r["official"] != true {
			t.Errorf("an unofficial result with official_only: %v", r)
		}
	}
	if _, err := call(t, h, fresh(), "container.image.search", false, map[string]any{"term": "-x"}); err == nil {
		t.Error("a term beginning with - was accepted")
	}
	if _, err := call(t, h, fresh(), "container.image.search", false, map[string]any{"term": "x", "limit": 500}); err == nil {
		t.Error("a limit over 100 was accepted")
	}
}

// TestPush_TheEnginesRefusalIsTheTasks: a push to a registry address
// nothing listens on fails with the engine's error, and its check, which
// cannot ask that registry, predicts a push. A digest reference and a
// missing image are refused before anything is sent.
func TestPush_TheEnginesRefusalIsTheTasks(t *testing.T) {
	h := newHarness(t)
	repo := h.repo(t)
	h.imported(t, repo+":1", "push")
	target := "127.0.0.1:9/" + repo + ":1"
	if _, err := call(t, h, fresh(), "container.image.tag", false, map[string]any{"source": repo + ":1", "tag": target}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.api.ImageRemove(context.Background(), target, client.ImageRemoveOptions{}) })
	if res, err := call(t, h, fresh(), "container.image.push", true, map[string]any{"image": target}); err != nil || !res.Changed {
		t.Errorf("a check of push = %+v, %v", res, err)
	}
	rc := fresh()
	if _, err := call(t, h, rc, "container.image.push", false, map[string]any{"image": target}); err == nil || !strings.Contains(err.Error(), "pushing") {
		t.Errorf("a push to nowhere = %v", err)
	}
	if _, ok := rc.stats["inverse"]; ok {
		t.Error("push recorded an undo")
	}
	if _, err := call(t, h, fresh(), "container.image.push", false, map[string]any{"image": "nginx@sha256:" + strings.Repeat("a", 64)}); err == nil || !strings.Contains(err.Error(), "needs a tag") {
		t.Errorf("pushing a digest = %v", err)
	}
	if _, err := call(t, h, fresh(), "container.image.push", false, map[string]any{"image": repo + ":absent"}); err == nil || !strings.Contains(err.Error(), "no image") {
		t.Errorf("pushing a missing image = %v", err)
	}
}

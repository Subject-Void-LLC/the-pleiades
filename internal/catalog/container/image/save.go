// container.image.save: images written to an archive on the machine the
// engine runs on, known by its digest so one task's archive serves every
// later task that names that digest.
package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	fqcnSave   = "container.image.save"
	fqcnLoad   = "container.image.load"
	fqcnImport = "container.image.import"
)

// archiveDigest is an archive's digest as a task names it.
var archiveDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func init() {
	register(fqcnSave, containerengine.MethodManifest(collection.Reversibility{
		Reversible: false,
		Notes:      "An archive this task wrote replaces whatever was at the path, which is not kept; file.remove removes the archive when it is no longer needed.",
	}, saveDoc()), save)
}

// archivePath reads and checks the path parameter.
func archivePath(params map[string]any) (string, error) {
	p, err := sdk.RequiredStringParam(params, paramPath)
	if err != nil {
		return "", err
	}
	return p, containerengine.ValidHostPath(paramPath, p)
}

// wantDigest reads the optional digest parameter.
func wantDigest(params map[string]any) (string, error) {
	d, err := containerengine.TextParam(params, paramDigest)
	if err != nil {
		return "", err
	}
	if d != "" && !archiveDigest.MatchString(d) {
		return "", fmt.Errorf("%s: %q is not sha256:<64 hex digits>", paramDigest, d)
	}
	return d, nil
}

// save is container.image.save's one body: the images' archive, from the
// engine, written to path on the engine's machine unless the file there is
// already that archive. A check streams the archive through a hash and
// writes nothing, so it predicts exactly.
func save(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnSave)
	refs, _, err := sdk.StringSlice(params, paramImages)
	if err != nil {
		return fail(err)
	}
	if len(refs) == 0 {
		return fail(fmt.Errorf("%s is required: the images to save", paramImages))
	}
	for _, ref := range refs {
		if err := validImageRef(paramImages, ref); err != nil {
			return fail(err)
		}
	}
	p, err := archivePath(params)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnSave, containerengine.OpImageInspect, containerengine.OpImageSave)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		img, exists, err := inspect(ctx, s, ref)
		if err != nil {
			return fail(err)
		}
		if !exists {
			return fail(fmt.Errorf("there is no image %s on the engine to save", ref))
		}
		ids = append(ids, img.ID)
	}
	files, err := containerengine.OpenHostFiles(ctx, rc, device, params, fqcnSave)
	if err != nil {
		return collection.Result{}, err
	}
	defer files.Close()
	stream, err := s.API.ImageSave(ctx, refs)
	if err != nil {
		return fail(fmt.Errorf("saving %v: %w", refs, err))
	}
	defer func() { _ = stream.Close() }()

	var stored containerengine.Stored
	if mode == collection.ModeCheck {
		stored.Digest, stored.Size, err = digestOf(stream)
		if err != nil {
			return fail(fmt.Errorf("reading the archive of %v: %w", refs, err))
		}
		old, exists, err := files.Digest(ctx, p)
		if err != nil {
			return fail(err)
		}
		stored.Changed = !exists || old != stored.Digest
	} else if stored, err = files.Store(ctx, p, stream); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: stored.Changed}, recordStats(rc, map[string]any{
		statPath: p, statDigest: stored.Digest, statSize: stored.Size, statImages: ids,
	})
}

// digestOf hashes r to its end.
func digestOf(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

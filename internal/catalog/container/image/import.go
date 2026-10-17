// container.image.import: a filesystem archive made an image, refused
// when its digest is not the one the task names.
package image

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	register(fqcnImport, containerengine.MethodManifest(collection.Reversibility{
		Reversible: true,
		Notes:      "A run that moved an existing tag records pointing it back, guarded by the image it names now, and partial, since the imported image stays (container.image.prune removes it); one under a new tag records removing the imported image, by ID. An import that fails after reaching the engine puts the tag back or removes what it made, since a failed task records no undo.",
		Inverses: []sdk.InverseSpec{
			{FQCN: fqcnTag, Record: []string{paramSource, paramTarget, paramID, containerengine.ParamEngine}, MayBePartial: true},
			{FQCN: fqcnRemove, Record: []string{paramImage, paramID, containerengine.ParamEngine}},
		},
	}, importDoc()), importImage)
}

// importImage is container.image.import's one body: a filesystem archive
// at path, refused when its digest is not the one the task names, made an
// image the tag names.
func importImage(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnImport)
	p, err := archivePath(params)
	if err != nil {
		return fail(err)
	}
	want, err := wantDigest(params)
	if err != nil {
		return fail(err)
	}
	raw, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return fail(err)
	}
	ref, err := taggedRef(paramImage, raw)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnImport, containerengine.OpImageInspect, containerengine.OpImageImport)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	prior, had, err := inspect(ctx, s, ref)
	if err != nil {
		return fail(err)
	}
	files, err := containerengine.OpenHostFiles(ctx, rc, device, params, fqcnImport)
	if err != nil {
		return collection.Result{}, err
	}
	defer files.Close()
	f, got, size, err := spool(ctx, files, p, want, mode == collection.ModeExecute)
	if err != nil {
		return fail(err)
	}
	defer closeSpool(f)
	if err := recordStats(rc, map[string]any{statPath: p, statDigest: got, statSize: size, statImage: ref}); err != nil {
		return collection.Result{}, err
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	res, err := s.API.ImageImport(ctx, client.ImageImportSource{Source: f, SourceName: "-"}, ref, client.ImageImportOptions{})
	if err != nil {
		return fail(fmt.Errorf("importing %s: %w", p, err))
	}
	defer func() { _ = res.Close() }()
	if _, err := containerengine.DrainProgress(res); err != nil {
		return fail(fmt.Errorf("importing %s: %w; %s", p, err, restoreAfterImport(ctx, s, ref, prior.ID, had)))
	}
	img, exists, err := inspect(ctx, s, ref)
	if err != nil {
		return fail(fmt.Errorf("reading %s after importing it: %w; %s", ref, err, restoreAfterImport(ctx, s, ref, prior.ID, had)))
	}
	if !exists {
		return fail(fmt.Errorf("the engine reported %s imported, and has no image by that name", ref))
	}
	if err := rc.SetStat(statImageID, img.ID); err != nil {
		return collection.Result{}, err
	}
	inv := sdk.Inverse{FQCN: fqcnRemove, Params: map[string]any{paramImage: img.ID, paramID: img.ID}, Description: "Remove the image this task imported."}
	if had {
		inv = sdk.Inverse{
			FQCN: fqcnTag, Params: map[string]any{paramSource: prior.ID, paramTarget: ref, paramID: img.ID}, Partial: true,
			Description: "Point the tag back at the image it named before this task; the image this task imported stays, untagged, for container.image.prune.",
		}
	}
	return collection.Result{Changed: true}, containerengine.RecordUndo(rc, s, inv)
}

// restoreAfterImport undoes what an import that failed after reaching the
// engine may have done, since a failed task's undo is dropped
// (FAILURE_PATTERNS 452): a tag that named another image is pointed back,
// and an image made under a name that was absent is removed. It says what
// it did, for the error.
func restoreAfterImport(ctx context.Context, s *containerengine.Session, ref, prior string, had bool) string {
	cctx, cancel := containerengine.CleanupContext(ctx)
	defer cancel()
	if had {
		if _, err := s.API.ImageTag(cctx, client.ImageTagOptions{Source: prior, Target: ref}); err != nil {
			return fmt.Sprintf("pointing %s back at %s failed too: %v", ref, prior, err)
		}
		return fmt.Sprintf("%s was pointed back at %s", ref, prior)
	}
	img, exists, err := inspect(cctx, s, ref)
	if err != nil || !exists {
		return "no image was left under that name"
	}
	if _, err := s.API.ImageRemove(cctx, img.ID, client.ImageRemoveOptions{}); err != nil {
		return fmt.Sprintf("removing the image it may have made failed too: %v", err)
	}
	return "the image it made was removed"
}

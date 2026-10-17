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
	register(fqcnImport, manifest(collection.Reversibility{
		Reversible: true,
		Notes:      "A run records pointing the tag back at the image it named before, or removing the imported image, by ID, when the tag was new.",
		Inverses: []sdk.InverseSpec{
			{FQCN: fqcnTag, Record: []string{paramSource, paramTarget}},
			{FQCN: fqcnRemove, Record: []string{paramImage, paramID}},
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
		return fail(fmt.Errorf("importing %s: %w", p, err))
	}
	img, exists, err := inspect(ctx, s, ref)
	if err != nil || !exists {
		return fail(fmt.Errorf("reading %s after importing it: %v", ref, err))
	}
	if err := rc.SetStat(statImageID, img.ID); err != nil {
		return collection.Result{}, err
	}
	inv := sdk.Inverse{FQCN: fqcnRemove, Params: map[string]any{paramImage: img.ID, paramID: img.ID}, Description: "Remove the image this task imported."}
	if had {
		inv = sdk.Inverse{FQCN: fqcnTag, Params: map[string]any{paramSource: prior.ID, paramTarget: ref}, Description: "Point the tag back at the image it named before this task."}
	}
	return collection.Result{Changed: true}, sdk.RecordInverse(rc, inv)
}

// container.image.load: an archive loaded into the engine, refused when
// its digest is not the one the task names.
package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	register(fqcnLoad, containerengine.MethodManifest(collection.Reversibility{
		Reversible: false,
		Notes:      "A load can move tags the archive names from the images they named before, which the engine does not report, so what to put back is not known.",
	}, loadDoc()), load)
}

// spool copies the archive at p on the engine's machine into a private
// temporary file here, hashing it on the way, and refuses it when want is
// set and differs. What the engine then reads is the temporary, so the
// bytes it loads are the bytes that were checked, whatever happens to p
// meanwhile. In a check nothing is kept: the digest is all it needs.
func spool(ctx context.Context, files *containerengine.HostFiles, p, want string, keep bool) (*os.File, string, int64, error) {
	src, err := files.Open(ctx, p)
	if errors.Is(err, containerengine.ErrHostFileAbsent) {
		return nil, "", 0, fmt.Errorf("there is no archive at %s", p)
	}
	if err != nil {
		return nil, "", 0, err
	}
	defer func() { _ = src.Close() }()
	var sink io.Writer = io.Discard
	var tmp *os.File
	if keep {
		if tmp, err = os.CreateTemp("", "pleiades-archive-*"); err != nil {
			return nil, "", 0, fmt.Errorf("spooling %s: %w", p, err)
		}
		sink = tmp
	}
	discard := func() {
		if tmp != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(sink, h), src)
	if err != nil {
		discard()
		return nil, "", 0, fmt.Errorf("reading %s: %w", p, err)
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		discard()
		return nil, got, n, fmt.Errorf("the archive at %s is %s, not the %s the task names, so it is not loaded", p, got, want)
	}
	if tmp != nil {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			discard()
			return nil, "", 0, fmt.Errorf("spooling %s: %w", p, err)
		}
	}
	return tmp, got, n, nil
}

// closeSpool removes a spooled archive.
func closeSpool(f *os.File) {
	if f != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
}

// load is container.image.load's one body: the archive at path, refused
// when its digest is not the one the task names, loaded into the engine.
func load(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnLoad)
	p, err := archivePath(params)
	if err != nil {
		return fail(err)
	}
	want, err := wantDigest(params)
	if err != nil {
		return fail(err)
	}
	s, err := open(ctx, rc, device, params, fqcnLoad, containerengine.OpImageLoad)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	files, err := containerengine.OpenHostFiles(ctx, rc, device, params, fqcnLoad)
	if err != nil {
		return collection.Result{}, err
	}
	defer files.Close()
	f, got, size, err := spool(ctx, files, p, want, mode == collection.ModeExecute)
	if err != nil {
		return fail(err)
	}
	defer closeSpool(f)
	if err := recordStats(rc, map[string]any{statPath: p, statDigest: got, statSize: size}); err != nil {
		return collection.Result{}, err
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	res, err := s.API.ImageLoad(ctx, f, client.ImageLoadWithQuiet(true))
	if err != nil {
		return fail(fmt.Errorf("loading %s: %w", p, err))
	}
	defer func() { _ = res.Close() }()
	lines, err := containerengine.DrainProgress(res)
	if err != nil {
		return fail(fmt.Errorf("loading %s: %w", p, err))
	}
	return collection.Result{Changed: true}, rc.SetStat(statImages, nonNil(containerengine.LoadedImages(lines)))
}
